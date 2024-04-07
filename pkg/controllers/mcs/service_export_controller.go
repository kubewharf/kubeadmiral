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

package mcs

import (
	"context"
	"fmt"
	"reflect"

	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	discoveryv1informers "k8s.io/client-go/informers/discovery/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	discoveryv1listers "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	ServiceExportControllerName = "serviceexport-controller"
)

const (
	EndpointSliceSourceClusterLabel = common.DefaultPrefix + "source-cluster"
)

type reconcileKey struct {
	cluster string

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
	return fmt.Sprintf(`{"cluster": %q, "gvk": %q, "namespace": %q, "name": %q}`, r.cluster, r.gvk.String(), r.namespace, r.name)
}

type ServiceExportController struct {
	name string

	endpointSliceInformer discoveryv1informers.EndpointSliceInformer
	kubeClient            kubeclient.Interface
	fedInformerManager    informermanager.FederatedInformerManager

	worker worker.ReconcileWorker[reconcileKey]

	eventRecorder record.EventRecorder
	logger        klog.Logger
	metrics       stats.Metrics
}

func (c *ServiceExportController) IsControllerReady() bool {
	return c.HasSynced()
}

func (c *ServiceExportController) HasSynced() bool {
	return c.fedInformerManager.HasSynced() && c.endpointSliceInformer.Informer().HasSynced()
}

func NewServiceExportController(
	kubeClient kubeclient.Interface,
	endpointSliceInformer discoveryv1informers.EndpointSliceInformer,
	fedInformerManager informermanager.FederatedInformerManager,
	logger klog.Logger,
	metrics stats.Metrics,
	workerCount int,
) (*ServiceExportController, error) {
	c := &ServiceExportController{
		name:                  ServiceExportControllerName,
		kubeClient:            kubeClient,
		endpointSliceInformer: endpointSliceInformer,
		fedInformerManager:    fedInformerManager,
		eventRecorder:         eventsink.NewDefederatingRecorderMux(kubeClient, ServiceExportControllerName, 6),
		logger:                logger.WithValues("controller", ServiceExportControllerName),
		metrics:               metrics,
	}

	c.worker = worker.NewReconcileWorker[reconcileKey](
		ServiceExportControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	objectHandler := func(obj interface{}, cluster string) {
		reconcileKey, err := reconcileKeyFunc(cluster, obj)
		if err != nil {
			logger.Error(err, "Failed to generate key for obj")
			return
		}
		c.worker.Enqueue(reconcileKey)
	}

	c.fedInformerManager.AddResourceEventHandler(common.ServiceExportGVR, &informermanager.FilteringResourceEventHandlerWithClusterFuncs{
		FilterFunc: func(obj interface{}, cluster string) bool {
			switch t := obj.(type) {
			case *unstructured.Unstructured:
				return managedlabel.HasManagedLabel(t)
			default:
				return false
			}
		},
		Handler: informermanager.ResourceEventHandlerWithClusterFuncs{
			AddFunc: func(obj interface{}, cluster string) {
				objectHandler(obj, cluster)
			},
			DeleteFunc: func(obj interface{}, cluster string) {
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					obj = tombstone.Obj
					if obj == nil {
						return
					}
				}
				objectHandler(obj, cluster)
			},
		},
	})

	c.fedInformerManager.AddResourceEventHandler(common.EndpointSliceGVR, &informermanager.FilteringResourceEventHandlerWithClusterFuncs{
		FilterFunc: func(obj interface{}, cluster string) bool {
			switch t := obj.(type) {
			case *discoveryv1.EndpointSlice:
				if _, ok := t.Labels[discoveryv1.LabelServiceName]; !ok {
					return false
				}
				if _, ok := t.Labels[EndpointSliceSourceClusterLabel]; ok {
					return false
				}
				return true
			default:
				return false
			}
		},
		Handler: informermanager.ResourceEventHandlerWithClusterFuncs{
			AddFunc: func(obj interface{}, cluster string) {
				eps := obj.(*discoveryv1.EndpointSlice)
				c.worker.Enqueue(reconcileKey{
					cluster:   cluster,
					gvk:       common.EndpointSliceGVK,
					namespace: eps.Namespace,
					name:      eps.Name,
				})
			},
			UpdateFunc: func(oldObj, newObj interface{}, cluster string) {
				eps := newObj.(*discoveryv1.EndpointSlice)
				c.worker.Enqueue(reconcileKey{
					cluster:   cluster,
					gvk:       common.EndpointSliceGVK,
					namespace: eps.Namespace,
					name:      eps.Name,
				})
			},
			DeleteFunc: func(obj interface{}, cluster string) {
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					obj = tombstone.Obj
					if obj == nil {
						return
					}
				}
				eps := obj.(*discoveryv1.EndpointSlice)
				c.worker.Enqueue(reconcileKey{
					cluster:   cluster,
					gvk:       common.EndpointSliceGVK,
					namespace: eps.Namespace,
					name:      eps.Name,
				})
			},
		},
	})

	return c, nil
}

