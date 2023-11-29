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

package statusaggregator

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicclient "k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/statusaggregator/plugins"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/statusaggregator/plugins/sourcefeedback"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/propagationstatus"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	StatusAggregatorControllerName = "status-aggregator-controller"

	EventReasonUpdateSourceObjectStatus = "UpdateSourceObjectStatus"
)

// StatusAggregator aggregates statuses of target objects in member clusters to status of source object
type StatusAggregator struct {
	name string

	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterQueue            workqueue.DelayingInterface
	clusterAvailableDelay   time.Duration
	clusterUnavailableDelay time.Duration
	objectEnqueueDelay      time.Duration

	worker        worker.ReconcileWorker[reconcileKey]
	eventRecorder record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger

	dynamicClient dynamicclient.Interface
	fedClient     fedclient.Interface

	federatedInformer        informermanager.FederatedInformerManager
	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer
	informerManager          informermanager.InformerManager
}

type reconcileKey struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

func (r reconcileKey) NamespacedName() string {
	if r.namespace == "" {
		return r.name
	}
	return fmt.Sprintf("%s/%s", r.namespace, r.name)
}

func (r reconcileKey) String() string {
	return fmt.Sprintf(`{"gvk": %q, "namespace": %q, "name": %q}`, r.gvk.String(), r.namespace, r.name)
}

func NewStatusAggregatorController(
	kubeClient kubeclient.Interface,
	dynamicClient dynamicclient.Interface,
	fedClient fedclient.Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	federatedInformer informermanager.FederatedInformerManager,
	informerManager informermanager.InformerManager,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
	clusterAvailableDelay, clusterUnavailableDelay time.Duration,
) (*StatusAggregator, error) {
	a := &StatusAggregator{
		name: StatusAggregatorControllerName,

		// Build queue for triggering cluster reconciliations.
		clusterQueue:            workqueue.NewNamedDelayingQueue(StatusAggregatorControllerName),
		clusterAvailableDelay:   clusterAvailableDelay,
		clusterUnavailableDelay: clusterUnavailableDelay,
		objectEnqueueDelay:      3 * time.Second,

		eventRecorder: eventsink.NewDefederatingRecorderMux(kubeClient, StatusAggregatorControllerName, 4),
		metrics:       metrics,
		logger:        logger.WithValues("controller", StatusAggregatorControllerName),

		dynamicClient: dynamicClient,
		fedClient:     fedClient,

		fedObjectInformer:        fedObjectInformer,
		clusterFedObjectInformer: clusterFedObjectInformer,
		federatedInformer:        federatedInformer,
		informerManager:          informerManager,
	}

	a.worker = worker.NewReconcileWorker[reconcileKey](
		StatusAggregatorControllerName,
		a.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	genericFederatedObjectHandler := eventhandlers.NewTriggerOnAllChanges(func(fedObj fedcorev1a1.GenericFederatedObject) {
		if !fedObj.GetDeletionTimestamp().IsZero() {
			return
		}
		unsObj, err := fedObj.GetSpec().GetTemplateAsUnstructured()
		if err != nil {
			a.logger.Error(err, "Failed to get object metadata")
			return
		}
		gvk := unsObj.GroupVersionKind()
		if a.getFTCIfStatusAggregationIsEnabled(gvk) == nil {
			a.logger.V(3).Info("Status aggregation is disabled", "gvk", gvk)
			return
		}

		a.worker.EnqueueWithDelay(reconcileKey{
			gvk:       gvk,
			namespace: unsObj.GetNamespace(),
			name:      unsObj.GetName(),
		}, a.objectEnqueueDelay)
	})
	if _, err := a.fedObjectInformer.Informer().AddEventHandler(genericFederatedObjectHandler); err != nil {
		return nil, fmt.Errorf("failed to create federated informer: %w", err)
	}
	if _, err := a.clusterFedObjectInformer.Informer().AddEventHandler(genericFederatedObjectHandler); err != nil {
		return nil, fmt.Errorf("failed to create cluster federated informer: %w", err)
	}

	if err := a.federatedInformer.AddClusterEventHandlers(&informermanager.ClusterEventHandler{
		Predicate: func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
			if newCluster != nil && clusterutil.IsClusterReady(&newCluster.Status) &&
				(oldCluster == nil || !clusterutil.IsClusterReady(&oldCluster.Status)) {
				// When new cluster becomes available process all the target resources again.
				a.clusterQueue.AddAfter(struct{}{}, a.clusterAvailableDelay)
			}
			if oldCluster != nil && !oldCluster.DeletionTimestamp.IsZero() {
				// When a cluster becomes unavailable process all the target resources again.
				a.clusterQueue.AddAfter(struct{}{}, a.clusterUnavailableDelay)
			}
			return false
		},
		Callback: func(_ *fedcorev1a1.FederatedCluster) {},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler generator for cluster: %w", err)
	}

	if err := a.federatedInformer.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: informermanager.RegisterOncePredicate,
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			return eventhandlers.NewTriggerOnAllChanges(func(obj *unstructured.Unstructured) {
				if !obj.GetDeletionTimestamp().IsZero() {
					return
				}
				gvk := obj.GroupVersionKind()
				if a.getFTCIfStatusAggregationIsEnabled(gvk) == nil {
					a.logger.V(3).Info("Status aggregation is disabled", "gvk", gvk)
					return
				}
				a.worker.EnqueueWithDelay(reconcileKey{
					gvk:       gvk,
					namespace: obj.GetNamespace(),
					name:      obj.GetName(),
				}, a.objectEnqueueDelay)
			})
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler generator for cluster obj: %w", err)
	}

	if err := a.informerManager.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: informermanager.RegisterOncePredicate,
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			return eventhandlers.NewTriggerOnAllChanges(func(obj *unstructured.Unstructured) {
				if !obj.GetDeletionTimestamp().IsZero() || !ftc.GetStatusAggregationEnabled() {
					return
				}
				if anno := obj.GetAnnotations(); anno != nil {
					if noFederatedResource, ok := anno[federate.NoFederatedResource]; ok {
						if len(noFederatedResource) > 0 {
							logger.V(3).Info("No-federated-resource annotation found, skip status aggregation")
							return
						}
					}
				}
				a.worker.Enqueue(reconcileKey{
					gvk:       ftc.GetSourceTypeGVK(),
					namespace: obj.GetNamespace(),
					name:      obj.GetName(),
				})
			})
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler generator for obj: %w", err)
	}
	return a, nil
}

