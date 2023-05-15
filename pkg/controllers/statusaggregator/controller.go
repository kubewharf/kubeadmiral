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
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/statusaggregator/plugins"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	allClustersKey = "ALL_CLUSTERS"

	EventReasonUpdateSourceObjectStatus     = "UpdateSourceObjectStatus"
	EventReasonUpdateSourceObjectAnnotation = "UpdateSourceObjectAnnotation"
)

// StatusAggregator aggregates statuses of target objects in member clusters to status of source object
type StatusAggregator struct {
	// name of the controller: <sourceKind>-status-aggregator
	name string

	// Store for the federated type
	federatedStore cache.Store
	// Controller for the federated type
	federatedController cache.Controller
	// Client for federated type
	federatedClient util.ResourceClient

	// Store for the source type
	sourceStore cache.Store
	// Controller for the source type
	sourceController cache.Controller
	// Client for source type
	sourceClient util.ResourceClient

	// Informer for resources in member clusters
	informer util.FederatedInformer
	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterDeliverer        *delayingdeliver.DelayingDeliverer
	clusterAvailableDelay   time.Duration
	clusterUnavailableDelay time.Duration
	objectEnqueueDelay      time.Duration

	worker        worker.ReconcileWorker
	typeConfig    *fedcorev1a1.FederatedTypeConfig
	eventRecorder record.EventRecorder

	// plugin for source type to aggregate statuses
	plugin  plugins.Plugin
	metrics stats.Metrics
}

func StartStatusAggregator(controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{}, typeConfig *fedcorev1a1.FederatedTypeConfig,
) error {
	aggregator, err := newStatusAggregator(controllerConfig, typeConfig)
	if err != nil {
		return err
	}
	klog.V(4).Infof("Starting status aggregator for %q", typeConfig.GetSourceType().Kind)
	aggregator.Run(stopChan)
	return nil
}

func newStatusAggregator(controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) (*StatusAggregator, error) {
	federatedAPIResource := typeConfig.GetFederatedType()
	targetAPIResource := typeConfig.GetTargetType()
	sourceAPIResource := typeConfig.GetSourceType()
	if sourceAPIResource == nil {
		return nil, errors.Errorf("Object federation is not supported for %q", federatedAPIResource.Kind)
	}
	plugin := plugins.GetPlugin(sourceAPIResource)
	if plugin == nil {
		return nil, errors.Errorf("statuses aggregation plugin is not found for %q", sourceAPIResource.Kind)
	}

	userAgent := fmt.Sprintf("%s-status-aggregator", strings.ToLower(sourceAPIResource.Kind))
	configWithUserAgent := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configWithUserAgent, userAgent)

	kubeClient := kubeclient.NewForConfigOrDie(configWithUserAgent)
	recorder := eventsink.NewDefederatingRecorderMux(kubeClient, userAgent, 4)

	a := &StatusAggregator{
		name:          userAgent,
		eventRecorder: recorder,
		typeConfig:    typeConfig,
		plugin:        plugin,
		metrics:       controllerConfig.Metrics,
	}
	var err error
	a.federatedClient, err = util.NewResourceClient(configWithUserAgent, &federatedAPIResource)
	if err != nil {
		return nil, err
	}
	a.sourceClient, err = util.NewResourceClient(configWithUserAgent, sourceAPIResource)
	if err != nil {
		return nil, err
	}

	// Build deliverer for triggering cluster reconciliations.
	a.clusterDeliverer = delayingdeliver.NewDelayingDeliverer()
	a.clusterAvailableDelay = controllerConfig.ClusterAvailableDelay
	a.clusterUnavailableDelay = controllerConfig.ClusterUnavailableDelay
	a.objectEnqueueDelay = 10 * time.Second

	a.worker = worker.NewReconcileWorker(
		a.reconcile,
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags("statusaggregator-worker", typeConfig.GetTargetType().Kind),
	)
	enqueueObj := a.worker.EnqueueObject
	targetNamespace := controllerConfig.TargetNamespace
	a.federatedStore, a.federatedController = util.NewResourceInformer(
		a.federatedClient,
		targetNamespace,
		enqueueObj,
		controllerConfig.Metrics,
	)
	a.sourceStore, a.sourceController = util.NewResourceInformer(
		a.sourceClient,
		targetNamespace,
		enqueueObj,
		controllerConfig.Metrics,
	)
	a.informer, err = util.NewFederatedInformer(
		controllerConfig,
		genericclient.NewForConfigOrDie(configWithUserAgent),
		configWithUserAgent,
		&targetAPIResource,
		func(obj pkgruntime.Object) {
			qualifiedName := common.NewQualifiedName(obj)
			a.worker.EnqueueWithDelay(qualifiedName, a.objectEnqueueDelay)
		},
		&util.ClusterLifecycleHandlerFuncs{
			ClusterAvailable: func(cluster *fedcorev1a1.FederatedCluster) {
				// When new cluster becomes available process all the target resources again.
				a.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(a.clusterAvailableDelay))
			},
			// When a cluster becomes unavailable process all the target resources again.
			ClusterUnavailable: func(cluster *fedcorev1a1.FederatedCluster, _ []interface{}) {
				a.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(a.clusterUnavailableDelay))
			},
		},
	)
	if err != nil {
		return nil, err
	}
	klog.Infof("Status aggregator %q NewResourceInformer", federatedAPIResource.Kind)
	return a, nil
}

