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

	corev1 "k8s.io/api/core/v1"
	discoveryv1b1 "k8s.io/api/discovery/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	discoveryv1b1informers "k8s.io/client-go/informers/discovery/v1beta1"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/follower"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	ServiceImportControllerName = "serviceimport-controller"

	endpointSliceFtcName                = "endpointslices.discovery.k8s.io"
	serviceImportFtcName                = "serviceimports.multicluster.x-k8s.io"
	PrefixedGlobalSchedulerName         = common.DefaultPrefix + "global-scheduler"
	PrefixedServiceImportControllerName = common.DefaultPrefix + ServiceImportControllerName

	EventReasonSyncPlacementsToEndpointSlice = "SyncPlacementsToEndpointSlice"
)

type ServiceImportController struct {
	name string

	endpointSliceInformer discoveryv1b1informers.EndpointSliceInformer
	fedObjectInformer     fedcorev1a1informers.FederatedObjectInformer
	fedClient             fedclient.Interface

	worker worker.ReconcileWorker[fedObjKey]

	eventRecorder record.EventRecorder
	logger        klog.Logger
	metrics       stats.Metrics
}

type fedObjKey struct {
	qualifiedName common.QualifiedName
	sourceObjKind string
	sourceObjName string
}

func newFedObjKey(fedObj fedcorev1a1.GenericFederatedObject, sourceObjKind string) fedObjKey {
	return fedObjKey{
		qualifiedName: common.NewQualifiedName(fedObj),
		sourceObjName: fedObj.GetOwnerReferences()[0].Name,
		sourceObjKind: sourceObjKind,
	}
}

func (c *ServiceImportController) IsControllerReady() bool {
	return c.HasSynced()
}

func (c *ServiceImportController) HasSynced() bool {
	return c.fedObjectInformer.Informer().HasSynced() && c.endpointSliceInformer.Informer().HasSynced()
}