func (a *StatusAggregator) IsControllerReady() bool {
	return a.HasSynced()
}

func (a *StatusAggregator) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, a.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	go func() {
		for {
			item, shutdown := a.clusterQueue.Get()
			if shutdown {
				break
			}
			a.reconcileOnClusterChange()
			a.clusterQueue.Done(item)
		}
	}()
	if !cache.WaitForNamedCacheSync(StatusAggregatorControllerName, ctx.Done(), a.HasSynced) {
		logger.Error(nil, "Timed out waiting for cache sync")
		return
	}
	logger.Info("Caches are synced")

	a.worker.Run(ctx)
	<-ctx.Done()
	a.clusterQueue.ShutDown()
}

func (a *StatusAggregator) HasSynced() bool {
	return a.fedObjectInformer.Informer().HasSynced() &&
		a.clusterFedObjectInformer.Informer().HasSynced() &&
		a.federatedInformer.HasSynced() &&
		a.informerManager.HasSynced()
}

func (a *StatusAggregator) reconcile(ctx context.Context, key reconcileKey) (status worker.Result) {
	logger := a.logger.WithValues("object", key.NamespacedName(), "gvk", key.gvk)
	ctx = klog.NewContext(ctx, logger)

	ftc := a.getFTCIfStatusAggregationIsEnabled(key.gvk)
	if ftc == nil {
		logger.V(3).Info("StatusAggregation not enabled")
		return worker.StatusAllOK
	}

	a.metrics.Counter(metrics.StatusAggregatorThroughput, 1)
	logger.V(3).Info("Starting to reconcile")
	startTime := time.Now()
	defer func() {
		a.metrics.Duration(metrics.StatusAggregatorLatency, startTime)
		logger.V(3).WithValues("duration", time.Since(startTime), "status", status.String()).
			Info("Finished reconciling")
	}()

	sourceObject, err := a.getObjectFromStore(key, "")
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get source object from cache")
		return worker.StatusError
	}
	if sourceObject == nil || sourceObject.GetDeletionTimestamp() != nil {
		logger.V(3).Info("No source type found")
		return worker.StatusAllOK
	}
	sourceObject = sourceObject.DeepCopy()

	federatedName := naming.GenerateFederatedObjectName(key.name, ftc.Name)
	logger = logger.WithValues("federated-object", federatedName)
	ctx = klog.NewContext(ctx, logger)

	fedObject, err := fedobjectadapters.GetFromLister(
		a.fedObjectInformer.Lister(),
		a.clusterFedObjectInformer.Lister(),
		key.namespace,
		federatedName,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get object from store")
		return worker.StatusError
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		logger.V(3).Info("No federated type found")
		return worker.StatusAllOK
	}

	clusterObjs, err := a.clusterObjs(ctx, key)
	if err != nil {
		logger.Error(err, "Failed to get cluster objs")
		return worker.StatusError
	}

	clusterObjsUpToDate, err := propagationstatus.IsResourcePropagated(sourceObject, fedObject)
	if err != nil {
		logger.Error(err, "Failed to check if resource is propagated")
		return worker.StatusError
	}

	needUpdate := sourcefeedback.PopulateAnnotations(sourceObject, fedObject)
	if needUpdate {
		logger.V(1).Info("Updating metadata of source object")
		_, err = a.dynamicClient.Resource(ftc.GetSourceTypeGVR()).Namespace(key.namespace).
			Update(ctx, sourceObject, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}
	}

	needUpdate = false
	plugin := plugins.GetPlugin(key.gvk)
	if plugin != nil {
		newObj, newNeedUpdate, err := plugin.AggregateStatuses(ctx, sourceObject, fedObject, clusterObjs, clusterObjsUpToDate)
		if err != nil {
			return worker.StatusError
		}
		sourceObject = newObj
		needUpdate = newNeedUpdate
	}

	if needUpdate {
		latestSourceObj, err := a.dynamicClient.Resource(ftc.GetSourceTypeGVR()).Namespace(key.namespace).
			Get(ctx, key.name, metav1.GetOptions{})
		if err != nil {
			return worker.StatusError
		}
		statusMap, exists, err := unstructured.NestedMap(sourceObject.Object, "status")
		if err != nil || !exists {
			return worker.StatusError
		}
		err = unstructured.SetNestedMap(latestSourceObj.Object, statusMap, "status")
		if err != nil {
			return worker.StatusError
		}
		_, err = a.dynamicClient.Resource(ftc.GetSourceTypeGVR()).Namespace(key.namespace).
			UpdateStatus(ctx, latestSourceObj, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			a.eventRecorder.Eventf(sourceObject, corev1.EventTypeWarning, EventReasonUpdateSourceObjectStatus,
				"failed to update source object status %v, err: %v, retry later", key, err)
			return worker.StatusError
		}
		a.eventRecorder.Eventf(sourceObject, corev1.EventTypeNormal, EventReasonUpdateSourceObjectStatus,
			"updated source object status %v", key)
	}

	return worker.StatusAllOK
}

