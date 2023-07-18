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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	dynamicclient "k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/meta"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	FederateControllerName = "federate-controller"

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
	informerManager          informermanager.InformerManager
	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer

	fedClient     fedclient.Interface
	dynamicClient dynamic.Interface

	worker        worker.ReconcileWorker[workerKey]
	eventRecorder record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger
}

func (c *FederateController) IsControllerReady() bool {
	return c.HasSynced()
}

func NewFederateController(
	kubeClient kubeclient.Interface,
	dynamicClient dynamicclient.Interface,
	fedClient fedclient.Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	informerManager informermanager.InformerManager,
	metrics stats.Metrics,
	workerCount int,
	fedSystemNamespace string,
) (*FederateController, error) {
	c := &FederateController{
		informerManager:          informerManager,
		fedObjectInformer:        fedObjectInformer,
		clusterFedObjectInformer: clusterFedObjectInformer,
		fedClient:                fedClient,
		dynamicClient:            dynamicClient,
		metrics:                  metrics,
		logger:                   klog.Background().WithValues("controller", FederateControllerName),
	}

	c.eventRecorder = eventsink.NewDefederatingRecorderMux(kubeClient, FederateControllerName, 6)
	c.worker = worker.NewReconcileWorker[workerKey](
		FederateControllerName,
		nil,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	if err := informerManager.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: informermanager.RegisterOncePredicate,
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			return eventhandlers.NewTriggerOnAllChanges(func(obj runtime.Object) {
				uns := obj.(*unstructured.Unstructured)
				c.worker.Enqueue(workerKey{
					name:      uns.GetName(),
					namespace: uns.GetNamespace(),
					gvk:       ftc.GetSourceTypeGVK(),
				})
			})
		},
	}); err != nil {
		return nil, err
	}

	if _, err := fedObjectInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChanges(func(o runtime.Object) {
			fedObj := o.(*fedcorev1a1.FederatedObject)
			logger := c.logger.WithValues("federated-object", common.NewQualifiedName(fedObj))

			srcMeta, err := meta.GetSourceObjectMeta(fedObj)
			if err != nil {
				logger.Error(err, "Failed to get source object's metadata from FederatedObject")
				return
			}

			gvk := srcMeta.GroupVersionKind()

			c.worker.Enqueue(workerKey{
				name:      srcMeta.GetName(),
				namespace: srcMeta.GetNamespace(),
				gvk:       gvk,
			})
		}),
	); err != nil {
		return nil, err
	}

	if _, err := clusterFedObjectInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChanges(func(o runtime.Object) {
			fedObj := o.(*fedcorev1a1.ClusterFederatedObject)
			logger := c.logger.WithValues("cluster-federated-object", common.NewQualifiedName(fedObj))

			srcMeta, err := meta.GetSourceObjectMeta(fedObj)
			if err != nil {
				logger.Error(err, "Failed to get source object's metadata from ClusterFederatedObject")
				return
			}

			gvk := srcMeta.GroupVersionKind()

			c.worker.Enqueue(workerKey{
				name:      srcMeta.GetName(),
				namespace: srcMeta.GetNamespace(),
				gvk:       gvk,
			})
		}),
	); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *FederateController) Run(ctx context.Context) {
	ctx, logger := logging.InjectLoggerValues(ctx, "controller", FederateControllerName)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(FederateControllerName, ctx.Done(), c.HasSynced) {
		return
	}

	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *FederateController) HasSynced() bool {
	return c.informerManager.HasSynced() && c.fedObjectInformer.Informer().HasSynced()
}

