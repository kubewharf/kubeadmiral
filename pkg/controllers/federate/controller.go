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

package federate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	dynamicclient "k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	EventReasonCreateFederatedObject = "CreateFederatedObject"
	EventReasonUpdateFederatedObject = "UpdateFederatedObject"

	// If this finalizer is present on a k8s resource, the federate
	// controller will have the opportunity to perform pre-deletion operations
	// (like deleting federtated resources).
	FinalizerFederateController = common.DefaultPrefix + "federate-controller"

	// If this annotation is present on the source object, skip federating it.
	NoFederatedResource      = common.DefaultPrefix + "no-federated-resource"
	RetainReplicasAnnotation = common.DefaultPrefix + "retain-replicas"
)

// FederateController federates objects of source type to objects of federated type
type FederateController struct {
	typeConfig         *fedcorev1a1.FederatedTypeConfig
	name               string
	fedSystemNamespace string

	federatedObjectClient dynamicclient.NamespaceableResourceInterface
	federatedObjectLister cache.GenericLister
	federatedObjectSynced cache.InformerSynced

	sourceObjectClient dynamicclient.NamespaceableResourceInterface
	sourceObjectLister cache.GenericLister
	sourceObjectSynced cache.InformerSynced

	worker        worker.ReconcileWorker
	eventRecorder record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger
}

func NewFederateController(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	kubeClient kubeclient.Interface,
	dynamicClient dynamicclient.Interface,
	federatedObjectInformer informers.GenericInformer,
	sourceObjectInformer informers.GenericInformer,
	metrics stats.Metrics,
	workerCount int,
	fedSystemNamespace string,
) (*FederateController, error) {
	controllerName := fmt.Sprintf("%s-federate-controller", typeConfig.GetFederatedType().Name)

	c := &FederateController{
		typeConfig:         typeConfig,
		name:               controllerName,
		fedSystemNamespace: fedSystemNamespace,
		metrics:            metrics,
		logger:             klog.LoggerWithName(klog.Background(), controllerName),
	}

	c.worker = worker.NewReconcileWorker(
		c.reconcile,
		worker.WorkerTiming{},
		workerCount,
		metrics,
		delayingdeliver.NewMetricTags("federate-controller-worker", c.typeConfig.GetFederatedType().Kind),
	)
	c.eventRecorder = eventsink.NewDefederatingRecorderMux(kubeClient, c.name, 6)

	federatedAPIResource := typeConfig.GetFederatedType()
	c.federatedObjectClient = dynamicClient.Resource(schemautil.APIResourceToGVR(&federatedAPIResource))

	sourceAPIResource := typeConfig.GetSourceType()
	c.sourceObjectClient = dynamicClient.Resource(schemautil.APIResourceToGVR(sourceAPIResource))

	c.sourceObjectLister = sourceObjectInformer.Lister()
	c.sourceObjectSynced = sourceObjectInformer.Informer().HasSynced
	sourceObjectInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			metaObj, err := meta.Accessor(obj)
			if err != nil {
				c.logger.Error(err, fmt.Sprintf("Received source object with invalid type %T", obj))
			}
			return metaObj.GetNamespace() != c.fedSystemNamespace
		},
		Handler: util.NewTriggerOnAllChanges(c.worker.EnqueueObject),
	})

	c.federatedObjectLister = federatedObjectInformer.Lister()
	c.federatedObjectSynced = federatedObjectInformer.Informer().HasSynced
	federatedObjectInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			metaObj, err := meta.Accessor(obj)
			if err != nil {
				c.logger.Error(err, fmt.Sprintf("Received federated object with invalid type %T", obj))
			}
			return metaObj.GetNamespace() != c.fedSystemNamespace
		},
		Handler: util.NewTriggerOnAllChanges(c.worker.EnqueueObject),
	})

	return c, nil
}

func (c *FederateController) Run(ctx context.Context) {
	c.logger.Info("Starting controller")
	defer c.logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(c.name, ctx.Done(), c.sourceObjectSynced, c.federatedObjectSynced) {
		return
	}

	c.worker.Run(ctx.Done())
	<-ctx.Done()
}