// clusterStatuses returns the resource status in member cluster.
func (a *StatusAggregator) clusterObjs(ctx context.Context, key reconcileKey) (map[string]interface{}, error) {
	logger := klog.FromContext(ctx)

	clusters, err := a.federatedInformer.GetReadyClusters()
	if err != nil {
		logger.Error(err, "Failed to list clusters")
		return nil, err
	}

	objs := make(map[string]interface{})
	for _, cluster := range clusters {
		clusterObj, err := a.getObjectFromStore(key, cluster.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			logger.Error(err, "Failed to get object from cluster", "cluster", cluster.Name)
			return nil, fmt.Errorf("failed to get object from cluster: %w", err)
		}
		objs[cluster.Name] = clusterObj
	}

	return objs, nil
}

// The function triggers reconciliation of all target federated resources.
func (a *StatusAggregator) reconcileOnClusterChange() {
	ftcs := a.listFTCWithStatusAggregationEnabled()
	for _, ftc := range ftcs {
		gvk := ftc.GetSourceTypeGVK()
		logger := a.logger.WithValues("gvk", gvk)
		lister, hasSynced, exists := a.informerManager.GetResourceLister(gvk)
		if !exists {
			logger.Error(nil, "Lister does not exist")
			return
		}
		if !hasSynced() {
			logger.V(3).Info("Lister not synced, retry it later")
			a.clusterQueue.AddAfter(struct{}{}, a.clusterAvailableDelay)
			return
		}
		sources, err := lister.List(labels.Everything())
		if err != nil {
			logger.Error(err, "Failed to list source objects, retry it later")
			a.clusterQueue.AddAfter(struct{}{}, a.clusterAvailableDelay)
			return
		}
		for _, item := range sources {
			unsObj, ok := item.(*unstructured.Unstructured)
			if !ok {
				logger.V(3).Info("Expected unstructured, but got non-type", "obj", item)
				return
			}
			a.worker.EnqueueWithDelay(reconcileKey{
				gvk:       gvk,
				namespace: unsObj.GetNamespace(),
				name:      unsObj.GetName(),
			}, a.objectEnqueueDelay)
		}
	}
}

