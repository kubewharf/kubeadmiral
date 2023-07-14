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

package automigration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/automigration/plugins"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/util/unstructured"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	EventReasonAutoMigrationInfoUpdated = "AutoMigrationInfoUpdated"
	AutoMigrationControllerName         = "auto-migration"
)

/*
The current implementation would query the member apiserver for pods for every reconcile, which is not ideal.
An easy alternative is to cache all pods from all clusters, but this would significantly
reduce scalability based on past experience.

One way to prevent both is:
- Watch pods directly (would the cpu cost be ok?), but cache only unschedulable pods in a map from UID to pod labels
- When a pod is deleted, remove it from the map
- When the status of a target resource is updated, find its unschedulable pods and compute latest estimatedCapacity
*/

type Controller struct {
	name string

	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer
	federatedInformer        informermanager.FederatedInformerManager
	ftcManager               informermanager.FederatedTypeConfigManager

	fedClient fedclient.Interface

	worker worker.ReconcileWorker[common.QualifiedName]

	eventRecorder record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger
}

// IsControllerReady implements controllermanager.Controller
func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func NewAutoMigrationController(
	ctx context.Context,
	kubeClient kubeclient.Interface,
	fedClient fedclient.Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	federatedInformer informermanager.FederatedInformerManager,
	ftcManager informermanager.FederatedTypeConfigManager,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		name: AutoMigrationControllerName,

		fedObjectInformer:        fedObjectInformer,
		clusterFedObjectInformer: clusterFedObjectInformer,
		federatedInformer:        federatedInformer,
		ftcManager:               ftcManager,

		fedClient: fedClient,

		metrics:       metrics,
		logger:        logger.WithValues("controller", AutoMigrationControllerName),
		eventRecorder: eventsink.NewDefederatingRecorderMux(kubeClient, AutoMigrationControllerName, 6),
	}

	c.worker = worker.NewReconcileWorker[common.QualifiedName](
		AutoMigrationControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	federatedObjectHandler := cache.ResourceEventHandlerFuncs{
		// Only need to handle UnschedulableThreshold updates
		// Addition and deletion will be triggered by the target resources.
		UpdateFunc: func(oldFedObj, newFedObj interface{}) {
			oldObj, newObj := oldFedObj.(fedcorev1a1.GenericFederatedObject), newFedObj.(fedcorev1a1.GenericFederatedObject)
			oldThreshold := oldObj.GetAnnotations()[common.PodUnschedulableThresholdAnnotation]
			newThreshold := newObj.GetAnnotations()[common.PodUnschedulableThresholdAnnotation]
			if oldThreshold != newThreshold {
				c.worker.Enqueue(common.NewQualifiedName(newObj))
			}
		},
	}
	if _, err := c.fedObjectInformer.Informer().AddEventHandler(federatedObjectHandler); err != nil {
		return nil, fmt.Errorf("failed to create federated informer: %w", err)
	}
	if _, err := c.clusterFedObjectInformer.Informer().AddEventHandler(federatedObjectHandler); err != nil {
		return nil, fmt.Errorf("failed to create cluster federated informer: %w", err)
	}

	c.federatedInformer.AddPodEventHandler(&informermanager.ResourceEventHandlerWithClusterFuncs{
		UpdateFunc: func(oldObj, newObj interface{}, cluster string) {
			ctx := klog.NewContext(ctx, c.logger)
			ctx, logger := logging.InjectLoggerValues(ctx, "cluster", cluster)

			newPod := newObj.(*corev1.Pod)
			if newPod.GetDeletionTimestamp() != nil {
				return
			}
			oldPod := oldObj.(*corev1.Pod)
			if !podScheduledConditionChanged(oldPod, newPod) {
				return
			}

			qualifiedNames, err := c.getPossibleSourceObjectsFromCluster(ctx, newPod, cluster)
			if err != nil {
				logger.V(3).Info(
					"Failed to get possible source objects form pod",
					"pod", common.NewQualifiedName(newPod),
					"err", err,
				)
				return
			}
			for _, qualifiedName := range qualifiedNames {
				// enqueue with a delay to simulate a rudimentary rate limiter
				c.worker.EnqueueWithDelay(qualifiedName, 10*time.Second)
			}
		},
	})

	objectHandler := func(obj interface{}) {
		// work.enqueue
		targetObj := obj.(*unstructured.Unstructured)
		if !targetObj.GetDeletionTimestamp().IsZero() {
			return
		}
		gvk := targetObj.GroupVersionKind()
		ftc := c.getFTCIfAutoMigrationIsEnabled(gvk)
		if ftc == nil {
			c.logger.V(3).Info("Auto migration is disabled", "gvk", gvk)
			return
		}
		c.worker.EnqueueWithDelay(common.QualifiedName{
			Namespace: targetObj.GetNamespace(),
			Name:      naming.GenerateFederatedObjectName(targetObj.GetName(), ftc.Name),
		}, 10*time.Second)
	}
	if err := c.federatedInformer.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: informermanager.RegisterOncePredicate,
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			// EventHandler for target obj
			return cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					objectHandler(obj)
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
					objectHandler(newObj)
				},
				DeleteFunc: func(obj interface{}) {
					if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						obj = tombstone.Obj
						if obj == nil {
							return
						}
					}
					objectHandler(obj)
				},
			}
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to create target object informer: %w", err)
	}

	return c, nil
}