func NewServiceImportController(
	kubeClient kubeclient.Interface,
	endpointSliceInformer discoveryv1b1informers.EndpointSliceInformer,
	fedClient fedclient.Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*ServiceImportController, error) {
	c := &ServiceImportController{
		name: ServiceImportControllerName,

		endpointSliceInformer: endpointSliceInformer,
		fedObjectInformer:     fedObjectInformer,
		fedClient:             fedClient,

		metrics:       metrics,
		logger:        logger.WithValues("controller", ServiceImportControllerName),
		eventRecorder: eventsink.NewDefederatingRecorderMux(kubeClient, ServiceImportControllerName, 6),
	}

	c.worker = worker.NewReconcileWorker[fedObjKey](
		ServiceImportControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	federatedObjectHandler := cache.ResourceEventHandlerFuncs{
		// Only need to handle fedObj whose template is ServiceImport or EndpointSlice
		// For endpointSlice fedObj, only need to handle add event
		AddFunc: func(obj interface{}) {
			fedObj := obj.(fedcorev1a1.GenericFederatedObject)
			if fedObj.GetLabels()[mcsv1alpha1.GroupVersion.String()] == common.ServiceImportKind {
				c.worker.Enqueue(newFedObjKey(fedObj, common.ServiceImportKind))
			}
			if fedObj.GetLabels()[discoveryv1b1.SchemeGroupVersion.String()] == common.EndpointSliceKind {
				c.worker.Enqueue(newFedObjKey(fedObj, common.EndpointSliceKind))
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldFedObj, newFedObj := oldObj.(fedcorev1a1.GenericFederatedObject), newObj.(fedcorev1a1.GenericFederatedObject)
			if newFedObj.GetLabels()[mcsv1alpha1.GroupVersion.String()] != common.ServiceImportKind {
				return
			}
			oldTemplate, err := oldFedObj.GetSpec().GetTemplateAsUnstructured()
			if err != nil {
				return
			}
			newTemplate, err := newFedObj.GetSpec().GetTemplateAsUnstructured()
			if err != nil {
				return
			}
			oldPlacement := oldFedObj.GetSpec().Placements
			newPlacement := newFedObj.GetSpec().Placements
			if !reflect.DeepEqual(oldPlacement, newPlacement) || !reflect.DeepEqual(oldTemplate, newTemplate) {
				c.worker.Enqueue(newFedObjKey(newFedObj, common.ServiceImportKind))
			}
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
				if obj == nil {
					return
				}
			}
			fedObj := obj.(fedcorev1a1.GenericFederatedObject)
			if fedObj.GetLabels()[mcsv1alpha1.GroupVersion.String()] != common.ServiceImportKind {
				return
			}
			c.worker.Enqueue(newFedObjKey(fedObj, common.ServiceImportKind))
		},
	}
	if _, err := c.fedObjectInformer.Informer().AddEventHandler(federatedObjectHandler); err != nil {
		return nil, fmt.Errorf("failed to create federated informer: %w", err)
	}

	return c, nil
}

func (c *ServiceImportController) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	go c.endpointSliceInformer.Informer().Run(ctx.Done())

	if !cache.WaitForNamedCacheSync(ServiceImportControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for caches to sync")
		return
	}
	logger.Info("Caches are synced")
	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *ServiceImportController) reconcile(ctx context.Context, fedObjKey fedObjKey) (status worker.Result) {
	qualifiedName := fedObjKey.qualifiedName
	key := qualifiedName.String()
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "control-loop", "reconcile", "object", key)

	fedObject, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		nil,
		qualifiedName.Namespace,
		qualifiedName.Name,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get federated object from store")
		return worker.StatusError
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		if fedObjKey.sourceObjKind == common.ServiceImportKind {
			if err = c.syncPlacementsToEndpointSlice(ctx, fedObjKey.qualifiedName.Namespace, fedObjKey.sourceObjName,
				nil); err != nil {
				keyedLogger.Error(err, "Failed to sync placements to endpointSlice")
				return worker.StatusError
			}

			// Usually derivedSvcFedObj relies on the cascade deletion mechanism of K8s for cleaning,
			// but the GC time of K8s is not certain, so when it has not been recycled,
			// the controller needs to perform the deletion action.
			derivedSvcFedObj, err := fedobjectadapters.GetFromLister(
				c.fedObjectInformer.Lister(),
				nil,
				fedObjKey.qualifiedName.Namespace,
				naming.GenerateDerivedSvcFedObjName(fedObjKey.sourceObjName),
			)
			if err != nil && !apierrors.IsNotFound(err) {
				keyedLogger.Error(err, "Failed to get derived service federated object from store")
				return worker.StatusError
			}

			if derivedSvcFedObj != nil && derivedSvcFedObj.GetDeletionTimestamp() == nil {
				if err = fedobjectadapters.Delete(
					ctx,
					c.fedClient.CoreV1alpha1(),
					derivedSvcFedObj.GetNamespace(),
					derivedSvcFedObj.GetName(),
					metav1.DeleteOptions{},
				); err != nil {
					return worker.StatusError
				}
			}

			return worker.StatusAllOK
		}

		return worker.StatusAllOK
	}

	if follower.SkipSync(fedObject) {
		return worker.StatusAllOK
	}

	template, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		keyedLogger.Error(err, "Failed to get template from federated object")
		return worker.StatusError
	}

	switch template.GroupVersionKind() {
	case common.EndpointSliceGVK:
		return c.reconcileEpsFedObj(ctx, template)
	case common.ServiceImportGVK:
		return c.reconcileSvcImportFedObj(ctx, fedObject, template)
	}

	return worker.StatusAllOK
}