func (c *ServiceExportController) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	go c.endpointSliceInformer.Informer().Run(ctx.Done())

	if !cache.WaitForNamedCacheSync(ServiceExportControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for caches to sync")
		return
	}
	logger.Info("Caches are synced")
	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *ServiceExportController) reconcile(ctx context.Context, key reconcileKey) (status worker.Result) {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "cluster", key.cluster, "gvk",
		key.gvk, "namespace", key.namespace, "name", key.name)

	switch key.gvk {
	case common.ServiceExportGVK:
		if err := c.reconcileServiceExport(ctx, key); err != nil {
			keyedLogger.Error(err, fmt.Sprintf("failed to reconcile serviceExport(%s/%s)", key.namespace, key.name))
			return worker.StatusError
		}
	case common.EndpointSliceGVK:
		if err := c.reconcileEndpointSlice(ctx, key); err != nil {
			keyedLogger.Error(err, fmt.Sprintf("failed to reconcile endpointSlice(%s/%s)", key.namespace, key.name))
			return worker.StatusError
		}
	}

	return worker.StatusAllOK
}

func (c *ServiceExportController) reconcileServiceExport(ctx context.Context, key reconcileKey) error {
	logger := klog.FromContext(ctx)

	needReconcile, err := c.filterServiceExport(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to check if serviceExport needs to be filtered: %w", err)
	}
	if !needReconcile {
		return nil
	}

	seListerInterface, synced, exists := c.fedInformerManager.GetResourceListerFromFactory(common.ServiceExportGVR, key.cluster)
	if !exists || !synced() {
		return fmt.Errorf("informer of serviceExport not exists or not synced for cluster %s", key.cluster)
	}

	seLister, ok := seListerInterface.(cache.GenericLister)
	if !ok {
		return fmt.Errorf("failed to convert interface to seLister")
	}

	_, err = seLister.ByNamespace(key.namespace).Get(key.name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(2).Info("Cleanup imported endpointSlice due to serviceExport event")
			deleteErr := c.kubeClient.DiscoveryV1().EndpointSlices(key.namespace).DeleteCollection(ctx,
				metav1.DeleteOptions{}, metav1.ListOptions{
					LabelSelector: labels.SelectorFromSet(labels.Set{
						discoveryv1.LabelServiceName:    key.name,
						EndpointSliceSourceClusterLabel: naming.GenerateSourceClusterValue(key.cluster),
					}).String(),
				})
			if deleteErr != nil {
				logger.Error(deleteErr, "Failed to cleanup importedSlice in control plane")
			}
			return deleteErr
		}
		return err
	}

	clusterEpsListerInterface, synced, exist := c.fedInformerManager.GetResourceListerFromFactory(common.EndpointSliceGVR, key.cluster)
	if !exist || !synced() {
		return fmt.Errorf("informer of endpointSlice not exists or not synced for cluster %s", key.cluster)
	}

	clusterEpsLister, ok := clusterEpsListerInterface.(discoveryv1listers.EndpointSliceLister)
	if !ok {
		return fmt.Errorf("failed to convert interface to clusterEpsLister")
	}

	clusterEndpointSlices, err := clusterEpsLister.List(labels.SelectorFromSet(labels.Set{
		discoveryv1.LabelServiceName: key.name,
	}))
	if err != nil {
		return err
	}

	fedEpsLister := c.endpointSliceInformer.Lister()
	var errs []error
	for _, eps := range clusterEndpointSlices {
		if _, exist = eps.Labels[EndpointSliceSourceClusterLabel]; exist {
			continue
		}

		importedEps := eps.DeepCopy()
		importedEps.ObjectMeta = metav1.ObjectMeta{
			Namespace: eps.Namespace,
			Name:      naming.GenerateImportedEndpointSliceName(eps.GetName(), key.cluster),
			Labels: map[string]string{
				discoveryv1.LabelServiceName:    key.name,
				EndpointSliceSourceClusterLabel: naming.GenerateSourceClusterValue(key.cluster),
			},
		}

		_, getErr := fedEpsLister.EndpointSlices(importedEps.GetNamespace()).Get(importedEps.GetName())
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				logger.V(2).Info("Creating imported endpointSlice due to serviceExport event")
				_, err = c.kubeClient.DiscoveryV1().EndpointSlices(key.namespace).Create(ctx, importedEps, metav1.CreateOptions{})
				if err != nil {
					errs = append(errs, err)
				}
			} else {
				errs = append(errs, getErr)
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (c *ServiceExportController) reconcileEndpointSlice(ctx context.Context, key reconcileKey) error {
	logger := klog.FromContext(ctx)

	clusterEpsListerInterface, synced, exists := c.fedInformerManager.GetResourceListerFromFactory(common.EndpointSliceGVR, key.cluster)
	if !exists || !synced() {
		return fmt.Errorf("informer of endpointSlice not exists or not synced for cluster %s", key.cluster)
	}

	clusterEpsLister, ok := clusterEpsListerInterface.(discoveryv1listers.EndpointSliceLister)
	if !ok {
		return fmt.Errorf("failed to convert interface to clusterEpsLister")
	}

	importedEpsName := naming.GenerateImportedEndpointSliceName(key.name, key.cluster)
	clusterEps, err := clusterEpsLister.EndpointSlices(key.namespace).Get(key.name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			deleteErr := c.kubeClient.DiscoveryV1().EndpointSlices(key.namespace).Delete(ctx,
				importedEpsName, metav1.DeleteOptions{})
			if apierrors.IsNotFound(deleteErr) {
				deleteErr = nil
			}
			if deleteErr != nil {
				logger.Error(deleteErr, "Failed to cleanup imported endpointSlice in control plane")
			}
			return deleteErr
		}
		return err
	}

	clusterEps = clusterEps.DeepCopy()
	needReconcile, err := c.filterEndpointSlice(ctx, key, clusterEps)
	if err != nil {
		return fmt.Errorf("failed to check if endpointSlice needs to be filtered: %w", err)
	}
	if !needReconcile {
		return nil
	}

	clusterEps.ObjectMeta = metav1.ObjectMeta{
		Namespace: key.namespace,
		Name:      importedEpsName,
		Labels: map[string]string{
			discoveryv1.LabelServiceName:    clusterEps.Labels[discoveryv1.LabelServiceName],
			EndpointSliceSourceClusterLabel: naming.GenerateSourceClusterValue(key.cluster),
		},
	}

	fedEpsLister := c.endpointSliceInformer.Lister()
	fedEps, err := fedEpsLister.EndpointSlices(key.namespace).Get(importedEpsName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(2).Info("Creating imported endpointSlice due to endpointSlice event")
			_, createErr := c.kubeClient.DiscoveryV1().EndpointSlices(key.namespace).Create(ctx, clusterEps, metav1.CreateOptions{})
			if createErr != nil {
				logger.Error(createErr, "Failed to create imported endpointSlice in control plane")
				return createErr
			}
		}
		return err
	}

	newFedEps := fedEps.DeepCopy()
	newFedEps.AddressType = clusterEps.AddressType
	newFedEps.Endpoints = clusterEps.Endpoints
	newFedEps.Labels = clusterEps.Labels
	newFedEps.Ports = clusterEps.Ports
	if reflect.DeepEqual(fedEps, newFedEps) {
		return nil
	}

	logger.V(2).Info("Updating imported endpointSlice due to endpointSlice event")
	_, err = c.kubeClient.DiscoveryV1().EndpointSlices(key.namespace).Update(ctx, newFedEps, metav1.UpdateOptions{})
	if err != nil {
		logger.Error(err, "Failed to update imported endpointSlice in control plane")
		return err
	}

	return nil
}

