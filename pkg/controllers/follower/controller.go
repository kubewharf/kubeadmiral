/*
Copyright 2023 The KubeAdmiral Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package follower

import (
	"context"
	"fmt"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	podutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	ControllerName         = "follower-controller"
	PrefixedControllerName = common.DefaultPrefix + ControllerName
)

const (
	EventReasonFailedInferFollowers = "FailedInferFollowers"
	EventReasonFailedUpdateFollower = "FailedUpdateFollower"
)

var (
	// Map from supported leader type to pod spec path. These values will be initialized by podutil.PodSpecPaths
	leaderPodSpecPaths = map[schema.GroupKind]string{
		{Group: appsv1.GroupName, Kind: common.DeploymentKind}:  "",
		{Group: appsv1.GroupName, Kind: common.StatefulSetKind}: "",
		{Group: appsv1.GroupName, Kind: common.DaemonSetKind}:   "",
		{Group: batchv1.GroupName, Kind: common.JobKind}:        "",
		{Group: batchv1.GroupName, Kind: common.CronJobKind}:    "",
		{Group: "", Kind: common.PodKind}:                       "",
	}

	supportedFollowerTypes = sets.New(
		schema.GroupKind{Group: "", Kind: common.ConfigMapKind},
		schema.GroupKind{Group: "", Kind: common.SecretKind},
		schema.GroupKind{Group: "", Kind: common.PersistentVolumeClaimKind},
		schema.GroupKind{Group: "", Kind: common.ServiceAccountKind},
		schema.GroupKind{Group: "", Kind: common.ServiceKind},
		schema.GroupKind{Group: networkingv1.GroupName, Kind: common.IngressKind},
	)
)

func init() {
	for k := range leaderPodSpecPaths {
		leaderPodSpecPaths[k] = podutil.PodSpecPaths[k]
	}
}

// TODO: limit max number of leaders per follower to prevent oversized follower objects?
// TODO: support handles-object annotations in this controller?
// TODO: support parsing followers introduced by overrides

type Controller struct {
	gkToFTCLock sync.RWMutex
	gkToFTCName map[schema.GroupKind]string

	cacheObservedFromLeaders   *bidirectionalCache[fedcorev1a1.LeaderReference, FollowerReference]
	cacheObservedFromFollowers *bidirectionalCache[FollowerReference, fedcorev1a1.LeaderReference]

	worker                   worker.ReconcileWorker[objectGroupKindKey]
	informerManager          informermanager.InformerManager
	ftcInformer              fedcorev1a1informers.FederatedTypeConfigInformer
	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer

	fedClient     fedclient.Interface
	eventRecorder record.EventRecorder
	metrics       stats.Metrics
	logger        klog.Logger
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func NewFollowerController(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,
	informerManager informermanager.InformerManager,
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		gkToFTCName:                make(map[schema.GroupKind]string),
		informerManager:            informerManager,
		ftcInformer:                ftcInformer,
		fedObjectInformer:          fedObjectInformer,
		clusterFedObjectInformer:   clusterFedObjectInformer,
		cacheObservedFromLeaders:   newBidirectionalCache[fedcorev1a1.LeaderReference, FollowerReference](),
		cacheObservedFromFollowers: newBidirectionalCache[FollowerReference, fedcorev1a1.LeaderReference](),
		fedClient:                  fedClient,
		eventRecorder:              eventsink.NewDefederatingRecorderMux(kubeClient, ControllerName, 4),
		metrics:                    metrics,
		logger:                     logger.WithValues("controller", ControllerName),
	}

	if _, err := c.fedObjectInformer.Informer().AddEventHandlerWithResyncPeriod(
		eventhandlers.NewTriggerOnAllChanges(c.enqueueSupportedType),
		0,
	); err != nil {
		return nil, err
	}

	if _, err := c.clusterFedObjectInformer.Informer().AddEventHandlerWithResyncPeriod(
		eventhandlers.NewTriggerOnAllChanges(c.enqueueSupportedType),
		0,
	); err != nil {
		return nil, err
	}

	c.worker = worker.NewReconcileWorker[objectGroupKindKey](
		"follower-controller-worker",
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		c.metrics,
	)

	if err := c.informerManager.AddFTCUpdateHandler(func(_, latest *fedcorev1a1.FederatedTypeConfig) {
		if latest == nil {
			return
		}
		targetType := latest.Spec.SourceType
		targetGK := schema.GroupKind{Group: targetType.Group, Kind: targetType.Kind}

		c.gkToFTCLock.Lock()
		defer c.gkToFTCLock.Unlock()

		c.gkToFTCName[targetGK] = latest.Name
	}); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Controller) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(ControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for caches to sync")
		return
	}
	logger.Info("Caches are synced")
	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *Controller) HasSynced() bool {
	return c.fedObjectInformer.Informer().HasSynced() &&
		c.clusterFedObjectInformer.Informer().HasSynced()
}

func (c *Controller) enqueueSupportedType(object interface{}) {
	fedObject, ok := object.(fedcorev1a1.GenericFederatedObject)
	if !ok {
		return
	}

	template, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		return
	}

	templateGK := template.GroupVersionKind().GroupKind()
	_, isLeader := leaderPodSpecPaths[templateGK]
	isHPAType, _ := c.isHPAType(templateGK)
	isFollower := supportedFollowerTypes.Has(templateGK) || isHPAType
	if isLeader || isFollower {
		c.worker.Enqueue(objectGroupKindKey{
			sourceGK:   templateGK,
			namespace:  fedObject.GetNamespace(),
			fedName:    fedObject.GetName(),
			sourceName: template.GetName(),
		})
	}
}

func (c *Controller) reconcile(ctx context.Context, key objectGroupKindKey) (status worker.Result) {
	if _, exists := leaderPodSpecPaths[key.sourceGK]; exists {
		return c.reconcileLeader(ctx, key)
	}

	isHPAType, err := c.isHPAType(key.sourceGK)
	if err != nil {
		return worker.StatusError
	}

	if supportedFollowerTypes.Has(key.sourceGK) || isHPAType {
		return c.reconcileFollower(ctx, key)
	}

	return worker.StatusAllOK
}

/*
Reconciles the leader to make sure its desired followers (derivable from the leader object) reference it,
and its stale followers (derivable from cache) no longer reference it.
*/
func (c *Controller) reconcileLeader(
	ctx context.Context,
	key objectGroupKindKey,
) (status worker.Result) {
	c.metrics.Counter(metrics.FollowerControllerThroughput, 1)
	//nolint:staticcheck
	ctx, keyedLogger := logging.InjectLoggerValues(
		ctx,
		"origin",
		"reconcileLeader",
		"type",
		key.sourceGK.String(),
		"key",
		key.ObjectSourceKey(),
	)

	startTime := time.Now()
	keyedLogger.V(3).Info("Starting reconcileLeader")
	defer func() {
		c.metrics.Duration(
			metrics.FollowerControllerLatency,
			startTime,
			stats.Tag{Name: "name", Value: key.sourceGK.String()},
			stats.Tag{Name: "source", Value: "leader"},
		)
		keyedLogger.WithValues(
			"duration",
			time.Since(startTime),
			"status",
			status.String(),
		).V(3).Info("Finished reconcileLeader")
	}()

	leader := fedcorev1a1.LeaderReference{
		Group:     key.sourceGK.Group,
		Kind:      key.sourceGK.Kind,
		Namespace: key.namespace,
		Name:      key.sourceName,
	}

	fedObj, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		key.namespace,
		key.fedName,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get leader object from store")
		return worker.StatusError
	}

	if apierrors.IsNotFound(err) {
		fedObj = nil
	}

	var desiredFollowers sets.Set[FollowerReference]
	if fedObj != nil {
		// Only leaders that have not been deleted should have followers
		desiredFollowers, err = c.inferFollowers(key.sourceGK, fedObj)
		if err != nil {
			keyedLogger.Error(err, "Failed to infer followers")
			if fedObj != nil {
				c.eventRecorder.Eventf(
					fedObj,
					corev1.EventTypeWarning,
					EventReasonFailedInferFollowers,
					"Failed to infer followers: %v",
					err,
				)
			}
			return worker.StatusError
		}

		c.metrics.Store(metrics.FollowersTotal, len(desiredFollowers),
			stats.Tag{Name: "namespace", Value: key.namespace},
			stats.Tag{Name: "name", Value: key.sourceName},
			stats.Tag{Name: "group", Value: key.sourceGK.Group},
			stats.Tag{Name: "kind", Value: key.sourceGK.Kind})
	}

	c.cacheObservedFromLeaders.update(leader, desiredFollowers)
	currentFollowers := c.cacheObservedFromFollowers.reverseLookup(leader)

	// enqueue all followers whose desired state may have changed
	c.enqueueFollowers(desiredFollowers.Union(currentFollowers))

	return worker.StatusAllOK
}

