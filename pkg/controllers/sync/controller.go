/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package sync

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/util/adoption"
	"github.com/kubewharf/kubeadmiral/pkg/util/cascadingdeletion"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/orphaning"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/dispatch"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/status"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	SyncControllerName = "sync-controller"
)

const (
	EventReasonWaitForCascadingDelete      = "WaitForCascadingDelete"
	EventReasonWaitForCascadingDeleteError = "WaitForCascadingDeleteError"
)

const (
	// If this finalizer is present on a federated resource, the sync
	// controller will have the opportunity to perform pre-deletion operations
	// (like deleting managed resources from member clusters).
	FinalizerSyncController = common.DefaultPrefix + "sync-controller"

	// If this finalizer is present on a cluster, the sync
	// controller will have the opportunity to perform per-deletion operations
	// (like deleting managed resources from member clusters).
	FinalizerCascadingDelete = common.DefaultPrefix + "cascading-delete"
)

// SyncController synchronizes the state of federated resources
// in the host cluster with resources in member clusters.
type SyncController struct {
	worker worker.ReconcileWorker[common.QualifiedName]

	// For handling cascading deletion.
	clusterCascadingDeletionWorker worker.ReconcileWorker[common.QualifiedName]

	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterReadinessTransitionQueue workqueue.DelayingInterface

	fedClient fedclient.Interface

	ftcManager         informermanager.FederatedTypeConfigManager
	fedInformerManager informermanager.FederatedInformerManager

	// For accessing FederatedResources (logical federated objects)
	fedAccessor FederatedResourceAccessor

	// For events
	eventRecorder record.EventRecorder

	clusterAvailableDelay         time.Duration
	clusterUnavailableDelay       time.Duration
	reconcileOnClusterChangeDelay time.Duration
	memberObjectEnqueueDelay      time.Duration
	recheckAfterDispatchDelay     time.Duration
	ensureDeletionRecheckDelay    time.Duration
	cascadingDeletionRecheckDelay time.Duration

	metrics stats.Metrics

	logger klog.Logger
}

