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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
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
	NoFederatedResource = common.DefaultPrefix + "no-federated-resource"
)

// FederateController federates objects of source type to FederatedObjects or ClusterFederatedObjects.
type FederateController struct {
	informerManager          informermanager.InformerManager
	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer

	fedClient     fedclient.Interface
	dynamicClient dynamic.Interface

	worker               worker.ReconcileWorker[workerKey]
	cacheSyncRateLimiter workqueue.RateLimiter
	eventRecorder        record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger
}

func (c *FederateController) IsControllerReady() bool {
	return c.HasSynced()
}

func NewFederateController(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	fedClient fedclient.Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	informerManager informermanager.InformerManager,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
	fedSystemNamespace string,
) (*FederateController, error) {
	c := &FederateController{
		informerManager:          informerManager,
		fedObjectInformer:        fedObjectInformer,
		clusterFedObjectInformer: clusterFedObjectInformer,
		fedClient:                fedClient,
		dynamicClient:            dynamicClient,
		cacheSyncRateLimiter:     workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 10*time.Second),
		metrics:                  metrics,
		logger:                   logger.WithValues("controller", FederateControllerName),
	}

	c.eventRecorder = eventsink.NewDefederatingRecorderMux(kubeClient, FederateControllerName, 6)
	c.worker = worker.NewReconcileWorker[workerKey](
		FederateControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	if err := informerManager.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: informermanager.RegisterOncePredicate,
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			return cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					if deleted, ok := obj.(*cache.DeletedFinalStateUnknown); ok {
						obj = deleted.Obj
					}
					uns := obj.(*unstructured.Unstructured)
					return uns.GetNamespace() != fedSystemNamespace
				},
				Handler: eventhandlers.NewTriggerOnAllChanges(
					func(uns *unstructured.Unstructured) {
						c.worker.Enqueue(workerKey{
							name:      uns.GetName(),
							namespace: uns.GetNamespace(),
							gvk:       ftc.GetSourceTypeGVK(),
						})
					},
				),
			}
		},
	}); err != nil {
		return nil, err
	}

	if _, err := fedObjectInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			if deleted, ok := obj.(*cache.DeletedFinalStateUnknown); ok {
				obj = deleted.Obj
			}
			fedObj := obj.(*fedcorev1a1.FederatedObject)
			return fedObj.Namespace != fedSystemNamespace
		},
		Handler: eventhandlers.NewTriggerOnAllChanges(
			func(fedObj *fedcorev1a1.FederatedObject) {
				srcMeta, err := fedObj.Spec.GetTemplateAsUnstructured()
				if err != nil {
					c.logger.Error(
						err,
						"Failed to get source object's metadata from FederatedObject",
						"object",
						common.NewQualifiedName(fedObj),
					)
					return
				}

				gvk := srcMeta.GroupVersionKind()

				c.worker.Enqueue(workerKey{
					name:      srcMeta.GetName(),
					namespace: srcMeta.GetNamespace(),
					gvk:       gvk,
				})
			}),
	}); err != nil {
		return nil, err
	}

	if _, err := clusterFedObjectInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChanges(
			func(fedObj *fedcorev1a1.ClusterFederatedObject) {
				srcMeta, err := fedObj.Spec.GetTemplateAsUnstructured()
				if err != nil {
					logger.Error(
						err,
						"Failed to get source object's metadata from ClusterFederatedObject",
						"object",
						common.NewQualifiedName(fedObj),
					)
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
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(FederateControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for cache sync")
		return
	}

	logger.Info("Caches are synced")

	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *FederateController) HasSynced() bool {
	return c.informerManager.HasSynced() && c.fedObjectInformer.Informer().HasSynced() &&
		c.clusterFedObjectInformer.Informer().HasSynced()
}

func (c *FederateController) reconcile(ctx context.Context, key workerKey) (status worker.Result) {
	c.metrics.Counter(metrics.FederateControllerThroughput, 1)
	ctx, logger := logging.InjectLoggerValues(ctx, "source-object", key.QualifiedName().String(), "gvk", key.gvk)

	startTime := time.Now()

	logger.V(3).Info("Start reconcile")
	defer func() {
		c.metrics.Duration(metrics.FederateControllerLatency, startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	ftc, exists := c.informerManager.GetResourceFTC(key.gvk)
	if !exists {
		// This could happen if:
		// 1) The InformerManager is not yet up-to-date.
		// 2) We received an event from a FederatedObject without a corresponding FTC.
		//
		// For case 1, when the InformerManager becomes up-to-date, all the source objects will be enqueued once anyway,
		// so it is safe to skip processing this time round. We do not have to process orphaned FederatedObjects.
		// For case 2, the federate controller does not have to process FederatedObjects without a corresponding FTC.
		return worker.StatusAllOK
	}

	sourceGVR := ftc.GetSourceTypeGVR()
	ctx, logger = logging.InjectLoggerValues(ctx, "ftc", ftc.Name, "gvr", sourceGVR)

	lister, hasSynced, exists := c.informerManager.GetResourceLister(key.gvk)
	if !exists {
		// Once again, this could happen if:
		// 1) The InformerManager is not yet up-to-date.
		// 2) We received an event from a FederatedObject without a corresponding FTC.
		//
		// See above comment for an explanation of the handling logic.
		return worker.StatusAllOK
	}
	if !hasSynced() {
		// If lister is not yet synced, simply reenqueue after a short delay.
		logger.V(3).Info("Lister for source type not yet synced, will reenqueue")
		return worker.Result{
			Success:      true,
			RequeueAfter: pointer.Duration(c.cacheSyncRateLimiter.When(key)),
		}
	}
	c.cacheSyncRateLimiter.Forget(key)

	sourceUns, err := lister.Get(key.QualifiedName().String())
	if err != nil && apierrors.IsNotFound(err) {
		logger.V(3).Info("No source object found, skip federating")
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get source object from store")
		return worker.StatusError
	}
	sourceObject := sourceUns.(*unstructured.Unstructured).DeepCopy()

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

	if fedObject != nil {
		var owner *metav1.OwnerReference
		ownerRefs := fedObject.GetOwnerReferences()
		for i, ref := range ownerRefs {
			if ref.Controller != nil && *ref.Controller {
				owner = &ownerRefs[i]
				break
			}
		}

		// To account for the very small chance of name collision, we verify the owner reference before proceeding.
		// Note that we allow the adoption of orphaned federated objects.
		if owner != nil &&
			(schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind) != key.gvk || owner.Name != sourceObject.GetName()) {
			logger.Error(nil, "Federated object not owned by source object, possible name collision detected")
			return worker.StatusErrorNoRetry
		}
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
				// If the federated object template is invalid, reenqueueing will not help solve the problem. Instead,
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
	}

	return worker.StatusAllOK
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
	needsUpdate, err := updateFederatedObjectForSourceObject(ftc, sourceObject, fedObject)
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