func (c *ServiceImportController) reconcileEpsFedObj(
	ctx context.Context,
	unstructuredEps *unstructured.Unstructured,
) (status worker.Result) {
	logger := klog.FromContext(ctx)

	svcName := unstructuredEps.GetLabels()[discoveryv1b1.LabelServiceName]

	siFedObject, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		nil,
		unstructuredEps.GetNamespace(),
		naming.GenerateFederatedObjectName(svcName, serviceImportFtcName),
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(2).Info("serviceImport federated object not found, no need to reconcile")
			return worker.StatusAllOK
		}

		logger.Error(err, "Failed to get serviceImport federated object from store")
		return worker.StatusError
	}

	serviceImportTemplate, err := siFedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		logger.Error(err, "Failed to get serviceImport template from federated object")
		return worker.StatusError
	}
	serviceImportObj := &mcsv1alpha1.ServiceImport{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(serviceImportTemplate.UnstructuredContent(), serviceImportObj); err != nil {
		logger.Error(err, "Failed to convert ServiceImport from unstructured object")
		return worker.StatusError
	}

	if err := c.syncPlacementsToEndpointSlice(ctx, serviceImportObj.Namespace, serviceImportObj.Name,
		siFedObject.GetSpec().GetControllerPlacement(PrefixedGlobalSchedulerName)); err != nil {
		logger.Error(err, "Failed to sync placements to endpointSlice")
		c.eventRecorder.Eventf(
			serviceImportObj,
			corev1.EventTypeWarning,
			EventReasonSyncPlacementsToEndpointSlice,
			"Failed to sync placements to endpointSlice: %v",
			err,
		)
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (c *ServiceImportController) reconcileSvcImportFedObj(
	ctx context.Context,
	siFedObject fedcorev1a1.GenericFederatedObject,
	unstructuredSvcImport *unstructured.Unstructured,
) (status worker.Result) {
	logger := klog.FromContext(ctx)

	serviceImportObj := &mcsv1alpha1.ServiceImport{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredSvcImport.UnstructuredContent(),
		serviceImportObj); err != nil {
		logger.Error(err, "Failed to convert ServiceImport from unstructured object")
		return worker.StatusError
	}

	if serviceImportObj.Spec.Type != mcsv1alpha1.ClusterSetIP {
		logger.V(2).Info("only support ClusterSetIP type, no need to reconcile")
		return worker.StatusAllOK
	}

	derivedSvcFedObj, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		nil,
		serviceImportObj.Namespace,
		naming.GenerateDerivedSvcFedObjName(serviceImportObj.Name),
	)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get derived service federated object from store")
		return worker.StatusError
	}

	newDerivedSvc := newDerivedService(serviceImportObj)

	if derivedSvcFedObj == nil {
		if err := c.handleCreateFederatedObject(ctx, serviceImportObj,
			newDerivedSvc, siFedObject); err != nil {
			logger.Error(err, "Failed to create derived service federated object")
			return worker.StatusError
		}
	} else if derivedSvcFedObj.GetDeletionTimestamp() != nil {
		return worker.StatusError
	} else {
		derivedSvcFedObj = derivedSvcFedObj.DeepCopyGenericFederatedObject()
		if err := c.handleExistingFederatedObject(ctx, serviceImportObj, newDerivedSvc,
			derivedSvcFedObj, siFedObject); err != nil {
			logger.Error(err, "Failed to reconcile existing derived service federated object")
			return worker.StatusError
		}
	}

	if err := c.syncPlacementsToEndpointSlice(ctx, serviceImportObj.Namespace, serviceImportObj.Name,
		siFedObject.GetSpec().GetControllerPlacement(PrefixedGlobalSchedulerName)); err != nil {
		logger.Error(err, "Failed to sync placements to endpointSlice")
		c.eventRecorder.Eventf(
			serviceImportObj,
			corev1.EventTypeWarning,
			EventReasonSyncPlacementsToEndpointSlice,
			"Failed to sync placements to endpointSlice: %v",
			err,
		)
		return worker.StatusError
	}
	return worker.StatusAllOK
}

func newDerivedService(svcImport *mcsv1alpha1.ServiceImport) *corev1.Service {
	derivedService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       common.ServiceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcImport.Namespace,
			Name:      svcImport.Name,
			Annotations: map[string]string{
				common.DerivedServiceAnnotation: svcImport.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeClusterIP,
			Ports: servicePorts(svcImport),
		},
	}

	return derivedService
}

