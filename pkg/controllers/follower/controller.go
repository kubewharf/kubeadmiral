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
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
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
	// Map from supported leader type to pod template path.
	// TODO: think about whether PodTemplatePath/PodSpecPath should be specified in the FTC instead.
	// Specifying in the FTC allows changing the path according to the api version.
	// Other controllers should consider using the specified paths instead of hardcoded paths.
	leaderPodTemplatePaths = map[schema.GroupKind]string{
		{Group: appsv1.GroupName, Kind: common.DeploymentKind}:  "spec.template",
		{Group: appsv1.GroupName, Kind: common.StatefulSetKind}: "spec.template",
		{Group: appsv1.GroupName, Kind: common.DaemonSetKind}:   "spec.template",
		{Group: batchv1.GroupName, Kind: common.JobKind}:        "spec.template",
		{Group: batchv1.GroupName, Kind: common.CronJobKind}:    "spec.jobTemplate.spec.template",
		{Group: "", Kind: common.PodKind}:                       "spec",
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

// TODO: limit max number of leaders per follower to prevent oversized follower objects?
// TODO: support handles-object annotations in this controller?
// TODO: support parsing followers introduced by overrides

// Handles for a leader or follower type.
type typeHandles struct {
	// <leader|follower>+sourceKind
	name       string
	typeConfig *fedcorev1a1.FederatedTypeConfig
	sourceGK   schema.GroupKind
}

type Controller struct {
	name string

	eventRecorder record.EventRecorder
	metrics       stats.Metrics
	logger        klog.Logger

	// The following maps are written during initialization and only read afterward,
	// therefore no locks are required.

	// map from leader object GroupKind to typeHandle
	leaderTypeHandles map[schema.GroupKind]*typeHandles
	// map from follower object GroupKind to typeHandle
	followerTypeHandles map[schema.GroupKind]*typeHandles

	cacheObservedFromLeaders   *bidirectionalCache[fedcorev1a1.LeaderReference, FollowerReference]
	cacheObservedFromFollowers *bidirectionalCache[FollowerReference, fedcorev1a1.LeaderReference]

	worker                  worker.ReconcileWorker[SourceGroupKindKey]
	federatedObjectInformer fedcorev1a1informers.FederatedObjectInformer
	fedClient               fedclient.Interface
}

type SourceGroupKindKey struct {
	sourceGK      schema.GroupKind
	federatedName common.QualifiedName
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func NewFollowerController(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,
	informerFactory fedinformers.SharedInformerFactory,
	metrics stats.Metrics,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		name:                       ControllerName,
		eventRecorder:              eventsink.NewDefederatingRecorderMux(kubeClient, ControllerName, 4),
		metrics:                    metrics,
		logger:                     klog.LoggerWithValues(klog.Background(), "controller", ControllerName),
		federatedObjectInformer:    informerFactory.Core().V1alpha1().FederatedObjects(),
		leaderTypeHandles:          make(map[schema.GroupKind]*typeHandles),
		followerTypeHandles:        make(map[schema.GroupKind]*typeHandles),
		cacheObservedFromLeaders:   newBidirectionalCache[fedcorev1a1.LeaderReference, FollowerReference](),
		cacheObservedFromFollowers: newBidirectionalCache[FollowerReference, fedcorev1a1.LeaderReference](),
		fedClient:                  fedClient,
	}

	c.federatedObjectInformer.Informer().AddEventHandlerWithResyncPeriod(
		c.NewTriggerWithSupportedTypeFilter(),
		util.NoResyncPeriod,
	)

	c.worker = worker.NewReconcileWorker[SourceGroupKindKey](
		"follower-controller-worker",
		nil,
		func(key SourceGroupKindKey) worker.Result {
			return c.reconcile(key)
		},
		worker.RateLimiterOptions{},
		workerCount,
		c.metrics,
	)

	getHandles := func(
		ftc *fedcorev1a1.FederatedTypeConfig,
		handleNamePrefix string,
	) *typeHandles {
		targetType := ftc.Spec.SourceType
		return &typeHandles{
			name:       handleNamePrefix + "-" + targetType.Kind,
			typeConfig: ftc,
			sourceGK:   schema.GroupKind{Group: targetType.Group, Kind: targetType.Kind},
		}
	}

	ftcs, err := c.fedClient.CoreV1alpha1().
		FederatedTypeConfigs().
		List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// Find the supported leader and follower types and create their handles
	for i := range ftcs.Items {
		ftc := &ftcs.Items[i]
		targetType := ftc.Spec.SourceType
		targetGK := schema.GroupKind{Group: targetType.Group, Kind: targetType.Kind}

		if _, exists := leaderPodTemplatePaths[targetGK]; exists {
			handles := getHandles(ftc, "leader")
			c.leaderTypeHandles[targetGK] = handles
			c.logger.V(2).Info(fmt.Sprintf("Found supported leader FederatedTypeConfig %s", ftc.Name))
		} else if supportedFollowerTypes.Has(targetGK) {
			handles := getHandles(ftc, "follower")
			c.followerTypeHandles[targetGK] = handles
			c.logger.V(2).Info(fmt.Sprintf("Found supported follower FederatedTypeConfig %s", ftc.Name))
		}
	}

	return c, nil
}

func (c *Controller) Run(stopChan <-chan struct{}) {
	c.logger.Info("Starting controller")
	defer c.logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(c.name, stopChan, c.HasSynced) {
		return
	}

	c.worker.Run(stopChan)
	<-stopChan
}

func (c *Controller) HasSynced() bool {
	return c.federatedObjectInformer.Informer().HasSynced()
}

func (c *Controller) NewTriggerWithSupportedTypeFilter() *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(old interface{}) {
			if deleted, ok := old.(cache.DeletedFinalStateUnknown); ok {
				// This object might be stale but ok for our current usage.
				old = deleted.Obj
				if old == nil {
					return
				}
			}
			c.enqueueSupportedType(old)
		},
		AddFunc: func(cur interface{}) {
			c.enqueueSupportedType(cur)
		},
		UpdateFunc: func(old, cur interface{}) {
			if !reflect.DeepEqual(old, cur) {
				c.enqueueSupportedType(cur)
			}
		},
	}
}