// NewSyncController returns a new sync controller for the configuration
func NewSyncController(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,

	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,

	ftcManager informermanager.FederatedTypeConfigManager,
	fedInformerManager informermanager.FederatedInformerManager,

	fedSystemNamespace, targetNamespace string,
	clusterAvailableDelay, clusterUnavailableDelay time.Duration,

	logger klog.Logger,
	workerCount int,
	metrics stats.Metrics,
) (*SyncController, error) {
	recorder := eventsink.NewDefederatingRecorderMux(kubeClient, SyncControllerName, 4)
	logger = klog.LoggerWithValues(logger, "controller", SyncControllerName)
	s := &SyncController{
		fedClient:                     fedClient,
		ftcManager:                    ftcManager,
		fedInformerManager:            fedInformerManager,
		clusterAvailableDelay:         clusterAvailableDelay,
		clusterUnavailableDelay:       clusterUnavailableDelay,
		reconcileOnClusterChangeDelay: time.Second * 3,
		memberObjectEnqueueDelay:      time.Second * 10,
		recheckAfterDispatchDelay:     time.Second * 10,
		ensureDeletionRecheckDelay:    time.Second * 5,
		cascadingDeletionRecheckDelay: time.Second * 10,
		eventRecorder:                 recorder,
		metrics:                       metrics,
		logger:                        logger,
	}

	s.worker = worker.NewReconcileWorker[common.QualifiedName](
		SyncControllerName,
		nil,
		s.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	s.clusterCascadingDeletionWorker = worker.NewReconcileWorker[common.QualifiedName](
		SyncControllerName+"-cluster-cascading-deletion-worker",
		nil,
		s.reconcileClusterForCascadingDeletion,
		worker.RateLimiterOptions{},
		1,
		metrics,
	)

	// Build queue for triggering cluster reconciliations.
	s.clusterReadinessTransitionQueue = workqueue.NewNamedDelayingQueue("sync-controller-cluster-readiness-transition-queue")

	if err := s.fedInformerManager.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: informermanager.RegisterOncePredicate,
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			return eventhandlers.NewTriggerOnAllChanges(func(o pkgruntime.Object) {
				obj := o.(*unstructured.Unstructured)

				ftc, exists := s.ftcManager.GetResourceFTC(obj.GroupVersionKind())
				if !exists {
					return
				}

				federatedName := common.QualifiedName{
					Namespace: obj.GetNamespace(),
					Name:      naming.GenerateFederatedObjectName(obj.GetName(), ftc.GetName()),
				}
				s.worker.EnqueueWithDelay(federatedName, s.memberObjectEnqueueDelay)
			})
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler generator: %w", err)
	}

	if err := s.fedInformerManager.AddClusterEventHandlers(
		&informermanager.ClusterEventHandler{
			Predicate: func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				// Enqueue cluster when it's marked for deletion to ensure cascading deletion
				return oldCluster != nil && newCluster != nil &&
					oldCluster.GetDeletionTimestamp().IsZero() && !newCluster.GetDeletionTimestamp().IsZero()
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				s.clusterCascadingDeletionWorker.Enqueue(common.NewQualifiedName(cluster))
			},
		},
		&informermanager.ClusterEventHandler{
			Predicate: func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				// Reconcile all federated objects when cluster becomes available
				return oldCluster != nil && newCluster != nil &&
					!clusterutil.IsClusterReady(&oldCluster.Status) && clusterutil.IsClusterReady(&newCluster.Status)
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				s.clusterReadinessTransitionQueue.AddAfter(struct{}{}, s.clusterAvailableDelay)
			},
		},
		&informermanager.ClusterEventHandler{
			Predicate: func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				// Reconcile all federated objects when cluster becomes unavailable
				return oldCluster != nil && newCluster != nil &&
					clusterutil.IsClusterReady(&oldCluster.Status) && !clusterutil.IsClusterReady(&newCluster.Status)
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				s.clusterReadinessTransitionQueue.AddAfter(struct{}{}, s.clusterUnavailableDelay)
			},
		},
	); err != nil {
		return nil, fmt.Errorf("failed to add cluster event handler: %w", err)
	}

	s.fedAccessor = NewFederatedResourceAccessor(
		logger,
		fedSystemNamespace, targetNamespace,
		fedClient.CoreV1alpha1(),
		fedObjectInformer, clusterFedObjectInformer,
		ftcManager,
		func(qualifiedName common.QualifiedName) {
			s.worker.Enqueue(qualifiedName)
		},
		recorder,
	)

	return s, nil
}

func (s *SyncController) Run(ctx context.Context) {
	s.fedAccessor.Run(ctx)
	go func() {
		for {
			_, shutdown := s.clusterReadinessTransitionQueue.Get()
			if shutdown {
				break
			}
			s.enqueueAllObjects()
		}
	}()

	if !cache.WaitForNamedCacheSync(SyncControllerName, ctx.Done(), s.HasSynced) {
		return
	}

	s.worker.Run(ctx)
	s.clusterCascadingDeletionWorker.Run(ctx)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-ctx.Done()
		s.clusterReadinessTransitionQueue.ShutDown()
	}()
}

// Check whether all data stores are in sync. False is returned if any of the informer/stores is not yet
// synced with the corresponding api server.
func (s *SyncController) HasSynced() bool {
	if !s.fedInformerManager.HasSynced() {
		s.logger.V(3).Info("FederatedInformerManager not synced")
		return false
	}
	if !s.fedAccessor.HasSynced() {
		// The fed accessor will have logged why sync is not yet
		// complete.
		return false
	}

	return true
}

func (s *SyncController) IsControllerReady() bool {
	return s.HasSynced()
}

func (s *SyncController) getClusterClient(clusterName string) (dynamic.Interface, error) {
	if client, exists := s.fedInformerManager.GetClusterClient(clusterName); exists {
		return client, nil
	}
	return nil, fmt.Errorf("client does not exist for cluster")
}

