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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
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
	// <leader|follower>+federatedKind
	name        string
	typeConfig  *fedcorev1a1.FederatedTypeConfig
	sourceGK    schema.GroupKind
	federatedGK schema.GroupKind
	informer    informers.GenericInformer
	client      dynamic.NamespaceableResourceInterface
	worker      worker.ReconcileWorker
}

type Controller struct {
	name string

	eventRecorder record.EventRecorder
	metrics       stats.Metrics
	logger        klog.Logger

	// The following maps are written during initialization and only read afterward,
	// therefore no locks are required.

	// map from source GroupKind to federated GroupKind
	sourceToFederatedGKMap map[schema.GroupKind]schema.GroupKind
	// map from leader federated GroupKind to typeHandle
	leaderTypeHandles map[schema.GroupKind]*typeHandles
	// map from follower federated GroupKind to typeHandle
	followerTypeHandles map[schema.GroupKind]*typeHandles

	cacheObservedFromLeaders   *bidirectionalCache[fedtypesv1a1.LeaderReference, FollowerReference]
	cacheObservedFromFollowers *bidirectionalCache[FollowerReference, fedtypesv1a1.LeaderReference]

	kubeClient kubernetes.Interface
	fedClient  fedclient.Interface
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func NewFollowerController(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	fedClient fedclient.Interface,
	informerFactory dynamicinformer.DynamicSharedInformerFactory,
	metrics stats.Metrics,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		name:                       ControllerName,
		eventRecorder:              eventsink.NewDefederatingRecorderMux(kubeClient, ControllerName, 4),
		metrics:                    metrics,
		logger:                     klog.LoggerWithValues(klog.Background(), "controller", ControllerName),
		sourceToFederatedGKMap:     make(map[schema.GroupKind]schema.GroupKind),
		leaderTypeHandles:          make(map[schema.GroupKind]*typeHandles),
		followerTypeHandles:        make(map[schema.GroupKind]*typeHandles),
		cacheObservedFromLeaders:   newBidirectionalCache[fedtypesv1a1.LeaderReference, FollowerReference](),
		cacheObservedFromFollowers: newBidirectionalCache[FollowerReference, fedtypesv1a1.LeaderReference](),
		kubeClient:                 kubeClient,
		fedClient:                  fedClient,
	}

	getHandles := func(
		ftc *fedcorev1a1.FederatedTypeConfig,
		handleNamePrefix string,
		reconcile func(*typeHandles, common.QualifiedName) worker.Result,
	) *typeHandles {
		targetType := ftc.GetTargetType()
		federatedType := ftc.GetFederatedType()
		federatedGVR := schemautil.APIResourceToGVR(&federatedType)

		handles := &typeHandles{
			name:        handleNamePrefix + "-" + federatedType.Kind,
			typeConfig:  ftc,
			sourceGK:    schemautil.APIResourceToGVK(&targetType).GroupKind(),
			federatedGK: schemautil.APIResourceToGVK(&federatedType).GroupKind(),
			informer:    informerFactory.ForResource(federatedGVR),
			client:      dynamicClient.Resource(federatedGVR),
		}
		handles.worker = worker.NewReconcileWorker(
			func(qualifiedName common.QualifiedName) worker.Result {
				return reconcile(handles, qualifiedName)
			},
			worker.WorkerTiming{},
			workerCount,
			c.metrics,
			delayingdeliver.NewMetricTags("follower-controller-worker", handles.name),
		)
		handles.informer.Informer().AddEventHandlerWithResyncPeriod(
			util.NewTriggerOnAllChanges(handles.worker.EnqueueObject),
			util.NoResyncPeriod,
		)

		return handles
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
		targetType := ftc.Spec.TargetType
		federatedType := ftc.Spec.FederatedType
		targetGK := schema.GroupKind{Group: targetType.Group, Kind: targetType.Kind}
		federatedGK := schema.GroupKind{Group: federatedType.Group, Kind: federatedType.Kind}

		if _, exists := leaderPodTemplatePaths[targetGK]; exists {
			handles := getHandles(ftc, "leader", c.reconcileLeader)
			c.sourceToFederatedGKMap[targetGK] = federatedGK
			c.leaderTypeHandles[federatedGK] = handles
			c.logger.V(2).Info(fmt.Sprintf("Found supported leader FederatedTypeConfig %s", ftc.Name))
		} else if supportedFollowerTypes.Has(targetGK) {
			handles := getHandles(ftc, "follower", c.reconcileFollower)
			c.sourceToFederatedGKMap[targetGK] = federatedGK
			c.followerTypeHandles[federatedGK] = handles
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

	for _, handle := range c.leaderTypeHandles {
		handle.worker.Run(stopChan)
	}
	for _, handle := range c.followerTypeHandles {
		handle.worker.Run(stopChan)
	}

	<-stopChan
}

func (c *Controller) HasSynced() bool {
	for _, handle := range c.leaderTypeHandles {
		if !handle.informer.Informer().HasSynced() {
			return false
		}
	}
	for _, handle := range c.followerTypeHandles {
		if !handle.informer.Informer().HasSynced() {
			return false
		}
	}
	return true
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

	leader := fedtypesv1a1.LeaderReference{
		Group:     handles.federatedGK.Group,
		Kind:      handles.federatedGK.Kind,
		Namespace: qualifiedName.Namespace,
		Name:      qualifiedName.Name,
	}

	fedObj, err := getObjectFromStore(handles.informer.Informer().GetStore(), key)
	if err != nil {
		logger.Error(err, "Failed to retrieve object from store")
		return worker.StatusError
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
		handles, exists := c.followerTypeHandles[follower.GroupKind]
		if !exists {
			logger.WithValues("follower", follower).Error(nil, "Unsupported follower type")
			return worker.StatusError
		}
		handles.worker.Enqueue(
			common.QualifiedName{Namespace: follower.Namespace, Name: follower.Name},
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
			_, err = handles.client.Namespace(fedObj.GetNamespace()).
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
	fedObj *unstructured.Unstructured,
) (sets.Set[FollowerReference], error) {
	if fedObj.GetAnnotations()[common.EnableFollowerSchedulingAnnotation] != common.AnnotationValueTrue {
		// follower scheduling is not enabled
		return nil, nil
	}

	followersFromAnnotation, err := getFollowersFromAnnotation(fedObj, c.sourceToFederatedGKMap)
	if err != nil {
		return nil, err
	}

	followersFromPodTemplate, err := getFollowersFromPodTemplate(
		fedObj,
		leaderPodTemplatePaths[handles.sourceGK],
		c.sourceToFederatedGKMap,
	)
	if err != nil {
		return nil, err
	}

	return followersFromAnnotation.Union(followersFromPodTemplate), err
}

func (c *Controller) updateFollower(
	ctx context.Context,
	handles *typeHandles,
	followerUns *unstructured.Unstructured,
	followerObj *fedtypesv1a1.GenericFederatedFollower,
	leadersChanged bool,
	leaders []fedtypesv1a1.LeaderReference,
) (updated bool, err error) {
	logger := klog.FromContext(ctx)

	if leadersChanged {
		if err := fedtypesv1a1.SetFollows(followerUns, leaders); err != nil {
			return false, fmt.Errorf("set leaders on follower: %w", err)
		}
	}

	clusters, err := c.leaderPlacementUnion(leaders)
	if err != nil {
		return false, fmt.Errorf("get leader placement union: %w", err)
	}

	placementsChanged := followerObj.Spec.SetPlacementNames(PrefixedControllerName, clusters)
	if placementsChanged {
		err = util.SetGenericPlacements(followerUns, followerObj.Spec.Placements)
		if err != nil {
			return false, fmt.Errorf("set placements: %w", err)
		}
	}

	needsUpdate := leadersChanged || placementsChanged
	if needsUpdate {
		logger.V(1).Info("Updating follower to sync with leaders")
		_, err = handles.client.Namespace(followerUns.GetNamespace()).
			Update(context.TODO(), followerUns, metav1.UpdateOptions{})
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
		GroupKind: handles.federatedGK,
		Namespace: qualifiedName.Namespace,
		Name:      qualifiedName.Name,
	}

	followerUns, err := getObjectFromStore(
		handles.informer.Informer().GetStore(),
		qualifiedName.String(),
	)
	if err != nil {
		logger.Error(err, "Failed to get follower object from store")
		return worker.StatusError
	}

	if followerUns == nil {
		// The deleted follower no longer references any leaders
		c.cacheObservedFromFollowers.update(follower, nil)
		return worker.StatusAllOK
	}

	followerObj := &fedtypesv1a1.GenericFederatedFollower{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(followerUns.Object, followerObj)
	if err != nil {
		logger.Error(err, "Failed to unmarshall follower object from unstructured")
		return worker.StatusAllOK // retrying won't help
	}

	currentLeaders := sets.New(followerObj.Spec.Follows...)
	c.cacheObservedFromFollowers.update(follower, currentLeaders)

	desiredLeaders := c.cacheObservedFromLeaders.reverseLookup(follower)

	leadersChanged := !equality.Semantic.DeepEqual(desiredLeaders, currentLeaders)
	updated, err := c.updateFollower(
		ctx,
		handles,
		followerUns,
		followerObj,
		leadersChanged,
		desiredLeaders.UnsortedList(),
	)
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		logger.Error(err, "Failed to update follower")
		c.eventRecorder.Eventf(
			followerUns,
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
	leader fedtypesv1a1.LeaderReference,
) (*typeHandles, *fedtypesv1a1.GenericObjectWithPlacements, error) {
	leaderGK := leader.GroupKind()
	handles, exists := c.leaderTypeHandles[leaderGK]
	if !exists {
		return nil, nil, fmt.Errorf("unsupported leader type %v", leaderGK)
	}
	leaderQualifiedName := common.QualifiedName{Namespace: leader.Namespace, Name: leader.Name}
	leaderUns, err := getObjectFromStore(
		handles.informer.Informer().GetStore(),
		leaderQualifiedName.String(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("get from store: %w", err)
	}
	if leaderUns == nil {
		return nil, nil, nil
	}

	leaderObj, err := util.UnmarshalGenericPlacements(leaderUns)
	if err != nil {
		return nil, nil, fmt.Errorf("unmarshal to generic object with placements: %w", err)
	}

	return handles, leaderObj, nil
}

func (c *Controller) leaderPlacementUnion(
	leaders []fedtypesv1a1.LeaderReference,
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
		for cluster := range leaderObjWithPlacement.ClusterNameUnion() {
			clusters[cluster] = struct{}{}
		}
	}

	return clusters, nil
}

func getObjectFromStore(store cache.Store, key string) (*unstructured.Unstructured, error) {
	obj, exists, err := store.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	return obj.(*unstructured.Unstructured).DeepCopy(), nil
}