func (c *Controller) enqueueSupportedType(object interface{}) {
	fedObject, ok := object.(*fedcorev1a1.FederatedObject)
	if !ok {
		return
	}

	templateObj := &unstructured.Unstructured{}
	err := templateObj.UnmarshalJSON(fedObject.Spec.Template.Raw)
	if err != nil {
		return
	}

	sourceGK := templateObj.GroupVersionKind().GroupKind()
	if _, exists := leaderPodTemplatePaths[sourceGK]; exists {
		c.worker.Enqueue(SourceGroupKindKey{
			sourceGK:      sourceGK,
			federatedName: common.QualifiedName{Namespace: fedObject.Namespace, Name: fedObject.Name},
		})
	}

	if supportedFollowerTypes.Has(templateObj.GroupVersionKind().GroupKind()) {
		c.worker.Enqueue(SourceGroupKindKey{
			sourceGK:      sourceGK,
			federatedName: common.QualifiedName{Namespace: fedObject.Namespace, Name: fedObject.Name},
		})
	}
}

func (c *Controller) reconcile(key SourceGroupKindKey) (status worker.Result) {
	if leaderHandle, exists := c.leaderTypeHandles[key.sourceGK]; exists {
		return c.reconcileLeader(leaderHandle, key.federatedName)
	}

	if followerHandle, exists := c.followerTypeHandles[key.sourceGK]; exists {
		return c.reconcileFollower(followerHandle, key.federatedName)
	}

	return worker.StatusAllOK
}