// The function triggers reconciliation of all target federated resources.
func (s *SyncController) enqueueAllObjects() {
	s.logger.V(2).Info("Enqueuing all federated resources")
	s.fedAccessor.VisitFederatedResources(func(obj fedcorev1a1.GenericFederatedObject) {
		qualifiedName := common.NewQualifiedName(obj)
		s.worker.EnqueueWithDelay(qualifiedName, s.reconcileOnClusterChangeDelay)
	})
}

func (s *SyncController) reconcile(ctx context.Context, federatedName common.QualifiedName) (status worker.Result) {
	keyedLogger := s.logger.WithValues("federated-name", federatedName.String())
	ctx = klog.NewContext(ctx, keyedLogger)

	s.metrics.Rate("sync.throughput", 1)
	keyedLogger.V(3).Info("Starting to reconcile")
	startTime := time.Now()
	defer func() {
		s.metrics.Duration("sync.latency", startTime)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status).V(3).Info("Finished reconciling")
	}()

	fedResource, err := s.fedAccessor.FederatedResource(federatedName)
	if err != nil {
		keyedLogger.Error(err, "Failed to create FederatedResource helper")
		return worker.StatusError
	}
	if fedResource == nil {
		return worker.StatusAllOK
	}

	ctx, keyedLogger = logging.InjectLoggerValues(
		ctx,
		"target-name", fedResource.TargetName().String(),
		"gvk", fedResource.TargetGVK().String(),
	)

	if fedResource.Object().GetDeletionTimestamp() != nil {
		return s.ensureDeletion(ctx, fedResource)
	}

	pendingControllers, err := pendingcontrollers.GetPendingControllers(fedResource.Object())
	if err != nil {
		keyedLogger.Error(err, "Failed to get pending controllers")
		return worker.StatusError
	}
	if len(pendingControllers) > 0 {
		// upstream controllers have not finished processing, we wait for our turn
		return worker.StatusAllOK
	}

	err = s.ensureFinalizer(ctx, fedResource)
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		fedResource.RecordError("EnsureFinalizerError", errors.Wrap(err, "Failed to ensure finalizer"))
		return worker.StatusError
	}

	return s.syncToClusters(ctx, fedResource)
}