func (c *FederateController) reconcile(qualifiedName common.QualifiedName) (status worker.Result) {
	c.metrics.Rate("federate.throughput", 1)
	key := qualifiedName.String()
	keyedLogger := c.logger.WithValues("control-loop", "reconcile", "key", key)
	startTime := time.Now()

	keyedLogger.Info("Start reconcile")
	defer func() {
		c.metrics.Duration(fmt.Sprintf("%s.latency", c.name), startTime)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status.String()).Info("Finished reconcile")
	}()

	sourceObject, err := c.sourceObjectFromStore(qualifiedName)
	if err != nil && apierrors.IsNotFound(err) {
		keyedLogger.Info(fmt.Sprintf("No source object for %s found, skip federating", qualifiedName.String()))
		return worker.StatusAllOK
	}
	if err != nil {
		keyedLogger.Error(err, "Failed to get source object from store")
		return worker.StatusError
	}

	fedObject, err := c.federatedObjectFromStore(qualifiedName)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get federated object from store")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) {
		fedObject = nil
	}

	ctx := klog.NewContext(context.Background(), keyedLogger)
	federatedAPIResource := c.typeConfig.GetFederatedType()
	federatedGVK := schemautil.APIResourceToGVK(&federatedAPIResource)

	if sourceObject.GetDeletionTimestamp() != nil {
		keyedLogger.Info("Source object terminating, ensure deletion of federated object")
		if err := c.handleTerminatingSourceObject(ctx, sourceObject, fedObject); err != nil {
			keyedLogger.Error(err, "Failed to handle source object deletion")
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if noFederatedResource, ok := sourceObject.GetAnnotations()[NoFederatedResource]; ok {
		if len(noFederatedResource) > 0 {
			keyedLogger.Info("No-federated-resource annotation found, skip federating")
			return worker.StatusAllOK
		}
	}

	if sourceObject, err = c.ensureFinalizer(ctx, sourceObject); err != nil {
		keyedLogger.Error(err, "Failed to ensure finalizer on source object")
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	if fedObject == nil {
		keyedLogger.Info("No federated object found, creating federated object")
		if err := c.handleCreateFederatedObject(ctx, sourceObject); err != nil {
			keyedLogger.Error(err, "Failed to create federated object")
			c.eventRecorder.Eventf(
				sourceObject,
				corev1.EventTypeWarning,
				EventReasonCreateFederatedObject,
				"Failed to create federated object: %s %s: %v",
				federatedGVK.String(),
				qualifiedName.String(),
				err,
			)
			return worker.StatusError
		}
		c.eventRecorder.Eventf(
			sourceObject,
			corev1.EventTypeNormal,
			EventReasonCreateFederatedObject,
			"Federated object created: %s %s",
			federatedGVK.String(),
			qualifiedName.String(),
		)
		return worker.StatusAllOK
	}

	keyedLogger.Info("Federated object already exists, reconciling federated object")
	updated, err := c.handleExistingFederatedObject(ctx, sourceObject, fedObject)
	if err != nil {
		keyedLogger.Error(err, "Failed to reconcile existing federated object")
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		c.eventRecorder.Eventf(
			sourceObject,
			corev1.EventTypeWarning,
			EventReasonUpdateFederatedObject,
			"Failed to reconcile existing federated object: %s %s: %v",
			federatedGVK,
			qualifiedName.String(),
			err,
		)
		return worker.StatusError
	}
	if updated {
		c.eventRecorder.Eventf(
			sourceObject,
			corev1.EventTypeNormal,
			EventReasonUpdateFederatedObject,
			"Federated object updated: %s %s",
			federatedGVK,
			qualifiedName.String(),
		)
	} else {
		keyedLogger.V(8).Info("No updates required to the federated object")
	}

	if err := c.updateFeedbackAnnotations(ctx, sourceObject, fedObject); err != nil {
		keyedLogger.Error(err, "Failed to sync feedback annotations to source object")
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (c *FederateController) federatedObjectFromStore(qualifiedName common.QualifiedName) (*unstructured.Unstructured, error) {
	var obj pkgruntime.Object
	var err error

	if c.typeConfig.GetNamespaced() {
		obj, err = c.federatedObjectLister.ByNamespace(qualifiedName.Namespace).Get(qualifiedName.Name)
	} else {
		obj, err = c.federatedObjectLister.Get(qualifiedName.Name)
	}

	return obj.(*unstructured.Unstructured), err
}

func (c *FederateController) sourceObjectFromStore(qualifiedName common.QualifiedName) (*unstructured.Unstructured, error) {
	var obj pkgruntime.Object
	var err error

	if c.typeConfig.GetNamespaced() {
		obj, err = c.sourceObjectLister.ByNamespace(qualifiedName.Namespace).Get(qualifiedName.Name)
	} else {
		obj, err = c.sourceObjectLister.Get(qualifiedName.Name)
	}

	return obj.(*unstructured.Unstructured), err
}

func (c *FederateController) ensureFinalizer(
	ctx context.Context,
	sourceObj *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	logger := klog.FromContext(ctx).WithValues("finalizer", FinalizerFederateController)

	isUpdated, err := finalizersutil.AddFinalizers(sourceObj, sets.NewString(FinalizerFederateController))
	if err != nil {
		return nil, fmt.Errorf("failed to add finalizer to source object: %w", err)
	}
	if !isUpdated {
		return sourceObj, nil
	}

	logger.V(8).Info("Adding finalizer to source object")
	sourceObj, err = c.sourceObjectClient.Namespace(sourceObj.GetNamespace()).Update(ctx, sourceObj, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update source object with finalizer: %w", err)
	}

	return sourceObj, err
}

func (c *FederateController) removeFinalizer(
	ctx context.Context,
	sourceObj *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	logger := klog.FromContext(ctx).WithValues("finalizer", FinalizerFederateController).V(6)

	isUpdated, err := finalizersutil.RemoveFinalizers(sourceObj, sets.NewString(FinalizerFederateController))
	if err != nil {
		return nil, fmt.Errorf("failed to remove finalizer from source object: %w", err)
	}
	if !isUpdated {
		return sourceObj, nil
	}

	logger.V(8).Info("Removing finalizer from source object")
	sourceObj, err = c.sourceObjectClient.Namespace(sourceObj.GetNamespace()).Update(ctx, sourceObj, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update source object without finalizer: %w", err)
	}

	return sourceObj, nil
}

func (c *FederateController) handleTerminatingSourceObject(ctx context.Context, sourceObject, fedObject *unstructured.Unstructured) error {
	logger := klog.FromContext(ctx)

	if fedObject == nil {
		logger.V(6).Info("Federated object deleted, removing finalizer from source object")
		var err error
		if _, err = c.removeFinalizer(ctx, sourceObject); err != nil {
			return fmt.Errorf("failed to remove finalizer from source object: %w", err)
		}
		return nil
	}

	if fedObject.GetDeletionTimestamp() == nil {
		logger.V(6).Info("Federated object still exists, deleting federated object")
		if err := c.federatedObjectClient.Namespace(fedObject.GetNamespace()).Delete(
			ctx,
			fedObject.GetName(),
			metav1.DeleteOptions{},
		); err != nil {
			return fmt.Errorf("failed to delete federated object: %w", err)
		}
	}

	logger.V(6).Info("Federated object is terminating, wait for deletion")
	return nil
}

func (c *FederateController) handleCreateFederatedObject(ctx context.Context, sourceObject *unstructured.Unstructured) error {
	logger := klog.FromContext(ctx)

	logger.V(6).Info("Generating federated object from source object")
	fedObject, err := newFederatedObjectForSourceObject(c.typeConfig, sourceObject)
	if err != nil {
		return fmt.Errorf("failed to generate federated object from source object: %w", err)
	}

	logger.V(6).Info("Set pending controllers on federated object")
	if _, err = pendingcontrollers.SetPendingControllers(fedObject, c.typeConfig.GetControllers()); err != nil {
		return fmt.Errorf("failed to set pending controllers on federated object: %w", err)
	}

	logger.V(6).Info("Creating federated object")
	if _, err = c.federatedObjectClient.Namespace(fedObject.GetNamespace()).Create(
		ctx,
		fedObject,
		metav1.CreateOptions{},
	); err != nil {
		return fmt.Errorf("failed to create federated object: %w", err)
	}

	return nil
}

func (c *FederateController) handleExistingFederatedObject(
	ctx context.Context,
	sourceObject, fedObject *unstructured.Unstructured,
) (bool, error) {
	logger := klog.FromContext(ctx)

	logger.V(6).Info("Checking if federated object needs update")
	needsUpdate, err := updateFederatedObjectForSourceObject(fedObject, c.typeConfig, sourceObject)
	if err != nil {
		return false, fmt.Errorf("failed to check if federated object needs update: %w", err)
	}
	if !needsUpdate {
		logger.V(8).Info("No updates required to the federated object")
		return false, nil
	}

	logger.V(6).Info("Source object differs from federated object, updating federated object")
	if _, err = c.federatedObjectClient.Namespace(fedObject.GetNamespace()).Update(ctx, fedObject, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("failed to update federated object: %w", err)
	}

	return true, nil
}

func (c *FederateController) updateFeedbackAnnotations(ctx context.Context, sourceObject, fedObject *unstructured.Unstructured) error {
	logger := klog.FromContext(ctx)
	hasChanged := false

	logger.V(8).Info("Sync scheduling annotation to source object")
	if err := sourcefeedback.PopulateSchedulingAnnotation(sourceObject, fedObject, &hasChanged); err != nil {
		return fmt.Errorf("failed to sync scheduling annotation to source object: %w", err)
	}

	logger.V(8).Info("Sync syncing annotation to source object")
	if value, exists := fedObject.GetAnnotations()[sourcefeedback.SyncingAnnotation]; exists {
		hasAnnotationChanged, err := annotation.AddAnnotation(sourceObject, sourcefeedback.SyncingAnnotation, value)
		if err != nil {
			return fmt.Errorf("failed to sync syncing annotation to source object: %w", err)
		}
		hasChanged = hasChanged || hasAnnotationChanged
	}

	if hasChanged {
		var err error
		if c.typeConfig.GetSourceType().Group == appsv1.GroupName && c.typeConfig.GetSourceType().Name == "deployments" {
			// deployment bumps generation if annotations are updated
			_, err = c.sourceObjectClient.Namespace(sourceObject.GetNamespace()).UpdateStatus(ctx, sourceObject, metav1.UpdateOptions{})
		} else {
			_, err = c.sourceObjectClient.Namespace(sourceObject.GetNamespace()).Update(ctx, sourceObject, metav1.UpdateOptions{})
		}

		if err != nil {
			return fmt.Errorf("failed to update source object with feedback annotations: %w", err)
		}
	}

	return nil
}

func ensureDeploymentFields(sourceObj, fedObj *unstructured.Unstructured) (bool, error) {
	isUpdate := false
	anno := sourceObj.GetAnnotations()

	// for retainReplicas
	retainReplicasString := anno[RetainReplicasAnnotation]
	retainReplicas := false
	if retainReplicasString == "true" {
		retainReplicas = true
	}
	actualRetainReplicas, ok, err := unstructured.NestedBool(
		fedObj.Object,
		common.SpecField,
		common.RetainReplicasField,
	)
	if err != nil {
		return isUpdate, err
	}
	if !ok || (retainReplicas != actualRetainReplicas) {
		isUpdate = true
		if err = unstructured.SetNestedField(fedObj.Object, retainReplicas, common.SpecField, common.RetainReplicasField); err != nil {
			return isUpdate, err
		}
	}

	// for revisionHistoryLimit
	revisionHistoryLimitString := anno[common.RevisionHistoryLimit]
	revisionHistoryLimit := int64(1)
	if revisionHistoryLimitString != "" {
		revisionHistoryLimit, err = strconv.ParseInt(revisionHistoryLimitString, 10, 64)
		if err != nil {
			return isUpdate, err
		}
	}
	actualRevisionHistoryLimit, ok, err := unstructured.NestedInt64(
		fedObj.Object,
		common.SpecField,
		common.RevisionHistoryLimit,
	)
	if err != nil {
		return isUpdate, err
	}
	if !ok || (revisionHistoryLimit != actualRevisionHistoryLimit) {
		isUpdate = true
		if err = unstructured.SetNestedField(fedObj.Object, revisionHistoryLimit, common.SpecField, common.RevisionHistoryLimit); err != nil {
			return isUpdate, err
		}
	}

	return isUpdate, nil
}