func (c *ServiceExportController) filterServiceExport(ctx context.Context, key reconcileKey) (bool, error) {
	logger := klog.FromContext(ctx)

	svcListerInterface, synced, exists := c.fedInformerManager.GetResourceListerFromFactory(common.ServiceGVR, key.cluster)
	if !exists || !synced() {
		return true, fmt.Errorf("informer of service not exists or not synced for cluster %s", key.cluster)
	}

	svcLister, ok := svcListerInterface.(corev1listers.ServiceLister)
	if !ok {
		return true, fmt.Errorf("failed to convert interface to svcLister")
	}

	svc, err := svcLister.Services(key.namespace).Get(key.name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(2).Info(fmt.Sprintf("Service(%s/%s) that needs to be exported was not found",
				key.namespace, key.name))
			return false, nil
		}
		return true, err
	}

	if len(svc.Spec.Selector) == 0 {
		logger.V(2).Info(fmt.Sprintf("Service(%s/%s) has no selector, cannot be exported",
			key.namespace, key.name))
		return false, nil
	}

	return true, nil
}

func (c *ServiceExportController) filterEndpointSlice(
	ctx context.Context, key reconcileKey,
	eps *discoveryv1.EndpointSlice,
) (bool, error) {
	logger := klog.FromContext(ctx)

	svcName := eps.Labels[discoveryv1.LabelServiceName]
	seListerInterface, synced, exists := c.fedInformerManager.GetResourceListerFromFactory(common.ServiceExportGVR, key.cluster)
	if !exists {
		return false, nil
	}
	if !synced() {
		return true, fmt.Errorf("informer of serviceExport not synced for cluster %s", key.cluster)
	}

	seLister, ok := seListerInterface.(cache.GenericLister)
	if !ok {
		return true, fmt.Errorf("failed to convert interface to seLister")
	}

	_, err := seLister.ByNamespace(eps.Namespace).Get(svcName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(2).Info(fmt.Sprintf("The associated svc(%s/%s) was not exported", eps.Name, svcName))
			return false, nil
		}

		return true, err
	}

	return true, nil
}

// reconcileKeyFunc generates a reconcileKey for object.
func reconcileKeyFunc(cluster string, obj interface{}) (reconcileKey, error) {
	key := reconcileKey{}

	runtimeObject, ok := obj.(runtime.Object)
	if !ok {
		return key, fmt.Errorf("not runtime object")
	}

	metaInfo, err := meta.Accessor(obj)
	if err != nil {
		return key, fmt.Errorf("object has no meta: %w", err)
	}

	gvk := runtimeObject.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		return key, fmt.Errorf("object is a typed instance which will drop gvk info")
	}

	key.gvk = gvk
	key.cluster = cluster
	key.namespace = metaInfo.GetNamespace()
	key.name = metaInfo.GetName()

	return key, nil
}