func (c *Controller) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(AutoMigrationControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for cache sync")
		return
	}

	logger.Info("Caches are synced")

	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *Controller) HasSynced() bool {
	if !c.fedObjectInformer.Informer().HasSynced() ||
		!c.clusterFedObjectInformer.Informer().HasSynced() ||
		!c.federatedInformer.HasSynced() ||
		!c.ftcManager.HasSynced() {
		return false
	}

	// We do not wait for the individual clusters' informers to sync to prevent a single faulty informer from blocking the
	// whole automigration controller. If a single cluster's informer is not synced and the cluster objects cannot be
	// retrieved, it will simply be ineligible for automigraiton.

	return true
}

func (c *Controller) reconcile(ctx context.Context, qualifiedName common.QualifiedName) (status worker.Result) {
	key := qualifiedName.String()
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "control-loop", "reconcile", "object", key)

	startTime := time.Now()
	c.metrics.Rate("auto-migration.throughput", 1)
	keyedLogger.V(3).Info("Start reconcile")
	defer func() {
		c.metrics.Duration(fmt.Sprintf("%s.latency", c.name), startTime)
		keyedLogger.V(3).Info("Finished reconcile", "duration", time.Since(startTime), "status", status)
	}()

	fedObject, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		qualifiedName.Namespace,
		qualifiedName.Name,
	)
	if err != nil {
		keyedLogger.Error(err, "Failed to get federated object from store")
		return worker.StatusError
	}
	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		return worker.StatusAllOK
	}
	fedObject = fedObject.DeepCopyGenericFederatedObject()

	annotations := fedObject.GetAnnotations()
	clusterObjs, ftc, unschedulableThreshold, err := c.getTargetObjectsIfAutoMigrationEnabled(fedObject)
	if err != nil {
		keyedLogger.Error(err, "Failed to get objects from federated informer stores")
		return worker.StatusError
	}
	// auto-migration controller sets AutoMigrationAnnotation to
	// feedback auto-migration information back to the scheduler

	var estimatedCapacity map[string]int64
	var result *worker.Result
	needsUpdate := false
	if unschedulableThreshold == nil {
		// Clean up the annotation if auto migration is disabled.
		keyedLogger.V(3).Info("Auto migration is disabled")
		_, exists := annotations[common.AutoMigrationInfoAnnotation]
		if exists {
			delete(annotations, common.AutoMigrationInfoAnnotation)
			needsUpdate = true
		}
	} else {
		// Keep the annotation up-to-date if auto migration is enabled.
		keyedLogger.V(3).Info("Auto migration is enabled")
		estimatedCapacity, result = c.estimateCapacity(ctx, ftc, clusterObjs, *unschedulableThreshold)
		autoMigrationInfo := &framework.AutoMigrationInfo{EstimatedCapacity: estimatedCapacity}

		// Compare with the existing autoMigration annotation
		existingAutoMigrationInfo := &framework.AutoMigrationInfo{EstimatedCapacity: nil}
		if existingAutoMigrationInfoBytes, exists := annotations[common.AutoMigrationInfoAnnotation]; exists {
			err := json.Unmarshal([]byte(existingAutoMigrationInfoBytes), existingAutoMigrationInfo)
			if err != nil {
				keyedLogger.Error(err, "Existing auto migration annotation is invalid, ignoring")
				// we treat invalid existing annotation as if it doesn't exist
			}
		}

		if !equality.Semantic.DeepEqual(existingAutoMigrationInfo, autoMigrationInfo) {
			autoMigrationInfoBytes, err := json.Marshal(autoMigrationInfo)
			if err != nil {
				keyedLogger.Error(err, "Failed to marshal auto migration")
				return worker.StatusAllOK
			}
			annotations[common.AutoMigrationInfoAnnotation] = string(autoMigrationInfoBytes)
			needsUpdate = true
		}
	}

	keyedLogger.V(3).Info("Observed migration information", "estimatedCapacity", estimatedCapacity)
	if needsUpdate {
		fedObject.SetAnnotations(annotations)

		keyedLogger.V(1).Info("Updating federated object with auto migration information", "estimatedCapacity", estimatedCapacity)
		if _, err = fedobjectadapters.Update(
			ctx,
			c.fedClient.CoreV1alpha1(),
			fedObject,
			metav1.UpdateOptions{},
		); err != nil {
			keyedLogger.Error(err, "Failed to update federated object for auto migration")
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}

		keyedLogger.V(1).Info("Updated federated object with auto migration information", "estimatedCapacity", estimatedCapacity)
		c.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeNormal,
			EventReasonAutoMigrationInfoUpdated,
			"Auto migration information updated: estimatedCapacity=%+v",
			estimatedCapacity,
		)
	}

	if result == nil {
		return worker.StatusAllOK
	} else {
		return *result
	}
}