// syncToClusters ensures that the state of the given object is
// synchronized to member clusters.
func (s *SyncController) syncToClusters(ctx context.Context, fedResource FederatedResource) worker.Result {
	keyedLogger := klog.FromContext(ctx)

	clusters, err := s.fedInformerManager.GetJoinedClusters()
	if err != nil {
		fedResource.RecordError(
			string(fedcorev1a1.ClusterRetrievalFailed),
			errors.Wrap(err, "Failed to retrieve list of clusters"),
		)
		return s.setFederatedStatus(ctx, fedResource, fedcorev1a1.ClusterRetrievalFailed, nil)
	}

	selectedClusterNames := fedResource.ComputePlacement(clusters)
	keyedLogger.V(2).Info("Ensuring target object in clusters", "clusters", strings.Join(sets.List(selectedClusterNames), ","))

	skipAdoptingPreexistingResources := !adoption.ShouldAdoptPreexistingResources(fedResource.Object())
	dispatcher := dispatch.NewManagedDispatcher(
		s.getClusterClient,
		fedResource,
		skipAdoptingPreexistingResources,
		s.metrics,
	)

	shouldRecheckAfterDispatch := false
	for _, cluster := range clusters {
		clusterName := cluster.Name
		isSelectedCluster := selectedClusterNames.Has(clusterName)
		isCascadingDeletionTriggered := cluster.GetDeletionTimestamp() != nil && cascadingdeletion.IsCascadingDeleteEnabled(cluster)
		shouldBeDeleted := !isSelectedCluster || isCascadingDeletionTriggered

		if !clusterutil.IsClusterReady(&cluster.Status) {
			if !shouldBeDeleted {
				// Cluster state only needs to be reported in resource
				// status for clusters where the object should not be deleted.
				err := errors.New("Cluster not ready")
				dispatcher.RecordClusterError(fedcorev1a1.ClusterNotReady, clusterName, err)
			}
			continue
		}

		var clusterObj *unstructured.Unstructured
		{
			// TODO: updating the sync status may thrash the host apiserver if the host caches are synced but member caches are not synced.
			// Find out if this is ok.
			lister, hasSynced, exists := s.fedInformerManager.GetResourceLister(fedResource.TargetGVK(), clusterName)
			if !exists || !hasSynced() {
				wrappedErr := fmt.Errorf("cluster cache is not synced")
				dispatcher.RecordClusterError(fedcorev1a1.CachedRetrievalFailed, clusterName, wrappedErr)
				continue
			}

			clusterObjAny, err := lister.Get(fedResource.TargetName().String())
			if err != nil || clusterObjAny == nil {
				wrappedErr := fmt.Errorf("failed to get cluster object: %w", err)
				dispatcher.RecordClusterError(fedcorev1a1.CachedRetrievalFailed, clusterName, wrappedErr)
				continue
			}

			clusterObj = clusterObjAny.(*unstructured.Unstructured)
		}

		// Resource should not exist in the named cluster
		if shouldBeDeleted {
			if clusterObj == nil {
				// Resource does not exist in the cluster
				continue
			}
			if clusterObj.GetDeletionTimestamp() != nil {
				// Resource is marked for deletion
				dispatcher.RecordStatus(clusterName, fedcorev1a1.WaitingForRemoval)
				continue
			}
			if cluster.GetDeletionTimestamp() != nil && !cascadingdeletion.IsCascadingDeleteEnabled(cluster) {
				// If cluster is terminating and cascading-delete is disabled,
				// disallow deletion to preserve cluster object.
				// This could happen right after a cluster is deleted:
				// the scheduler observes the cluster deletion and removes
				// the placement, while the sync controller's informer is
				// lagging behind and observes a terminating cluster.
				continue
			}

			// We only respect orphaning behavior during cascading deletion, but not while migrating between clusters.
			s.deleteFromCluster(ctx, dispatcher, clusterName, fedResource, clusterObj, isCascadingDeletionTriggered)
			continue
		}

		// Resource should appear in the named cluster
		if cluster.GetDeletionTimestamp() != nil {
			// if the cluster is terminating, we should not sync
			dispatcher.RecordClusterError(
				fedcorev1a1.ClusterTerminating,
				clusterName,
				errors.New("Cluster terminating"),
			)
			continue
		}
		hasFinalizer, err := finalizersutil.HasFinalizer(cluster, FinalizerCascadingDelete)
		if err != nil {
			shouldRecheckAfterDispatch = true
			dispatcher.RecordClusterError(fedcorev1a1.FinalizerCheckFailed, clusterName, err)
			continue
		}
		if !hasFinalizer {
			// we should not sync before finalizer is added
			shouldRecheckAfterDispatch = true
			dispatcher.RecordClusterError(
				fedcorev1a1.FinalizerCheckFailed,
				clusterName,
				errors.Errorf("Missing cluster finalizer %s", FinalizerCascadingDelete),
			)
			continue
		}
		if clusterObj == nil {
			dispatcher.Create(ctx, clusterName)
		} else {
			dispatcher.Update(ctx, clusterName, clusterObj)
		}
	}

	dispatchOk, timeoutErr := dispatcher.Wait()
	if !dispatchOk {
		keyedLogger.Error(nil, "Failed to sync target object to cluster")
	}
	if timeoutErr != nil {
		fedResource.RecordError("OperationTimeoutError", timeoutErr)
		keyedLogger.Error(timeoutErr, "Sync to cluster timeout")
		return worker.StatusError
	}

	// Write updated versions to the API.
	updatedVersionMap := dispatcher.VersionMap()
	err = fedResource.UpdateVersions(sets.List(selectedClusterNames), updatedVersionMap)
	if err != nil {
		// Versioning of federated resources is an optimization to
		// avoid unnecessary updates, and failure to record version
		// information does not indicate a failure of propagation.
		keyedLogger.Error(err, "Failed to record version information")
	}

	collectedStatus := dispatcher.CollectedStatus()
	if reconcileStatus := s.setFederatedStatus(
		ctx,
		fedResource,
		fedcorev1a1.AggregateSuccess,
		&collectedStatus,
	); reconcileStatus != worker.StatusAllOK {
		return reconcileStatus
	}

	if !dispatchOk {
		return worker.StatusError
	}

	if shouldRecheckAfterDispatch {
		return worker.Result{RequeueAfter: &s.recheckAfterDispatchDelay}
	}

	return worker.StatusAllOK
}