func (c *Controller) enqueueFollowers(followers sets.Set[FollowerReference]) {
	c.gkToFTCLock.RLock()
	defer c.gkToFTCLock.RUnlock()

	// enqueue all followers whose desired state may have changed
	for follower := range followers {
		ftcName, exists := c.gkToFTCName[follower.GroupKind]
		if !exists {
			continue
		}

		c.worker.Enqueue(
			objectGroupKindKey{
				sourceGK:   follower.GroupKind,
				namespace:  follower.Namespace,
				sourceName: follower.Name,
				fedName:    naming.GenerateFederatedObjectName(follower.Name, ftcName),
			},
		)
	}
}

func (c *Controller) inferFollowers(
	sourceGK schema.GroupKind,
	fedObj fedcorev1a1.GenericFederatedObject,
) (sets.Set[FollowerReference], error) {
	if fedObj.GetAnnotations()[common.EnableFollowerSchedulingAnnotation] != common.AnnotationValueTrue ||
		fedObj.GetAnnotations()[common.NoSchedulingAnnotation] == common.AnnotationValueTrue || skipSync(fedObj) {
		// follower scheduling is not enabled
		return nil, nil
	}

	followersFromAnnotation, err := GetFollowersFromAnnotation(fedObj)
	if err != nil {
		return nil, err
	}

	followersFromPodSpec, err := getFollowersFromPodSpec(
		fedObj,
		leaderPodSpecPaths[sourceGK],
	)
	if err != nil {
		return nil, err
	}

	return followersFromAnnotation.Union(followersFromPodSpec), err
}