func (c *Controller) estimateCapacity(
	ctx context.Context,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	clusterObjs []FederatedObject,
	unschedulableThreshold time.Duration,
) (map[string]int64, *worker.Result) {
	needsBackoff := false
	var retryAfter *time.Duration

	estimatedCapacity := make(map[string]int64, len(clusterObjs))

	for _, clusterObj := range clusterObjs {
		ctx, logger := logging.InjectLoggerValues(ctx, "cluster", clusterObj.ClusterName, "ftc", typeConfig.Name)

		// This is an optimization to skip pod listing when there are no unschedulable pods.
		totalReplicas, readyReplicas, err := c.getTotalAndReadyReplicas(typeConfig, clusterObj.Object)
		if err == nil && totalReplicas == readyReplicas {
			logger.V(3).Info("No unschedulable pods found, skip estimating capacity")
			continue
		}

		desiredReplicas, err := c.getDesiredReplicas(typeConfig, clusterObj.Object)
		if err != nil {
			logger.Error(err, "Failed to get desired replicas from object")
			continue
		}

		logger.V(2).Info("Getting pods from cluster")
		pods, clusterNeedsBackoff, err := c.getPodsFromCluster(ctx, typeConfig, clusterObj.Object, clusterObj.ClusterName)
		if err != nil {
			logger.Error(err, "Failed to get pods from cluster")
			if clusterNeedsBackoff {
				needsBackoff = true
			}
			continue
		}

		unschedulable, nextCrossIn := countUnschedulablePods(pods, time.Now(), unschedulableThreshold)
		logger.V(2).Info("Analyzed pods",
			"total", len(pods),
			"desired", desiredReplicas,
			"unschedulable", unschedulable,
		)

		if nextCrossIn != nil && (retryAfter == nil || *nextCrossIn < *retryAfter) {
			retryAfter = nextCrossIn
		}

		var clusterEstimatedCapacity int64
		if len(pods) >= int(desiredReplicas) {
			// When len(pods) >= desiredReplicas, we can immediately determine the capacity by taking the number of
			// schedulable pods.
			clusterEstimatedCapacity = int64(len(pods) - unschedulable)
		} else {
			// If len(pods) < desiredReplicas, we have uncreated pods. We must treat the uncreated pods as schedulable
			// to prevent them from being unnecessarily migrated before creation.
			clusterEstimatedCapacity = desiredReplicas - int64(unschedulable)
		}

		if clusterEstimatedCapacity >= desiredReplicas {
			// There is no need to migrate any pods. We skip writing estimatedCapacity information to avoid unnecessary
			// reconciliation by the scheduler.
			continue
		} else if clusterEstimatedCapacity < 0 {
			// estimatedCapacity should never be < 0. Nonetheless, this safeguard is required since the planner's
			// algorithm does not support negative estimatedCapacity.
			clusterEstimatedCapacity = 0
		}

		estimatedCapacity[clusterObj.ClusterName] = clusterEstimatedCapacity
	}

	var result *worker.Result
	if needsBackoff || retryAfter != nil {
		result = &worker.Result{
			Success:      true,
			RequeueAfter: retryAfter,
			Backoff:      needsBackoff,
		}
	}
	return estimatedCapacity, result
}