func (s *SyncController) setFederatedStatus(
	ctx context.Context,
	fedResource FederatedResource,
	reason fedcorev1a1.FederatedObjectConditionReason,
	collectedStatus *status.CollectedPropagationStatus,
) worker.Result {
	if collectedStatus == nil {
		collectedStatus = &status.CollectedPropagationStatus{}
	}

	obj := fedResource.Object()
	keyedLogger := klog.FromContext(ctx)

	// If the underlying resource has changed, attempt to retrieve and
	// update it repeatedly.
	err := wait.PollImmediateWithContext(ctx, 1*time.Second, 5*time.Second, func(ctx context.Context) (bool, error) {
		if updateRequired := status.SetFederatedStatus(obj, reason, *collectedStatus); !updateRequired {
			keyedLogger.V(4).Info("No status update necessary")
			return true, nil
		}

		var err error
		obj, err = fedobjectadapters.UpdateStatus(ctx, s.fedClient.CoreV1alpha1(), obj, metav1.UpdateOptions{})
		if err == nil {
			fedResource.SetObject(obj)
			return true, nil
		}
		if apierrors.IsConflict(err) {
			obj, err = fedobjectadapters.Get(ctx, s.fedClient.CoreV1alpha1(), obj.GetNamespace(), obj.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, errors.Wrapf(err, "failed to retrieve resource")
			}
			return false, nil
		}
		return false, errors.Wrapf(err, "failed to update resource")
	})
	if err != nil {
		keyedLogger.Error(err, "Failed to set propagation status")
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (s *SyncController) ensureDeletion(ctx context.Context, fedResource FederatedResource) worker.Result {
	fedResource.DeleteVersions()

	keyedLogger := klog.FromContext(ctx)
	keyedLogger.V(2).Info("Ensuring deletion of federated object")

	obj := fedResource.Object()

	finalizers := sets.NewString(obj.GetFinalizers()...)
	if !finalizers.Has(FinalizerSyncController) {
		keyedLogger.V(3).Info("Federated object does not have the finalizer. Nothing to do", "finalizer-name", FinalizerSyncController)
		return worker.StatusAllOK
	}

	keyedLogger.V(2).Info("Deleting resources managed by this federated object from member clusters")
	recheckRequired, err := s.deleteFromClusters(ctx, fedResource)
	if err != nil {
		fedResource.RecordError(string(fedcorev1a1.EnsureDeletionFailed), err)
		keyedLogger.Error(err, "Failed to ensure deletion of member objects")
		return worker.StatusError
	}
	if recheckRequired {
		return worker.Result{RequeueAfter: &s.ensureDeletionRecheckDelay}
	}
	if err := s.removeFinalizer(ctx, fedResource); err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		keyedLogger.Error(err, "Failed to remove finalizer from the federated object", "finalizer-name", FinalizerSyncController)
		return worker.StatusError
	}
	return worker.StatusAllOK
}

func (s *SyncController) deleteFromCluster(
	ctx context.Context,
	dispatcher dispatch.UnmanagedDispatcher,
	clusterName string,
	fedResource FederatedResource,
	clusterObj *unstructured.Unstructured,
	respectOrphaningBehavior bool,
) {
	if !respectOrphaningBehavior {
		dispatcher.Delete(ctx, clusterName, clusterObj)
		return
	}

	keyedLogger := klog.FromContext(ctx)
	// Respect orphaning behavior
	orphaningBehavior := orphaning.GetOrphaningBehavior(fedResource.Object())
	shouldBeOrphaned := orphaningBehavior == orphaning.OrphanManagedResourcesAll ||
		orphaningBehavior == orphaning.OrphanManagedResourcesAdopted && adoption.HasAdoptedAnnotation(clusterObj)
	if shouldBeOrphaned {
		keyedLogger.WithValues("cluster-name", clusterName).
			V(2).Info("Cluster object is going to be orphaned")
		dispatcher.RemoveManagedLabel(ctx, clusterName, clusterObj)
	} else {
		dispatcher.Delete(ctx, clusterName, clusterObj)
	}
}

func (s *SyncController) deleteFromClusters(ctx context.Context, fedResource FederatedResource) (bool, error) {
	gvk := fedResource.TargetGVK()
	qualifiedName := fedResource.TargetName()
	keyedLogger := klog.FromContext(ctx)

	remainingClusters := []string{}
	ok, err := s.handleDeletionInClusters(
		ctx,
		gvk,
		qualifiedName,
		func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured) {
			remainingClusters = append(remainingClusters, clusterName)
			s.deleteFromCluster(ctx, dispatcher, clusterName, fedResource, clusterObj, true)
		},
	)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errors.Errorf("failed to remove managed resources from one or more clusters.")
	}
	if len(remainingClusters) > 0 {
		keyedLogger.WithValues("clusters", strings.Join(remainingClusters, ", ")).
			V(2).Info("Waiting for resources managed by this federated object to be removed from some clusters")
		return true, nil
	}
	err = s.ensureRemovedOrUnmanaged(ctx, fedResource)
	if err != nil {
		return false, errors.Wrapf(err, "failed to verify that managed resources no longer exist in any cluster")
	}

	// Managed resources no longer exist in any member cluster
	return false, nil
}