func (c *Controller) updateFollower(
	ctx context.Context,
	followerObj fedcorev1a1.GenericFederatedObject,
	leadersChanged bool,
	leaders []fedcorev1a1.LeaderReference,
) (updated bool, err error) {
	logger := klog.FromContext(ctx)

	if leadersChanged {
		followerObj.GetSpec().Follows = leaders
	}

	clusters, err := c.leaderPlacementUnion(leaders)
	if err != nil {
		return false, fmt.Errorf("get leader placement union: %w", err)
	}

	placementsChanged := followerObj.GetSpec().SetControllerPlacement(PrefixedControllerName, clusters.UnsortedList())
	needsUpdate := leadersChanged || placementsChanged
	if needsUpdate {
		logger.V(1).Info("Updating follower to sync with leaders")
		_, err = fedobjectadapters.Update(ctx, c.fedClient.CoreV1alpha1(), followerObj, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Errorf("update follower: %w", err)
		}
		return true, nil
	}

	return false, nil
}

/*
Reconciles the follower so it references the desired leaders and has the correct placements.
*/
func (c *Controller) reconcileFollower(
	ctx context.Context,
	key objectGroupKindKey,
) (status worker.Result) {
	c.metrics.Counter(metrics.FollowerControllerThroughput, 1)
	ctx, keyedLogger := logging.InjectLoggerValues(
		ctx,
		"origin",
		"reconcileFollower",
		"type",
		key.sourceGK.String(),
		"key",
		key.ObjectSourceKey(),
	)

	startTime := time.Now()
	keyedLogger.V(3).Info("Starting reconcileFollower")
	defer func() {
		c.metrics.Duration(
			"follower_controller_latency",
			startTime,
			stats.Tag{Name: "name", Value: key.sourceGK.String()},
			stats.Tag{Name: "source", Value: "follower"},
		)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status.String()).
			V(3).
			Info("Finished reconcileFollower")
	}()

	follower := FollowerReference{
		GroupKind: key.sourceGK,
		Namespace: key.namespace,
		Name:      key.sourceName,
	}

	followerObject, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		key.namespace,
		key.fedName,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get follower object from store")
		return worker.StatusError
	}

	if apierrors.IsNotFound(err) {
		// The deleted follower no longer references any leaders
		c.cacheObservedFromFollowers.update(follower, nil)
		return worker.StatusAllOK
	}

	currentLeaders := sets.New(followerObject.GetSpec().Follows...)
	c.cacheObservedFromFollowers.update(follower, currentLeaders)

	// If the follower resource is annotated with `kubeadmiral.io/disable-following="true"`,
	// the resource will not follow any leader resources for scheduling
	var desiredLeaders sets.Set[fedcorev1a1.LeaderReference]
	if followerObject.GetAnnotations()[common.DisableFollowingAnnotation] == common.AnnotationValueTrue {
		desiredLeaders = sets.New[fedcorev1a1.LeaderReference]()
	} else {
		desiredLeaders = c.cacheObservedFromLeaders.reverseLookup(follower)
	}

	leadersChanged := !equality.Semantic.DeepEqual(desiredLeaders, currentLeaders)
	updated, err := c.updateFollower(
		ctx,
		followerObject,
		leadersChanged,
		desiredLeaders.UnsortedList(),
	)
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		keyedLogger.Error(err, "Failed to update follower")
		c.eventRecorder.Eventf(
			followerObject,
			corev1.EventTypeWarning,
			EventReasonFailedUpdateFollower,
			"Failed to update follower to sync with leader placements: %v",
			err,
		)
		return worker.StatusError
	} else if updated {
		c.metrics.Store(metrics.LeadersTotal, len(desiredLeaders),
			stats.Tag{Name: "namespace", Value: key.namespace},
			stats.Tag{Name: "name", Value: key.sourceName},
			stats.Tag{Name: "group", Value: key.sourceGK.Group},
			stats.Tag{Name: "kind", Value: key.sourceGK.Kind})
		keyedLogger.V(1).Info("Updated follower to sync with leaders")
	}

	return worker.StatusAllOK
}