func (c *ServiceImportController) syncPlacementsToEndpointSlice(
	ctx context.Context,
	namespace string,
	name string,
	placements []fedcorev1a1.ClusterReference,
) error {
	logger := klog.FromContext(ctx)

	epsLister := c.endpointSliceInformer.Lister()
	epsList, _ := epsLister.EndpointSlices(namespace).List(
		labels.SelectorFromSet(labels.Set{
			discoveryv1b1.LabelServiceName: name,
		}))

	var errs []error
	for _, eps := range epsList {
		epsFedObjName := naming.GenerateFederatedObjectName(eps.Name, endpointSliceFtcName)
		epsFedObj, err := fedobjectadapters.GetFromLister(
			c.fedObjectInformer.Lister(),
			nil,
			eps.Namespace,
			epsFedObjName,
		)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		clusterNames := []string{}
		for _, clusterReference := range placements {
			clusterNames = append(clusterNames, clusterReference.Cluster)
		}
		updated := epsFedObj.GetSpec().SetControllerPlacement(PrefixedServiceImportControllerName, clusterNames)
		if !updated {
			logger.V(1).Info("No need to update endpointSlice federated object")
			continue
		}
		logger.V(1).Info(fmt.Sprintf("Updating endpointSlice federated object(%s/%s)",
			epsFedObj.GetNamespace(), epsFedObj.GetName()))
		if _, err := fedobjectadapters.Update(
			ctx,
			c.fedClient.CoreV1alpha1(),
			epsFedObj,
			metav1.UpdateOptions{},
		); err != nil {
			errs = append(errs, err)
			logger.Error(err, "Failed to update endpointSlice federated object")
			c.eventRecorder.Eventf(
				eps.DeepCopyObject(),
				corev1.EventTypeWarning,
				federate.EventReasonUpdateFederatedObject,
				"Failed to update EndpointSlice federated object: %v",
				err,
			)
			continue
		}
		logger.V(3).Info(fmt.Sprintf("EndpointSlice federated object(%s/%s) updated",
			epsFedObj.GetNamespace(), epsFedObj.GetName()))
	}

	return utilerrors.NewAggregate(errs)
}

func (c *ServiceImportController) handleCreateFederatedObject(
	ctx context.Context,
	serviceImport *mcsv1alpha1.ServiceImport,
	service *corev1.Service,
	siFedObj fedcorev1a1.GenericFederatedObject,
) error {
	logger := klog.FromContext(ctx)

	fedObj, err := newFederatedObjectForDerivedSvc(service, siFedObj)
	if err != nil {
		return fmt.Errorf("failed to generate federated object from derived service: %w", err)
	}

	logger.V(1).Info("Creating federated Object whose template is derived service")
	if _, err := fedobjectadapters.Create(
		ctx,
		c.fedClient.CoreV1alpha1(),
		fedObj,
		metav1.CreateOptions{},
	); err != nil {
		c.eventRecorder.Eventf(
			serviceImport,
			corev1.EventTypeWarning,
			federate.EventReasonCreateFederatedObject,
			"Failed to create derived service federated object: %v",
			err,
		)
		return fmt.Errorf("failed to create derived service federated object: %w", err)
	}

	return nil
}

func newFederatedObjectForDerivedSvc(
	service *corev1.Service,
	siFedObj fedcorev1a1.GenericFederatedObject,
) (fedcorev1a1.GenericFederatedObject, error) {
	fedObj := &fedcorev1a1.FederatedObject{}

	fedName := naming.GenerateDerivedSvcFedObjName(service.Name)
	fedObj.SetName(fedName)
	fedObj.SetNamespace(service.Namespace)
	fedObj.SetAnnotations(map[string]string{
		common.DerivedServiceAnnotation: service.Name,
	})
	fedObj.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(siFedObj,
		common.FederatedObjectGVK)})
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
	if err != nil {
		return nil, fmt.Errorf("failed to convert derived service to unstructured map: %w", err)
	}
	unstructuredObj := federate.TemplateForSourceObject(&unstructured.Unstructured{Object: unstructuredMap}, service.Annotations, nil)
	rawTemplate, err := unstructuredObj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal template: %w", err)
	}
	fedObj.GetSpec().Template.Raw = rawTemplate

	emptyPendingController := pendingcontrollers.PendingControllers{}
	_, err = pendingcontrollers.SetPendingControllers(fedObj, emptyPendingController)
	if err != nil {
		return nil, fmt.Errorf("failed to set pending controller anno: %w", err)
	}

	placements := siFedObj.GetSpec().GetControllerPlacement(PrefixedGlobalSchedulerName)
	clusterNames := []string{}
	for _, clusterReference := range placements {
		clusterNames = append(clusterNames, clusterReference.Cluster)
	}
	fedObj.GetSpec().SetControllerPlacement(PrefixedServiceImportControllerName, clusterNames)
	return fedObj, nil
}