// ensureRemovedOrUnmanaged ensures that no resources in member
// clusters that could be managed by the given federated resources are
// present or labeled as managed.  The checks are performed without
// the informer to cover the possibility that the resources have not
// yet been cached.
func (s *SyncController) ensureRemovedOrUnmanaged(ctx context.Context, fedResource FederatedResource) error {
	clusters, err := s.fedInformerManager.GetJoinedClusters()
	if err != nil {
		return errors.Wrap(err, "failed to get a list of clusters")
	}

	dispatcher := dispatch.NewCheckUnmanagedDispatcher(
		s.getClusterClient,
		fedResource.TargetGVR(),
		fedResource.TargetName(),
	)
	unreadyClusters := []string{}
	for _, cluster := range clusters {
		if !clusterutil.IsClusterReady(&cluster.Status) {
			unreadyClusters = append(unreadyClusters, cluster.Name)
			continue
		}
		dispatcher.CheckRemovedOrUnlabeled(ctx, cluster.Name)
	}
	ok, timeoutErr := dispatcher.Wait()
	if timeoutErr != nil {
		return timeoutErr
	}
	if len(unreadyClusters) > 0 {
		return errors.Errorf("the following clusters were not ready: %s", strings.Join(unreadyClusters, ", "))
	}
	if !ok {
		return errors.Errorf("one or more checks failed")
	}
	return nil
}

// handleDeletionInClusters invokes the provided deletion handler for
// each managed resource in member clusters.
func (s *SyncController) handleDeletionInClusters(
	ctx context.Context,
	targetGVK schema.GroupVersionKind,
	targetQualifiedName common.QualifiedName,
	deletionFunc func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured),
) (bool, error) {
	keyedLogger := klog.FromContext(ctx)

	clusters, err := s.fedInformerManager.GetJoinedClusters()
	if err != nil {
		return false, fmt.Errorf("failed to get a list of clusters: %w", err)
	}

	ftc, exists := s.ftcManager.GetResourceFTC(targetGVK)
	if !exists {
		return false, fmt.Errorf("FTC does not exist")
	}

	dispatcher := dispatch.NewUnmanagedDispatcher(s.getClusterClient, ftc.GetSourceTypeGVR(), targetQualifiedName)
	retrievalFailureClusters := []string{}
	unreadyClusters := []string{}
	for _, cluster := range clusters {
		clusterName := cluster.Name

		if !clusterutil.IsClusterReady(&cluster.Status) {
			unreadyClusters = append(unreadyClusters, clusterName)
			continue
		}

		clusterObj, _, err := informermanager.GetClusterObject(
			ctx,
			s.ftcManager,
			s.fedInformerManager,
			clusterName,
			targetQualifiedName,
			targetGVK,
		)
		if err != nil {
			keyedLogger.WithValues("cluster-name", clusterName).
				Error(err, "Failed to retrieve object in cluster")
			retrievalFailureClusters = append(retrievalFailureClusters, clusterName)
			continue
		}
		if clusterObj == nil {
			continue
		}

		deletionFunc(dispatcher, clusterName, clusterObj)
	}
	ok, timeoutErr := dispatcher.Wait()
	if timeoutErr != nil {
		return false, timeoutErr
	}
	if len(retrievalFailureClusters) > 0 {
		return false, errors.Errorf(
			"failed to retrieve a managed resource for the following cluster(s): %s",
			strings.Join(retrievalFailureClusters, ", "),
		)
	}
	if len(unreadyClusters) > 0 {
		return false, errors.Errorf("the following clusters were not ready: %s", strings.Join(unreadyClusters, ", "))
	}
	return ok, nil
}