func (c *Controller) getTotalAndReadyReplicas(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	unsObj *unstructured.Unstructured,
) (int64, int64, error) {
	// These values might not have been populated by the controller, in which case we default to 0

	totalReplicas := int64(0)
	if replicasPtr, err := utilunstructured.GetInt64FromPath(
		unsObj, typeConfig.Spec.PathDefinition.ReplicasStatus, nil,
	); err != nil {
		return 0, 0, fmt.Errorf("replicas: %w", err)
	} else if replicasPtr != nil {
		totalReplicas = *replicasPtr
	}

	readyReplicas := int64(0)
	if readyReplicasPtr, err := utilunstructured.GetInt64FromPath(
		unsObj, typeConfig.Spec.PathDefinition.ReadyReplicasStatus, nil,
	); err != nil {
		return 0, 0, fmt.Errorf("ready replicas: %w", err)
	} else if readyReplicasPtr != nil {
		readyReplicas = *readyReplicasPtr
	}

	return totalReplicas, readyReplicas, nil
}

func (c *Controller) getDesiredReplicas(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	unsObj *unstructured.Unstructured,
) (int64, error) {
	desiredReplicas, err := utilunstructured.GetInt64FromPath(unsObj, typeConfig.Spec.PathDefinition.ReplicasSpec, nil)
	if err != nil {
		return 0, fmt.Errorf("desired replicas: %w", err)
	} else if desiredReplicas == nil {
		return 0, fmt.Errorf("no desired replicas at %s", typeConfig.Spec.PathDefinition.ReplicasSpec)
	}

	return *desiredReplicas, nil
}

func (c *Controller) getPodsFromCluster(
	ctx context.Context,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	unsClusterObj *unstructured.Unstructured,
	clusterName string,
) ([]*corev1.Pod, bool, error) {
	plugin, err := plugins.ResolvePlugin(typeConfig.GetSourceTypeGVK())
	if err != nil {
		return nil, false, fmt.Errorf("failed to get plugin for FTC: %w", err)
	}

	client, exist := c.federatedInformer.GetClusterDynamicClient(clusterName)
	if !exist {
		return nil, true, fmt.Errorf("failed to get client for cluster: %w", err)
	}

	pods, err := plugin.GetPodsForClusterObject(ctx, unsClusterObj, plugins.ClusterHandle{
		Client: client,
	})
	if err != nil {
		return nil, true, fmt.Errorf("failed to get pods for federated object: %w", err)
	}

	return pods, false, nil
}