func (c *ServiceImportController) handleExistingFederatedObject(
	ctx context.Context,
	serviceImport *mcsv1alpha1.ServiceImport,
	service *corev1.Service,
	fedObj fedcorev1a1.GenericFederatedObject,
	siFedObj fedcorev1a1.GenericFederatedObject,
) error {
	logger := klog.FromContext(ctx)

	needsUpdate, err := updateFederatedObjectForDerivedSvc(service, fedObj, siFedObj)
	if err != nil {
		return fmt.Errorf("failed to check if federated object needs update: %w", err)
	}

	if !needsUpdate {
		logger.V(3).Info("No updates required to the federated object")
		return nil
	}

	logger.V(1).Info("Updating federated Object whose template is derived service")
	if _, err := fedobjectadapters.Update(
		ctx,
		c.fedClient.CoreV1alpha1(),
		fedObj,
		metav1.UpdateOptions{},
	); err != nil {
		c.eventRecorder.Eventf(
			serviceImport,
			corev1.EventTypeWarning,
			federate.EventReasonCreateFederatedObject,
			"Failed to update derived service federated object: %v",
			err,
		)
		return fmt.Errorf("failed to update derived service federated object: %w", err)
	}

	return nil
}

func updateFederatedObjectForDerivedSvc(
	service *corev1.Service,
	fedObj fedcorev1a1.GenericFederatedObject,
	siFedObj fedcorev1a1.GenericFederatedObject,
) (bool, error) {
	isUpdated := false

	currentOwner := fedObj.GetOwnerReferences()
	desiredOwner := []metav1.OwnerReference{*metav1.NewControllerRef(siFedObj,
		common.FederatedObjectGVK)}
	if !reflect.DeepEqual(currentOwner, desiredOwner) {
		fedObj.SetOwnerReferences(desiredOwner)
		isUpdated = true
	}

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
	if err != nil {
		return false, fmt.Errorf("failed to convert derived service to unstructured map: %w", err)
	}
	targetTemplate := federate.TemplateForSourceObject(&unstructured.Unstructured{Object: unstructuredMap}, service.Annotations, nil)
	foundTemplate := &unstructured.Unstructured{}
	if err := foundTemplate.UnmarshalJSON(fedObj.GetSpec().Template.Raw); err != nil {
		return false, fmt.Errorf("failed to unmarshal template from federated object: %w", err)
	}
	if !reflect.DeepEqual(foundTemplate.Object, targetTemplate.Object) {
		rawTargetTemplate, err := targetTemplate.MarshalJSON()
		if err != nil {
			return false, fmt.Errorf("failed to marshal template: %w", err)
		}
		fedObj.GetSpec().Template.Raw = rawTargetTemplate
		isUpdated = true
	}

	clusterNames := []string{}
	for _, clusterReference := range siFedObj.GetSpec().GetControllerPlacement(PrefixedGlobalSchedulerName) {
		clusterNames = append(clusterNames, clusterReference.Cluster)
	}
	if needUpdate := fedObj.GetSpec().SetControllerPlacement(PrefixedServiceImportControllerName, clusterNames); needUpdate {
		isUpdated = true
	}
	return isUpdated, nil
}

func servicePorts(svcImport *mcsv1alpha1.ServiceImport) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, len(svcImport.Spec.Ports))
	for i, p := range svcImport.Spec.Ports {
		ports[i] = corev1.ServicePort{
			Name:        p.Name,
			Protocol:    p.Protocol,
			Port:        p.Port,
			AppProtocol: p.AppProtocol,
		}
	}
	return ports
}