func (s *SyncController) ensureFinalizer(ctx context.Context, fedResource FederatedResource) error {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "finalizer-name", FinalizerSyncController)

	obj := fedResource.Object()
	isUpdated, err := finalizersutil.AddFinalizers(obj, sets.NewString(FinalizerSyncController))
	if err != nil || !isUpdated {
		return err
	}

	keyedLogger.V(1).Info("Adding finalizer to federated object")
	updatedObj, err := fedobjectadapters.Update(
		ctx,
		s.fedClient.CoreV1alpha1(),
		obj,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return err
	}

	fedResource.SetObject(updatedObj)
	return nil
}

func (s *SyncController) removeFinalizer(ctx context.Context, fedResource FederatedResource) error {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "finalizer-name", FinalizerSyncController)

	obj := fedResource.Object()
	isUpdated, err := finalizersutil.RemoveFinalizers(obj, sets.NewString(FinalizerSyncController))
	if err != nil || !isUpdated {
		return err
	}

	keyedLogger.V(1).Info("Removing finalizer from federated object")
	updatedObj, err := fedobjectadapters.Update(
		ctx,
		s.fedClient.CoreV1alpha1(),
		obj,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return err
	}

	fedResource.SetObject(updatedObj)
	return nil
}

func (s *SyncController) ensureClusterFinalizer(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) error {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "finalizer-name", FinalizerCascadingDelete)
	keyedLogger.V(1).Info("Adding finalizer to cluster")
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var err error
		cluster, err = s.fedClient.CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{ResourceVersion: "0"})
		if err != nil {
			return err
		}
		isUpdated, err := finalizersutil.AddFinalizers(cluster, sets.NewString(FinalizerCascadingDelete))
		if err != nil || !isUpdated {
			return err
		}
		cluster, err = s.fedClient.CoreV1alpha1().FederatedClusters().Update(ctx, cluster, metav1.UpdateOptions{})
		return err
	}); err != nil {
		keyedLogger.Error(err, "Failed to ensure cluster finalizer")
		return err
	}
	return nil
}

func (s *SyncController) removeClusterFinalizer(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) error {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "finalizer-name", FinalizerCascadingDelete)
	keyedLogger.V(1).Info("Removing finalizer from cluster")
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var err error
		cluster, err = s.fedClient.CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{ResourceVersion: "0"})
		if err != nil {
			return err
		}
		isUpdated, err := finalizersutil.RemoveFinalizers(cluster, sets.NewString(FinalizerCascadingDelete))
		if err != nil || !isUpdated {
			return err
		}
		cluster, err = s.fedClient.CoreV1alpha1().FederatedClusters().Update(ctx, cluster, metav1.UpdateOptions{})
		return err
	}); err != nil {
		keyedLogger.Error(err, "Failed to remove cluster finalizer")
		return err
	}
	return nil
}