func (c *Controller) getPossibleSourceObjectsFromCluster(
	ctx context.Context,
	pod *corev1.Pod,
	clusterName string,
) (possibleQualifies []common.QualifiedName, err error) {
	client, exist := c.federatedInformer.GetClusterDynamicClient(clusterName)
	if !exist {
		return nil, fmt.Errorf("failed to get client for cluster %s", clusterName)
	}

	for gvk, plugin := range plugins.NativePlugins {
		ctx, logger := logging.InjectLoggerValues(ctx, "gvk", gvk)
		ftc := c.getFTCIfAutoMigrationIsEnabled(gvk)
		if ftc == nil {
			continue
		}
		object, found, err := plugin.GetTargetObjectFromPod(ctx, pod.DeepCopy(), plugins.ClusterHandle{
			Client: client,
		})
		if err != nil || !found {
			logger.V(3).Info(
				"Failed to get target object form pod",
				"found", found,
				"err", err,
			)
			continue
		}
		managed := object.GetLabels()[managedlabel.ManagedByKubeAdmiralLabelKey] == managedlabel.ManagedByKubeAdmiralLabelValue
		gkMatched := object.GroupVersionKind().GroupKind() == gvk.GroupKind()
		if !managed || !gkMatched {
			c.logger.V(3).Info(
				"The GVK of Target object not matched",
				"got-gvk", object.GroupVersionKind(),
				"managed", managed,
				"resource", common.NewQualifiedName(object),
			)
			continue
		}
		possibleQualifies = append(possibleQualifies, common.QualifiedName{
			Namespace: object.GetNamespace(),
			Name:      naming.GenerateFederatedObjectName(object.GetName(), ftc.Name),
		})
	}
	return possibleQualifies, nil
}

func (c *Controller) getTargetObjectsIfAutoMigrationEnabled(
	fedObject fedcorev1a1.GenericFederatedObject,
) (clusterObjs []FederatedObject, ftc *fedcorev1a1.FederatedTypeConfig, unschedulableThreshold *time.Duration, err error) {
	// PodUnschedulableThresholdAnnotation is set by the scheduler. Its presence determines whether auto migration is enabled.
	if value, exists := fedObject.GetAnnotations()[common.PodUnschedulableThresholdAnnotation]; exists {
		if duration, err := time.ParseDuration(value); err != nil {
			err = fmt.Errorf("failed to parse PodUnschedulableThresholdAnnotation: %w", err)
			return nil, nil, nil, err
		} else {
			unschedulableThreshold = &duration
		}
	}

	objectMeta := &metav1.PartialObjectMetadata{}
	if err = json.Unmarshal(fedObject.GetSpec().Template.Raw, objectMeta); err != nil {
		err = fmt.Errorf("failed to unmarshall template of federated object: %w", err)
		return nil, nil, nil, err
	}
	gvk := objectMeta.GroupVersionKind()

	ftc = c.getFTCIfAutoMigrationIsEnabled(gvk)
	if ftc == nil {
		return nil, nil, nil, nil
	}

	for _, placement := range fedObject.GetSpec().Placements {
		for _, cluster := range placement.Placement {
			lister, synced, exist := c.federatedInformer.GetResourceLister(gvk, cluster.Cluster)
			if !exist || !synced() {
				err = fmt.Errorf("informer of resource %v not exists or not synced for cluster %s", gvk, cluster.Cluster)
				return nil, nil, nil, err
			}
			object, err := lister.ByNamespace(objectMeta.Namespace).Get(objectMeta.Name)
			if err != nil && !apierrors.IsNotFound(err) {
				err = fmt.Errorf("failed to get %v from informer stores for cluster %s: %w", objectMeta, cluster.Cluster, err)
				return nil, nil, nil, err
			}
			if apierrors.IsNotFound(err) {
				continue
			}
			unsObj, ok := object.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			clusterObjs = append(clusterObjs, FederatedObject{Object: unsObj, ClusterName: cluster.Cluster})
		}
	}
	return clusterObjs, ftc, unschedulableThreshold, nil
}

func (c *Controller) getFTCIfAutoMigrationIsEnabled(gvk schema.GroupVersionKind) *fedcorev1a1.FederatedTypeConfig {
	typeConfig, exists := c.ftcManager.GetResourceFTC(gvk)
	if !exists || typeConfig == nil || !typeConfig.GetAutoMigrationEnabled() {
		return nil
	}

	return typeConfig
}