// getObjectFromStore returns the specified obj from cluster.
// If cluster is "", get the obj from informerManager.
func (a *StatusAggregator) getObjectFromStore(qualifedName reconcileKey, cluster string) (*unstructured.Unstructured, error) {
	var (
		lister    cache.GenericLister
		hasSynced cache.InformerSynced
		exists    bool
	)
	if cluster != "" {
		lister, hasSynced, exists = a.federatedInformer.GetResourceLister(qualifedName.gvk, cluster)
	} else {
		lister, hasSynced, exists = a.informerManager.GetResourceLister(qualifedName.gvk)
	}
	if !exists {
		return nil, fmt.Errorf("lister for %s does not exist", qualifedName.gvk)
	}
	if !hasSynced() {
		return nil, fmt.Errorf("lister for %s not synced", qualifedName.gvk)
	}

	var obj pkgruntime.Object
	var err error
	if qualifedName.namespace == "" {
		obj, err = lister.Get(qualifedName.name)
	} else {
		obj, err = lister.ByNamespace(qualifedName.namespace).Get(qualifedName.name)
	}
	if err != nil {
		return nil, err
	}

	return obj.(*unstructured.Unstructured), nil
}

func (a *StatusAggregator) getFTCIfStatusAggregationIsEnabled(gvk schema.GroupVersionKind) *fedcorev1a1.FederatedTypeConfig {
	typeConfig, exists := a.informerManager.GetResourceFTC(gvk)
	if !exists || typeConfig == nil || !typeConfig.GetStatusAggregationEnabled() {
		return nil
	}

	return typeConfig.DeepCopy()
}

func (a *StatusAggregator) listFTCWithStatusAggregationEnabled() []*fedcorev1a1.FederatedTypeConfig {
	ftcLister := a.federatedInformer.GetFederatedTypeConfigLister()
	ftcList, err := ftcLister.List(labels.Everything())
	if err != nil {
		return nil
	}

	res := make([]*fedcorev1a1.FederatedTypeConfig, 0, len(ftcList))
	for _, ftc := range ftcList {
		if ftc.GetStatusAggregationEnabled() {
			res = append(res, ftc.DeepCopy())
		}
	}
	return res
}