func (a *StatusAggregator) Run(stopChan <-chan struct{}) {
	go a.sourceController.Run(stopChan)
	go a.federatedController.Run(stopChan)
	a.informer.Start()
	a.clusterDeliverer.StartWithHandler(func(_ *delayingdeliver.DelayingDelivererItem) {
		a.reconcileOnClusterChange()
	})
	go a.clusterDeliverer.RunMetricLoop(stopChan, 30*time.Second, a.metrics,
		delayingdeliver.NewMetricTags("schedulingpreference-clusterDeliverer", a.typeConfig.GetTargetType().Kind))
	if !cache.WaitForNamedCacheSync(a.name, stopChan, a.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync for controller: %s", a.name))
	}
	a.worker.Run(stopChan)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		defer func() {
			if r := recover(); r != nil {
				klog.Warningf("recovered from panic: %v", r)
			}
		}()
		<-stopChan
		a.informer.Stop()
		a.clusterDeliverer.Stop()
	}()
}

func (a *StatusAggregator) HasSynced() bool {
	if !a.informer.ClustersSynced() {
		klog.V(2).Infof("Cluster list not synced")
		return false
	}
	if !a.federatedController.HasSynced() {
		klog.V(2).Infof("Federated type not synced")
		return false
	}
	if !a.sourceController.HasSynced() {
		klog.V(2).Infof("Status not synced")
		return false
	}

	clusters, err := a.informer.GetReadyClusters()
	if err != nil {
		utilruntime.HandleError(errors.Wrap(err, "Failed to get ready clusters"))
		return false
	}
	if !a.informer.GetTargetStore().ClustersSynced(clusters) {
		return false
	}
	return true
}