func (c *Controller) getLeaderObj(
	leader fedcorev1a1.LeaderReference,
) (fedcorev1a1.GenericFederatedObject, error) {
	leaderGK := leader.GroupKind()
	_, exists := leaderPodSpecPaths[leaderGK]
	if !exists {
		return nil, fmt.Errorf("unsupported leader type %v", leaderGK)
	}

	c.gkToFTCLock.RLock()
	defer c.gkToFTCLock.RUnlock()
	ftcName, exists := c.gkToFTCName[leaderGK]
	if !exists {
		return nil, fmt.Errorf("unknown leader gk %v", leaderGK)
	}

	leaderName := naming.GenerateFederatedObjectName(leader.Name, ftcName)
	leaderObj, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		leader.Namespace,
		leaderName,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get from store: %w", err)
	}
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	return leaderObj, nil
}

func (c *Controller) leaderPlacementUnion(
	leaders []fedcorev1a1.LeaderReference,
) (sets.Set[string], error) {
	clusters := sets.New[string]()
	for _, leader := range leaders {
		leaderObjWithPlacement, err := c.getLeaderObj(leader)
		if err != nil {
			return nil, fmt.Errorf("get leader object %v: %w", leader, err)
		}
		if leaderObjWithPlacement == nil {
			continue
		}

		clusters = leaderObjWithPlacement.GetSpec().GetPlacementUnion().Union(clusters)
	}

	return clusters, nil
}

func (c *Controller) isHPAType(gk schema.GroupKind) (bool, error) {
	c.gkToFTCLock.RLock()
	defer c.gkToFTCLock.RUnlock()

	ftcName := c.gkToFTCName[gk]
	ftc, err := c.ftcInformer.Lister().Get(ftcName)
	if err != nil {
		c.logger.Error(err, "Failed to get ftc")
		return false, err
	}

	_, ok := ftc.GetAnnotations()[common.HPAScaleTargetRefPath]
	return ok, nil
}