/*
Reconciles the leader to make sure its desired followers (derivable from the leader object) reference it,
and its stale followers (derivable from cache) no longer reference it.
*/
func (c *Controller) reconcileLeader(
	handles *typeHandles,
	qualifiedName common.QualifiedName,
) (status worker.Result) {
	c.metrics.Rate(fmt.Sprintf("follower-controller-%s.throughput", handles.name), 1)
	key := qualifiedName.String()
	logger := c.logger.WithValues("origin", "reconcileLeader", "type", handles.name, "key", key)
	startTime := time.Now()

	logger.V(3).Info("Starting reconcileLeader")
	defer func() {
		c.metrics.Duration(fmt.Sprintf("follower-controller-%s.latency", handles.name), startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcileLeader")
	}()

	leader := fedcorev1a1.LeaderReference{
		Group:     handles.sourceGK.Group,
		Kind:      handles.sourceGK.Kind,
		Namespace: qualifiedName.Namespace,
		Name:      qualifiedName.Name,
	}

	fedObj, err := c.federatedObjectInformer.Lister().FederatedObjects(qualifiedName.Namespace).Get(qualifiedName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get follower object from store")
		return worker.StatusError
	}

	if apierrors.IsNotFound(err) {
		fedObj = nil
	}

	var desiredFollowers sets.Set[FollowerReference]
	if fedObj != nil {
		// We only need to check for dependencies if the object exists
		if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(fedObj, PrefixedControllerName); err != nil {
			logger.Error(err, "Failed to check controller dependencies")
			return worker.StatusError
		} else if !ok {
			return worker.StatusAllOK
		}

		// Only leaders that have not been deleted should have followers
		desiredFollowers, err = c.inferFollowers(handles, fedObj)
		if err != nil {
			logger.Error(err, "Failed to infer followers")
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
	}
	c.cacheObservedFromLeaders.update(leader, desiredFollowers)
	currentFollowers := c.cacheObservedFromFollowers.reverseLookup(leader)

	// enqueue all followers whose desired state may have changed
	for follower := range desiredFollowers.Union(currentFollowers) {
		c.worker.Enqueue(
			SourceGroupKindKey{
				sourceGK: follower.GroupKind,
				federatedName: common.QualifiedName{
					Namespace: follower.Namespace,
					Name:      util.GenerateFederatedObjectName(follower.Name, c.followerTypeHandles[follower.GroupKind].typeConfig.Name)},
			},
		)
	}

	if fedObj != nil {
		updated, err := pendingcontrollers.UpdatePendingControllers(
			fedObj,
			PrefixedControllerName,
			false,
			handles.typeConfig.GetControllers(),
		)
		if err != nil {
			logger.Error(err, "Failed to set pending controllers")
			return worker.StatusError
		}
		if updated {
			logger.V(1).Info("Updating leader to sync with pending controllers")
			_, err = c.fedClient.CoreV1alpha1().FederatedObjects(qualifiedName.Namespace).
				Update(context.Background(), fedObj, metav1.UpdateOptions{})
			if err != nil {
				if apierrors.IsConflict(err) {
					return worker.StatusConflict
				}
				logger.Error(err, "Failed to update after modifying pending controllers")
				return worker.StatusError
			}
		}
	}

	return worker.StatusAllOK
}

func (c *Controller) inferFollowers(
	handles *typeHandles,
	fedObj *fedcorev1a1.FederatedObject,
) (sets.Set[FollowerReference], error) {
	if fedObj.GetAnnotations()[common.EnableFollowerSchedulingAnnotation] != common.AnnotationValueTrue {
		// follower scheduling is not enabled
		return nil, nil
	}

	followersFromAnnotation, err := getFollowersFromAnnotation(fedObj)
	if err != nil {
		return nil, err
	}

	followersFromPodTemplate, err := getFollowersFromPodTemplate(
		fedObj,
		leaderPodTemplatePaths[handles.sourceGK],
	)
	if err != nil {
		return nil, err
	}

	return followersFromAnnotation.Union(followersFromPodTemplate), err
}

func (c *Controller) updateFollower(
	ctx context.Context,
	followerObj *fedcorev1a1.FederatedObject,
	leadersChanged bool,
	leaders []fedcorev1a1.LeaderReference,
) (updated bool, err error) {
	logger := klog.FromContext(ctx)

	if leadersChanged {
		followerObj.Spec.Follows = leaders
	}

	clusters, err := c.leaderPlacementUnion(leaders)
	if err != nil {
		return false, fmt.Errorf("get leader placement union: %w", err)
	}

	clusterNames := make([]string, 0, len(clusters))
	for clusterName := range clusters {
		clusterNames = append(clusterNames, clusterName)
	}

	placementsChanged := followerObj.SetControllerPlacement(PrefixedControllerName, clusterNames)
	needsUpdate := leadersChanged || placementsChanged
	if needsUpdate {
		logger.V(1).Info("Updating follower to sync with leaders")
		_, err = c.fedClient.CoreV1alpha1().FederatedObjects(followerObj.GetNamespace()).
			Update(context.TODO(), followerObj, metav1.UpdateOptions{})
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
	handles *typeHandles,
	qualifiedName common.QualifiedName,
) (status worker.Result) {
	c.metrics.Rate(fmt.Sprintf("follower-controller-%s.throughput", handles.name), 1)
	key := qualifiedName.String()
	logger := c.logger.WithValues("origin", "reconcileFollower", "type", handles.name, "key", key)
	ctx := klog.NewContext(context.TODO(), logger)
	startTime := time.Now()

	logger.V(3).Info("Starting reconcileFollower")
	defer func() {
		c.metrics.Duration(fmt.Sprintf("follower-controller-%s.latency", handles.name), startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcileFollower")
	}()

	follower := FollowerReference{
		GroupKind: handles.sourceGK,
		Namespace: qualifiedName.Namespace,
		Name:      qualifiedName.Name,
	}

	followerObject, err := c.federatedObjectInformer.Lister().FederatedObjects(qualifiedName.Namespace).Get(qualifiedName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get follower object from store")
		return worker.StatusError
	}

	if apierrors.IsNotFound(err) {
		// The deleted follower no longer references any leaders
		c.cacheObservedFromFollowers.update(follower, nil)
		return worker.StatusAllOK
	}

	currentLeaders := sets.New(followerObject.Spec.Follows...)
	c.cacheObservedFromFollowers.update(follower, currentLeaders)

	desiredLeaders := c.cacheObservedFromLeaders.reverseLookup(follower)

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
		logger.Error(err, "Failed to update follower")
		c.eventRecorder.Eventf(
			followerObject,
			corev1.EventTypeWarning,
			EventReasonFailedUpdateFollower,
			"Failed to update follower to sync with leader placements: %v",
			err,
		)
		return worker.StatusError
	} else if updated {
		logger.V(1).Info("Updated follower to sync with leaders")
	}

	return worker.StatusAllOK
}

func (c *Controller) getLeaderObj(
	leader fedcorev1a1.LeaderReference,
) (*typeHandles, *fedcorev1a1.FederatedObject, error) {
	leaderGK := leader.GroupKind()
	handles, exists := c.leaderTypeHandles[leaderGK]
	if !exists {
		return nil, nil, fmt.Errorf("unsupported leader type %v", leaderGK)
	}

	leaderObj, err := c.federatedObjectInformer.Lister().FederatedObjects(leader.Namespace).Get(util.GenerateFederatedObjectName(leader.Name, handles.typeConfig.Name))
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, nil, fmt.Errorf("get from store: %w", err)
	}
	if apierrors.IsNotFound(err) {
		return nil, nil, nil
	}

	return handles, leaderObj, nil
}

func (c *Controller) leaderPlacementUnion(
	leaders []fedcorev1a1.LeaderReference,
) (map[string]struct{}, error) {
	clusters := map[string]struct{}{}
	for _, leader := range leaders {
		_, leaderObjWithPlacement, err := c.getLeaderObj(leader)
		if err != nil {
			return nil, fmt.Errorf("get leader object %v: %w", leader, err)
		}
		if leaderObjWithPlacement == nil {
			continue
		}
		for cluster := range leaderObjWithPlacement.GetPlacementUnion() {
			clusters[cluster] = struct{}{}
		}
	}

	return clusters, nil
}