func (s *SyncController) reconcileClusterForCascadingDeletion(ctx context.Context, qualifiedName common.QualifiedName) worker.Result {
	logger := s.logger.WithValues("cluster-name", qualifiedName.String(), "process", "cluster-cascading-deletion")
	ctx = klog.NewContext(ctx, logger)
	logger.V(3).Info("Starting to reconcile cluster for cascading deletion")

	clusterLister := s.fedInformerManager.GetFederatedClusterLister()
	cluster, err := clusterLister.Get(qualifiedName.Name)
	if apierrors.IsNotFound(err) {
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get federated cluster")
		return worker.StatusError
	}

	cluster = cluster.DeepCopy()
	if cluster.DeletionTimestamp == nil {
		// cluster is not yet terminating, ensure it has cascading-delete finalizer
		err := s.ensureClusterFinalizer(ctx, cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if !clusterutil.IsClusterJoined(&cluster.Status) || !cascadingdeletion.IsCascadingDeleteEnabled(cluster) {
		// cascading-delete is not required, remove cascading-delete finalizer immediately
		err := s.removeClusterFinalizer(ctx, cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}

		return worker.StatusAllOK
	}

	// cascading-delete is enabled, wait for member objects to be deleted
	ftcLister := s.ftcManager.GetFederatedTypeConfigLister()
	ftcs, err := ftcLister.List(labels.Everything())
	if err != nil {
		logger.Error(err, "failed to get ftc lister")
		return worker.StatusError
	}

	remainingByGVK := make(map[string]string, len(ftcs))
	for _, ftc := range ftcs {
		gvk := ftc.GetSourceTypeGVK().String()
		resourceLister, hasSynced, exists := s.fedInformerManager.GetResourceLister(ftc.GetSourceTypeGVK(), cluster.Name)
		if !exists {
			continue
		}

		// If cluster cache is synced, we check the store.
		// Otherwise, we will have to issue a list request.
		if hasSynced() {
			objects, err := resourceLister.List(labels.Everything())
			if err != nil {
				remainingByGVK[gvk] = fmt.Sprintf("Unknown (failed to list from cluster lister: %w)", err)
			} else if len(objects) > 0 {
				remainingByGVK[gvk] = strconv.Itoa(len(objects))
			}
		} else {
			client, exists := s.fedInformerManager.GetClusterClient(cluster.Name)
			if !exists {
				remainingByGVK[gvk] = "Unknown (cluster client does not exist)"
				continue
			}

			objects, err := client.Resource(ftc.GetSourceTypeGVR()).Namespace(corev1.NamespaceAll).List(
				ctx, metav1.ListOptions{
					Limit: 1,
					LabelSelector: labels.SelectorFromSet(labels.Set{
						managedlabel.ManagedByKubeAdmiralLabelKey: managedlabel.ManagedByKubeAdmiralLabelValue,
					}).String(),
				},
			)
			if err == nil && len(objects.Items) > 0 {
				remainingByGVK[gvk] = strconv.Itoa(len(objects.Items))
			} else if err != nil && !meta.IsNoMatchError(err) && !apierrors.IsNotFound(err) {
				remainingByGVK[gvk] = fmt.Sprintf("Unknown (failed to list from cluster: %v)", err)
			}
		}
	}

	if len(remainingByGVK) > 0 {
		s.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeNormal,
			EventReasonWaitForCascadingDelete,
			"waiting for cascading delete: %v",
			remainingByGVK,
		)
		return worker.Result{RequeueAfter: &s.cascadingDeletionRecheckDelay}
	}

	// The CRD may be deleted before the CR, and then the member cluster will be stuck
	// in the cascade deletion because the api no longer exists. So we need to ignore
	// the NoMatch and NotFound error.
	// Whether to return NoMatch or NotFound depends on whether the client has visited CR,
	// if so, returns NotFound (because the client has a scheme cache), otherwise returns NoMatch.
	if err != nil && !(meta.IsNoMatchError(err) || apierrors.IsNotFound(err)) {
		logger.Error(err, "Failed to list target objects from cluster")
		return worker.StatusError
	}

	// all member objects are deleted
	err = s.removeClusterFinalizer(ctx, cluster)
	if err != nil {
		logger.Error(err, "Failed to remove cluster finalizer")
		return worker.StatusError
	}

	return worker.StatusAllOK
}