func (c *FederateController) reconcile(ctx context.Context, key workerKey) (status worker.Result) {
	_ = c.metrics.Rate("federate.throughput", 1)
	ctx, logger := logging.InjectLogger(ctx, c.logger)
	ctx, logger = logging.InjectLoggerValues(ctx, "source-object", key.String())
	startTime := time.Now()

	logger.V(3).Info("Start reconcile")
	defer func() {
		c.metrics.Duration(fmt.Sprintf("%s.latency", FederateControllerName), startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	sourceGVK := key.gvk
	ctx, logger = logging.InjectLoggerValues(ctx, "gvk", sourceGVK)

	ftc, exists := c.informerManager.GetResourceFTC(key.gvk)
	if !exists {
		logger.Error(nil, "FTC does not exist for GVK")
		return worker.StatusError
	}
	ctx, logger = logging.InjectLoggerValues(ctx, "ftc", ftc.Name)

	sourceGVR := ftc.GetSourceTypeGVR()
	ctx, logger = logging.InjectLoggerValues(ctx, "gvr", sourceGVR)

	sourceObject, err := c.sourceObjectFromStore(key)
	if err != nil && apierrors.IsNotFound(err) {
		logger.V(3).Info(fmt.Sprintf("No source object for found, skip federating"))
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get source object from store")
		return worker.StatusError
	}
	sourceObject = sourceObject.DeepCopy()

	fedObjectName := naming.GenerateFederatedObjectName(sourceObject.GetName(), ftc.Name)
	ctx, logger = logging.InjectLoggerValues(ctx, "federated-object", fedObjectName)

	fedObject, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		sourceObject.GetNamespace(),
		fedObjectName,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get federated object from store")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) {
		fedObject = nil
	} else {
		fedObject = fedObject.DeepCopyGenericFederatedObject()
	}

	if sourceObject.GetDeletionTimestamp() != nil {
		logger.V(3).Info("Source object terminating")
		if err := c.handleTerminatingSourceObject(ctx, sourceGVR, sourceObject, fedObject); err != nil {
			logger.Error(err, "Failed to handle source object deletion")
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if noFederatedResource, ok := sourceObject.GetAnnotations()[NoFederatedResource]; ok {
		if len(noFederatedResource) > 0 {
			logger.V(3).Info("No-federated-resource annotation found, skip federating")
			return worker.StatusAllOK
		}
	}

	if sourceObject, err = c.ensureFinalizer(ctx, sourceGVR, sourceObject); err != nil {
		logger.Error(err, "Failed to ensure finalizer on source object")
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	if fedObject == nil {
		logger.V(3).Info("No federated object found")
		if err := c.handleCreateFederatedObject(ctx, ftc, sourceObject); err != nil {
			logger.Error(err, "Failed to create federated object")
			c.eventRecorder.Eventf(
				sourceObject,
				corev1.EventTypeWarning,
				EventReasonCreateFederatedObject,
				"Failed to create federated object: %v",
				err,
			)

			if apierrors.IsInvalid(err) {
				// if the federated object template is invalid, reenqueueing will not help solve the problem. instead,
				// we should wait for the source object template to be updated - which will trigger its own reconcile.
				return worker.StatusErrorNoRetry
			}
			return worker.StatusError
		}
		c.eventRecorder.Eventf(
			sourceObject,
			corev1.EventTypeNormal,
			EventReasonCreateFederatedObject,
			"Federated object created",
		)
		return worker.StatusAllOK
	}

	logger.V(3).Info("Federated object already exists")
	updated, err := c.handleExistingFederatedObject(ctx, ftc, sourceObject, fedObject)
	if err != nil {
		logger.Error(err, "Failed to reconcile existing federated object")
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		c.eventRecorder.Eventf(
			sourceObject,
			corev1.EventTypeWarning,
			EventReasonUpdateFederatedObject,
			"Failed to reconcile existing federated object %s: %v",
			fedObject.GetName(),
			err,
		)
		return worker.StatusError
	}
	if updated {
		c.eventRecorder.Eventf(
			sourceObject,
			corev1.EventTypeNormal,
			EventReasonUpdateFederatedObject,
			"Federated object updated: %s",
			fedObject.GetName(),
		)
	} else {
		logger.V(3).Info("No updates required to the federated object")
	}

	return worker.StatusAllOK
}

func (c *FederateController) sourceObjectFromStore(key workerKey) (*unstructured.Unstructured, error) {
	lister, hasSynced, exists := c.informerManager.GetResourceLister(key.gvk)
	if !exists {
		return nil, fmt.Errorf("lister for %s does not exist", key.gvk)
	}
	if !hasSynced() {
		return nil, fmt.Errorf("lister for %s not synced", key.gvk)
	}

	var obj runtime.Object
	var err error

	if key.namespace == "" {
		obj, err = lister.Get(key.name)
	} else {
		obj, err = lister.ByNamespace(key.namespace).Get(key.name)
	}

	return obj.(*unstructured.Unstructured), err
}

func (c *FederateController) ensureFinalizer(
	ctx context.Context,
	sourceGVR schema.GroupVersionResource,
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

	logger.V(1).Info("Adding finalizer to source object")

	sourceObj, err = c.dynamicClient.Resource(sourceGVR).Namespace(sourceObj.GetNamespace()).Update(
		ctx,
		sourceObj,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update source object with finalizer: %w", err)
	}

	return sourceObj, err
}

func (c *FederateController) removeFinalizer(
	ctx context.Context,
	sourceGVR schema.GroupVersionResource,
	sourceObj *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	logger := klog.FromContext(ctx).WithValues("finalizer", FinalizerFederateController)

	isUpdated, err := finalizersutil.RemoveFinalizers(sourceObj, sets.NewString(FinalizerFederateController))
	if err != nil {
		return nil, fmt.Errorf("failed to remove finalizer from source object: %w", err)
	}
	if !isUpdated {
		return sourceObj, nil
	}

	logger.V(1).Info("Removing finalizer from source object")

	sourceObj, err = c.dynamicClient.Resource(sourceGVR).Namespace(sourceObj.GetNamespace()).Update(
		ctx,
		sourceObj,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update source object without finalizer: %w", err)
	}

	return sourceObj, nil
}

func (c *FederateController) handleTerminatingSourceObject(
	ctx context.Context,
	sourceGVR schema.GroupVersionResource,
	sourceObject *unstructured.Unstructured,
	fedObject fedcorev1a1.GenericFederatedObject,
) error {
	logger := klog.FromContext(ctx)

	if fedObject == nil {
		logger.V(3).Info("Federated object deleted")
		var err error
		if _, err = c.removeFinalizer(ctx, sourceGVR, sourceObject); err != nil {
			return fmt.Errorf("failed to remove finalizer from source object: %w", err)
		}
		return nil
	}

	if fedObject.GetDeletionTimestamp() == nil {
		logger.V(1).Info("Deleting federated object")
		if err := fedobjectadapters.Delete(
			ctx,
			c.fedClient.CoreV1alpha1(),
			fedObject.GetNamespace(),
			fedObject.GetName(),
			metav1.DeleteOptions{},
		); err != nil {
			return fmt.Errorf("failed to delete federated object: %w", err)
		}
	}

	logger.V(3).Info("Federated object is terminating")
	return nil
}

func (c *FederateController) handleCreateFederatedObject(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
	sourceObject *unstructured.Unstructured,
) error {
	logger := klog.FromContext(ctx)

	logger.V(2).Info("Generating federated object from source object")
	fedObject, err := newFederatedObjectForSourceObject(ftc, sourceObject)
	if err != nil {
		return fmt.Errorf("failed to generate federated object from source object: %w", err)
	}

	if _, err = pendingcontrollers.SetPendingControllers(fedObject, ftc.GetControllers()); err != nil {
		return fmt.Errorf("failed to set pending controllers on federated object: %w", err)
	}

	logger.V(1).Info("Creating federated object")
	if _, err := fedobjectadapters.Create(
		ctx,
		c.fedClient.CoreV1alpha1(),
		fedObject,
		metav1.CreateOptions{},
	); err != nil {
		return fmt.Errorf("failed to create federated object: %w", err)
	}

	return nil
}

func (c *FederateController) handleExistingFederatedObject(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
	sourceObject *unstructured.Unstructured,
	fedObject fedcorev1a1.GenericFederatedObject,
) (bool, error) {
	logger := klog.FromContext(ctx)

	logger.V(3).Info("Checking if federated object needs update")
	needsUpdate, err := updateFederatedObjectForSourceObject(ftc,sourceObject, fedObject)
	if err != nil {
		return false, fmt.Errorf("failed to check if federated object needs update: %w", err)
	}
	if !needsUpdate {
		logger.V(3).Info("No updates required to the federated object")
		return false, nil
	}

	logger.V(1).Info("Updating federated object")
	if _, err = fedobjectadapters.Update(
		ctx,
		c.fedClient.CoreV1alpha1(),
		fedObject,
		metav1.UpdateOptions{},
	); err != nil {
		return false, fmt.Errorf("failed to update federated object: %w", err)
	}

	return true, nil
}