func (a *StatusAggregator) reconcile(qualifiedName common.QualifiedName) worker.Result {
	sourceKind := a.typeConfig.GetSourceType().Kind
	key := qualifiedName.String()

	a.metrics.Rate("status-aggregator.throughput", 1)
	klog.V(4).Infof("status aggregator for %v starting to reconcile %v", sourceKind, key)
	startTime := time.Now()
	defer func() {
		a.metrics.Duration("status-aggregator.latency", startTime)
		klog.V(4).
			Infof("status aggregator for %v finished reconciling %v (duration: %v)", sourceKind, key, time.Since(startTime))
	}()

	sourceObject, err := objectFromCache(a.sourceStore, key)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}

	if sourceObject == nil || sourceObject.GetDeletionTimestamp() != nil {
		klog.V(4).Infof("No source type for %v %v found", sourceKind, key)
		return worker.StatusAllOK
	}

	fedObject, err := objectFromCache(a.federatedStore, key)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		klog.V(4).Infof("No federated type for %v %v found", sourceKind, key)
		return worker.StatusAllOK
	}

	clusterObjs, err := a.clusterObjs(key)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}

	newObj, needUpdate, err := a.plugin.AggregateStatues(sourceObject.DeepCopy(), fedObject, clusterObjs)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}

	canReuseUpdateStatus := sourceObject.GroupVersionKind() == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind)

	if canReuseUpdateStatus {
		sourcefeedback.PopulateStatusAnnotation(newObj, clusterObjs, &needUpdate)
	}

	if needUpdate {
		klog.V(4).Infof("about to update status of source object %v: %v", sourceKind, key)
		_, err = a.sourceClient.Resources(qualifiedName.Namespace).
			UpdateStatus(context.TODO(), newObj, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			a.eventRecorder.Eventf(sourceObject, corev1.EventTypeWarning, EventReasonUpdateSourceObjectStatus,
				"failed to update source object status %v %v, err: %v, retry later", sourceKind, key, err)
			return worker.StatusError
		}
		a.eventRecorder.Eventf(sourceObject, corev1.EventTypeNormal, EventReasonUpdateSourceObjectStatus,
			"updated source object status %v %v", sourceKind, key)
	}

	if !canReuseUpdateStatus {
		needUpdate = false
		sourcefeedback.PopulateStatusAnnotation(newObj, clusterObjs, &needUpdate)

		if needUpdate {
			klog.V(4).Infof("about to update annotation of source object %v: %v", sourceKind, key)
			_, err = a.sourceClient.Resources(qualifiedName.Namespace).
				Update(context.TODO(), newObj, metav1.UpdateOptions{})
			if err != nil {
				if apierrors.IsConflict(err) {
					return worker.StatusConflict
				}
				a.eventRecorder.Eventf(sourceObject, corev1.EventTypeWarning, EventReasonUpdateSourceObjectAnnotation,
					"failed to update source object annotation %v %v, err: %v, retry later", sourceKind, key, err)
				return worker.StatusError
			}
			a.eventRecorder.Eventf(sourceObject, corev1.EventTypeNormal, EventReasonUpdateSourceObjectAnnotation,
				"updated source object annotation %v %v", sourceKind, key)
		}
	}

	return worker.StatusAllOK
}

// clusterStatuses returns the resource status in member cluster.
func (a *StatusAggregator) clusterObjs(key string) (map[string]interface{}, error) {
	clusters, err := a.informer.GetReadyClusters()
	if err != nil {
		return nil, err
	}

	objs := make(map[string]interface{})
	targetKind := a.typeConfig.GetTargetType().Kind
	for _, cluster := range clusters {
		clusterObj, exist, err := a.informer.GetTargetStore().GetByKey(cluster.Name, key)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "Failed to get %s %q from cluster %q", targetKind, key, cluster.Name)
			utilruntime.HandleError(wrappedErr)
			return nil, wrappedErr
		}
		if exist {
			objs[cluster.Name] = clusterObj
		}
	}

	return objs, nil
}

// The function triggers reconciliation of all target federated resources.
func (a *StatusAggregator) reconcileOnClusterChange() {
	if !a.HasSynced() {
		a.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(a.clusterAvailableDelay))
	}
	for _, obj := range a.sourceStore.List() {
		qualifiedName := common.NewQualifiedName(obj.(pkgruntime.Object))
		a.worker.EnqueueWithDelay(qualifiedName, time.Second*3)
	}
}

func objectFromCache(store cache.Store, key string) (*unstructured.Unstructured, error) {
	cachedObj, exist, err := store.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(*unstructured.Unstructured).DeepCopy(), nil
}
