//go:build exclude
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
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/dispatch"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/status"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/history"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/managedlabel"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	EventReasonWaitForCascadingDelete      = "WaitForCascadingDelete"
	EventReasonWaitForCascadingDeleteError = "WaitForCascadingDeleteError"
	SyncControllerName                     = "sync-controller"
)

const (
	// If this finalizer is present on a federated resource, the sync
	// controller will have the opportunity to perform pre-deletion operations
	// (like deleting managed resources from member clusters).
	FinalizerSyncController = common.DefaultPrefix + "sync-controller"

	// If this finalizer is present on a cluster, the sync
	// controller will have the opportunity to perform per-deletion operations
	// (like deleting managed resources from member clusters).
	FinalizerCascadingDeletePrefix = common.DefaultPrefix + "cascading-delete"
)

// SyncController synchronizes the state of federated resources
// in the host cluster with resources in member clusters.
type SyncController struct {
	name string

	worker        worker.ReconcileWorker
	clusterWorker worker.ReconcileWorker

	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterQueue workqueue.DelayingInterface

	// Informer for resources in member clusters
	informer util.FederatedInformer

	// For events
	eventRecorder record.EventRecorder

	clusterAvailableDelay         time.Duration
	clusterUnavailableDelay       time.Duration
	reconcileOnClusterChangeDelay time.Duration
	memberObjectEnqueueDelay      time.Duration
	recheckAfterDispatchDelay     time.Duration
	ensureDeletionRecheckDelay    time.Duration
	cascadingDeletionRecheckDelay time.Duration

	typeConfig *fedcorev1a1.FederatedTypeConfig

	fedAccessor FederatedResourceAccessor

	hostClusterClient genericclient.Client

	controllerHistory history.Interface

	controllerRevisionStore cache.Store

	controllerRevisionController cache.Controller

	revListerSynced cache.InformerSynced

	limitedScope bool

	cascadingDeleteFinalizer string

	metrics stats.Metrics

	logger klog.Logger
}

// StartSyncController starts a new sync controller for a type config
func StartSyncController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedNamespaceAPIResource *metav1.APIResource,
	controllerRevisionStore cache.Store,
	controllerRevisionController cache.Controller,
) error {
	controller, err := newSyncController(
		controllerConfig,
		typeConfig,
		fedNamespaceAPIResource,
		controllerRevisionStore,
		controllerRevisionController,
	)
	if err != nil {
		return err
	}
	if controllerConfig.MinimizeLatency {
		controller.minimizeLatency()
	}
	controller.logger.Info("Starting sync controller")
	controller.Run(stopChan)
	return nil
}

// newSyncController returns a new sync controller for the configuration
func newSyncController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedNamespaceAPIResource *metav1.APIResource,
	controllerRevisionStore cache.Store,
	controllerRevisionController cache.Controller,
) (*SyncController, error) {
	federatedTypeAPIResource := typeConfig.GetFederatedType()
	userAgent := fmt.Sprintf("%s-federate-sync-controller", strings.ToLower(federatedTypeAPIResource.Kind))

	// Initialize non-dynamic clients first to avoid polluting config
	client := genericclient.NewForConfigOrDieWithUserAgent(controllerConfig.KubeConfig, userAgent)
	kubeClient := kubeclient.NewForConfigOrDie(controllerConfig.KubeConfig)

	configCopy := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configCopy, userAgent)

	recorder := eventsink.NewDefederatingRecorderMux(kubeClient, userAgent, 4)
	logger := klog.LoggerWithValues(klog.Background(), "controller", SyncControllerName, "ftc", typeConfig.Name)
	s := &SyncController{
		name:                          userAgent,
		clusterAvailableDelay:         controllerConfig.ClusterAvailableDelay,
		clusterUnavailableDelay:       controllerConfig.ClusterUnavailableDelay,
		reconcileOnClusterChangeDelay: time.Second * 3,
		memberObjectEnqueueDelay:      time.Second * 10,
		recheckAfterDispatchDelay:     time.Second * 10,
		ensureDeletionRecheckDelay:    time.Second * 5,
		cascadingDeletionRecheckDelay: time.Second * 10,
		eventRecorder:                 recorder,
		typeConfig:                    typeConfig,
		hostClusterClient:             client,
		limitedScope:                  controllerConfig.LimitedScope(),
		controllerRevisionStore:       controllerRevisionStore,
		controllerRevisionController:  controllerRevisionController,
		metrics:                       controllerConfig.Metrics,
		logger:                        logger,
	}

	hash := fnv.New32()
	_, err := hash.Write([]byte(s.typeConfig.GetObjectMeta().Name))
	if err != nil {
		return nil, errors.Wrapf(
			err,
			"failed to generate cascading-delete finalizer for ftc %s",
			s.typeConfig.GetObjectMeta().Name,
		)
	}
	ftcNameTruncated := s.typeConfig.GetObjectMeta().Name
	if len(ftcNameTruncated) > 20 {
		ftcNameTruncated = ftcNameTruncated[:20]
	}
	s.cascadingDeleteFinalizer = fmt.Sprintf("%s-%s-%d", FinalizerCascadingDeletePrefix, ftcNameTruncated, hash.Sum32())

	s.worker = worker.NewReconcileWorker(
		s.reconcile,
		worker.RateLimiterOptions{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		deliverutil.NewMetricTags("sync-worker", typeConfig.GetTargetType().Kind),
	)

	// TODO: do we need both clusterWorker and clusterQueue?
	s.clusterWorker = worker.NewReconcileWorker(s.reconcileCluster, worker.RateLimiterOptions{}, 1, controllerConfig.Metrics,
		deliverutil.NewMetricTags("sync-cluster-worker", typeConfig.GetTargetType().Kind))

	// Build queue for triggering cluster reconciliations.
	s.clusterQueue = workqueue.NewNamedDelayingQueue("sync-controller-cluster-queue")

	targetAPIResource := typeConfig.GetTargetType()

	// Federated informer for resources in member clusters
	s.informer, err = util.NewFederatedInformer(
		controllerConfig,
		client,
		configCopy,
		&targetAPIResource,
		func(obj pkgruntime.Object) {
			qualifiedName := common.NewQualifiedName(obj)
			s.worker.EnqueueWithDelay(qualifiedName, s.memberObjectEnqueueDelay)
		},
		&util.ClusterLifecycleHandlerFuncs{
			ClusterAvailable: func(cluster *fedcorev1a1.FederatedCluster) {
				// When new cluster becomes available process all the target resources again.
				s.clusterWorker.EnqueueObject(cluster)
				s.clusterQueue.AddAfter(struct{}{}, s.clusterAvailableDelay)
			},
			// When a cluster becomes unavailable process all the target resources again.
			ClusterUnavailable: func(cluster *fedcorev1a1.FederatedCluster, _ []interface{}) {
				s.clusterWorker.EnqueueObject(cluster)
				s.clusterQueue.AddAfter(struct{}{}, s.clusterUnavailableDelay)
			},
		},
	)
	if err != nil {
		return nil, err
	}

	s.fedAccessor, err = NewFederatedResourceAccessor(
		logger, controllerConfig, typeConfig, fedNamespaceAPIResource,
		client, s.worker.EnqueueObject, recorder)
	if err != nil {
		return nil, err
	}

	if typeConfig.GetRevisionHistoryEnabled() {
		s.controllerHistory = history.NewHistory(kubeClient, controllerRevisionStore)
		s.revListerSynced = controllerRevisionController.HasSynced
	}

	return s, nil
}

// minimizeLatency reduces delays and timeouts to make the controller more responsive (useful for testing).
func (s *SyncController) minimizeLatency() {
	s.clusterAvailableDelay = time.Second
	s.clusterUnavailableDelay = time.Second
	s.reconcileOnClusterChangeDelay = 20 * time.Millisecond
	s.memberObjectEnqueueDelay = 50 * time.Millisecond
	s.recheckAfterDispatchDelay = 2 * time.Second
	s.ensureDeletionRecheckDelay = 2 * time.Second
	s.cascadingDeletionRecheckDelay = 3 * time.Second
}

func (s *SyncController) Run(stopChan <-chan struct{}) {
	s.fedAccessor.Run(stopChan)
	s.informer.Start()
	go func() {
		for {
			_, shutdown := s.clusterQueue.Get()
			if shutdown {
				break
			}
			s.reconcileOnClusterChange()
		}
	}()

	if !cache.WaitForNamedCacheSync(s.name, stopChan, s.HasSynced) {
		return
	}

	s.worker.Run(stopChan)
	s.clusterWorker.Run(stopChan)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		s.informer.Stop()
		s.clusterQueue.ShutDown()
	}()
}

// Check whether all data stores are in sync. False is returned if any of the informer/stores is not yet
// synced with the corresponding api server.
func (s *SyncController) HasSynced() bool {
	if !s.informer.ClustersSynced() {
		s.logger.V(3).Info("Cluster list not synced")
		return false
	}
	if !s.fedAccessor.HasSynced() {
		// The fed accessor will have logged why sync is not yet
		// complete.
		return false
	}

	if s.typeConfig.GetRevisionHistoryEnabled() && !s.revListerSynced() {
		s.logger.V(3).Info("ControllerRevision list not synced")
		return false
	}

	return true
}

// The function triggers reconciliation of all target federated resources.
func (s *SyncController) reconcileOnClusterChange() {
	s.fedAccessor.VisitFederatedResources(func(obj interface{}) {
		qualifiedName := common.NewQualifiedName(obj.(pkgruntime.Object))
		s.worker.EnqueueWithDelay(qualifiedName, s.reconcileOnClusterChangeDelay)
	})
}

func (s *SyncController) reconcile(qualifiedName common.QualifiedName) (status worker.Result) {
	key := qualifiedName.String()
	keyedLogger := s.logger.WithValues("object", key)
	ctx := klog.NewContext(context.TODO(), keyedLogger)
	fedResource, possibleOrphan, err := s.fedAccessor.FederatedResource(qualifiedName)
	if err != nil {
		keyedLogger.Error(err, "Failed to create FederatedResource helper")
		return worker.StatusError
	}
	if possibleOrphan {
		apiResource := s.typeConfig.GetTargetType()
		gvk := schemautil.APIResourceToGVK(&apiResource)
		keyedLogger.WithValues("label", managedlabel.ManagedByKubeAdmiralLabelKey).
			V(2).Info("Ensuring the removal of the label in member clusters")
		err = s.removeManagedLabel(ctx, gvk, qualifiedName)
		if err != nil {
			keyedLogger.WithValues("label", managedlabel.ManagedByKubeAdmiralLabelKey).
				Error(err, "Failed to remove the label from object in member clusters")
			return worker.StatusError
		}

		return worker.StatusAllOK
	}
	if fedResource == nil {
		return worker.StatusAllOK
	}

	s.metrics.Rate("sync.throughput", 1)
	keyedLogger.V(3).Info("Starting to reconcile")
	startTime := time.Now()
	defer func() {
		s.metrics.Duration("sync.latency", startTime)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status).V(3).Info("Finished reconciling")
	}()

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

	var lastRevisionNameWithHash, currentRevisionName string
	collisionCount := fedResource.CollisionCount()
	if s.typeConfig.GetRevisionHistoryEnabled() {
		keyedLogger.V(2).Info("Starting to sync revisions")
		collisionCount, lastRevisionNameWithHash, currentRevisionName, err = s.syncRevisions(ctx, fedResource)
		if err != nil {
			keyedLogger.Error(err, "Failed to sync revisions")
			fedResource.RecordError("SyncRevisionHistoryError", errors.Wrap(err, "Failed to sync revisions"))
			return worker.StatusError
		}
	}
	err = s.ensureAnnotations(ctx, fedResource, lastRevisionNameWithHash, currentRevisionName)
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		keyedLogger.Error(err, "Failed to ensure annotations")
		fedResource.RecordError("EnsureAnnotationsErr", errors.Wrap(err, "Failed to ensure annotations"))
		return worker.StatusError
	}

	return s.syncToClusters(ctx, fedResource, collisionCount)
}

// syncToClusters ensures that the state of the given object is
// synchronized to member clusters.
func (s *SyncController) syncToClusters(ctx context.Context, fedResource FederatedResource, collisionCount *int32) worker.Result {
	keyedLogger := klog.FromContext(ctx)

	clusters, err := s.informer.GetJoinedClusters()
	if err != nil {
		fedResource.RecordError(
			string(fedtypesv1a1.ClusterRetrievalFailed),
			errors.Wrap(err, "Failed to retrieve list of clusters"),
		)
		return s.setFederatedStatus(ctx, fedResource, collisionCount, fedtypesv1a1.ClusterRetrievalFailed, nil)
	}

	selectedClusterNames, err := fedResource.ComputePlacement(clusters)
	if err != nil {
		fedResource.RecordError(
			string(fedtypesv1a1.ComputePlacementFailed),
			errors.Wrap(err, "Failed to compute placement"),
		)
		return s.setFederatedStatus(ctx, fedResource, collisionCount, fedtypesv1a1.ComputePlacementFailed, nil)
	}

	keyedLogger.WithValues("clusters", strings.Join(selectedClusterNames.List(), ",")).
		V(2).Info("Ensuring target object in clusters")

	skipAdoptingPreexistingResources := !util.ShouldAdoptPreexistingResources(fedResource.Object())
	dispatcher := dispatch.NewManagedDispatcher(
		s.informer.GetClientForCluster,
		fedResource,
		skipAdoptingPreexistingResources,
		s.metrics,
	)

	shouldRecheckAfterDispatch := false
	for _, cluster := range clusters {
		clusterName := cluster.Name
		isSelectedCluster := selectedClusterNames.Has(clusterName)
		isCascadingDeletionTriggered := cluster.GetDeletionTimestamp() != nil && util.IsCascadingDeleteEnabled(cluster)
		shouldBeDeleted := !isSelectedCluster || isCascadingDeletionTriggered

		if !util.IsClusterReady(&cluster.Status) {
			if !shouldBeDeleted {
				// Cluster state only needs to be reported in resource
				// status for clusters where the object should not be deleted.
				err := errors.New("Cluster not ready")
				dispatcher.RecordClusterError(fedtypesv1a1.ClusterNotReady, clusterName, err)
			}
			continue
		}

		clusterObj, _, err := util.GetClusterObject(
			ctx,
			s.informer,
			clusterName,
			fedResource.TargetName(),
			s.typeConfig.GetTargetType(),
		)
		if err != nil {
			wrappedErr := errors.Wrap(err, "failed to get cluster object")
			dispatcher.RecordClusterError(fedtypesv1a1.CachedRetrievalFailed, clusterName, wrappedErr)
			continue
		}

		// Resource should not exist in the named cluster
		if shouldBeDeleted {
			if clusterObj == nil {
				// Resource does not exist in the cluster
				continue
			}
			if clusterObj.GetDeletionTimestamp() != nil {
				// Resource is marked for deletion
				dispatcher.RecordStatus(clusterName, fedtypesv1a1.WaitingForRemoval)
				continue
			}
			if cluster.GetDeletionTimestamp() != nil && !util.IsCascadingDeleteEnabled(cluster) {
				// If cluster is terminating and cascading-delete is disabled,
				// disallow deletion to preserve cluster object.
				// This could happen right after a cluster is deleted:
				// the scheduler observes the cluster deletion and removes
				// the placement, while the sync controller's informer is
				// lagging behind and sees a terminating cluster.
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
				fedtypesv1a1.ClusterTerminating,
				clusterName,
				errors.New("Cluster terminating"),
			)
			continue
		}
		hasFinalizer, err := finalizersutil.HasFinalizer(cluster, s.cascadingDeleteFinalizer)
		if err != nil {
			shouldRecheckAfterDispatch = true
			dispatcher.RecordClusterError(fedtypesv1a1.FinalizerCheckFailed, clusterName, err)
			continue
		}
		if !hasFinalizer {
			// we should not sync before finalizer is added
			shouldRecheckAfterDispatch = true
			dispatcher.RecordClusterError(
				fedtypesv1a1.FinalizerCheckFailed,
				clusterName,
				errors.Errorf("Missing cluster finalizer %s", s.cascadingDeleteFinalizer),
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

	if dispatchOk {
		err := s.updateSyncSuccessAnnotations(ctx, fedResource)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}
	}

	// Write updated versions to the API.
	updatedVersionMap := dispatcher.VersionMap()
	err = fedResource.UpdateVersions(selectedClusterNames.List(), updatedVersionMap)
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
		collisionCount,
		fedtypesv1a1.AggregateSuccess,
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

func (s *SyncController) updateSyncSuccessAnnotations(ctx context.Context, fedResource FederatedResource) error {
	// Update SyncSuccessTimestamp annotation to federated resource.
	obj := fedResource.Object()
	annotations := obj.GetAnnotations()
	generation := obj.GetGeneration()
	updateAnnotation := true
	federatedKeyLogger := klog.FromContext(ctx)

	if v, ok := annotations[annotationutil.LastSyncSuccessGeneration]; ok {
		if strconv.FormatInt(generation, 10) == v {
			updateAnnotation = false
		}
	}

	if updateAnnotation {
		_, err := annotationutil.AddAnnotation(
			obj,
			annotationutil.LastSyncSuccessGeneration,
			strconv.FormatInt(generation, 10),
		)
		if err != nil {
			return err
		}

		syncSuccessTimestamp := metav1.Now().UTC().Format(time.RFC3339Nano)
		_, err = annotationutil.AddAnnotation(obj, annotationutil.SyncSuccessTimestamp, syncSuccessTimestamp)
		if err != nil {
			return err
		}

		err = s.hostClusterClient.Update(ctx, obj)
		if err != nil {
			federatedKeyLogger.Error(err, "Failed to update syncSuccessTimestamp annotation of federated object")
			return err
		}
	}
	return nil
}

func (s *SyncController) setFederatedStatus(ctx context.Context, fedResource FederatedResource, collisionCount *int32,
	reason fedtypesv1a1.AggregateReason, collectedStatus *status.CollectedPropagationStatus,
) worker.Result {
	if collectedStatus == nil {
		collectedStatus = &status.CollectedPropagationStatus{}
	}

	obj := fedResource.Object()
	keyedLogger := klog.FromContext(ctx)

	// Only a single reason for propagation failure is reported at any one time, so only report
	// NamespaceNotFederated if no other explicit error has been indicated.
	if reason == fedtypesv1a1.AggregateSuccess {
		// For a cluster-scoped control plane, report when the containing namespace of a federated
		// resource is not federated.  The KubeAdmiral system namespace is implicitly federated in a
		// namespace-scoped control plane.
		if !s.limitedScope && fedResource.NamespaceNotFederated() {
			reason = fedtypesv1a1.NamespaceNotFederated
		}
	}

	// If the underlying resource has changed, attempt to retrieve and
	// update it repeatedly.
	err := wait.PollImmediate(1*time.Second, 5*time.Second, func() (bool, error) {
		if updateRequired, err := status.SetFederatedStatus(obj, collisionCount, reason, *collectedStatus); err != nil {
			return false, errors.Wrapf(err, "failed to set the status")
		} else if !updateRequired {
			keyedLogger.V(4).Info("No status update necessary")
			return true, nil
		}

		err := s.hostClusterClient.UpdateStatus(context.TODO(), obj)
		if err == nil {
			return true, nil
		}
		if apierrors.IsConflict(err) {
			err := s.hostClusterClient.Get(context.TODO(), obj, obj.GetNamespace(), obj.GetName())
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

	// UpdateStatus does not read the annotations, only the status field.
	// Update reads the annotations, but it will bump the generation if status is changed.
	// Therefore, we have to separate status update and annotation update into two separate calls.

	err = wait.PollImmediate(1*time.Second, 5*time.Second, func() (bool, error) {
		changed := false
		err := sourcefeedback.PopulateSyncingAnnotation(obj, collectedStatus.StatusMap, &changed)
		if err != nil {
			return false, err
		}

		if !changed {
			return true, nil
		}

		err = s.hostClusterClient.Update(context.TODO(), obj)
		if err == nil {
			return true, nil
		}

		if apierrors.IsConflict(err) {
			err := s.hostClusterClient.Get(context.TODO(), obj, obj.GetNamespace(), obj.GetName())
			if err != nil {
				return false, errors.Wrapf(err, "failed to retrieve resource")
			}
			return false, nil
		}
		return false, errors.Wrapf(err, "failed to update resource")
	})
	if err != nil {
		keyedLogger.Error(err, "Failed to update syncing annotation")
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (s *SyncController) ensureDeletion(ctx context.Context, fedResource FederatedResource) worker.Result {
	fedResource.DeleteVersions()

	key := fedResource.FederatedName().String()
	kind := fedResource.FederatedKind()
	keyedLogger := klog.FromContext(ctx)

	keyedLogger.V(2).Info("Ensuring deletion of federated object")

	obj := fedResource.Object()

	finalizers := sets.NewString(obj.GetFinalizers()...)
	if !finalizers.Has(FinalizerSyncController) {
		keyedLogger.WithValues("finalizer-name", FinalizerSyncController).
			V(3).Info("Federated object does not have the finalizer. Nothing to do")
		return worker.StatusAllOK
	}

	if util.GetOrphaningBehavior(obj) == util.OrphanManagedResourcesAll {
		keyedLogger.WithValues("orphaning-behavior", util.OrphanManagedResourcesAll).
			V(2).Info("Removing the finalizer")
		err := s.deleteHistory(fedResource)
		if err != nil {
			keyedLogger.Error(err, "Failed to delete history for federated object")
			return worker.StatusError
		}
		err = s.removeFinalizer(ctx, fedResource)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			keyedLogger.WithValues("finalizer-name", FinalizerSyncController).
				Error(err, "Failed to remove finalizer for federated object")
			return worker.StatusError
		}
		keyedLogger.WithValues("label-name", managedlabel.ManagedByKubeAdmiralLabelKey).
			V(2).Info("Removing managed label from resources previously managed by this federated object")
		err = s.removeManagedLabel(ctx, fedResource.TargetGVK(), fedResource.TargetName())
		if err != nil {
			keyedLogger.WithValues("label-name", managedlabel.ManagedByKubeAdmiralLabelKey).
				Error(err, "Failed to remove the label from all resources previously managed by this federated object")
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	keyedLogger.V(2).Info("Deleting resources managed by this federated object from member clusters")
	recheckRequired, err := s.deleteFromClusters(ctx, fedResource)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "failed to delete %s %q", kind, key)
		fedResource.RecordError(string(fedtypesv1a1.EnsureDeletionFailed), wrappedErr)
		keyedLogger.Error(err, "Failed to delete federated object")
		return worker.StatusError
	}
	if recheckRequired {
		return worker.Result{RequeueAfter: &s.ensureDeletionRecheckDelay}
	}
	if err := s.removeFinalizer(ctx, fedResource); err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		keyedLogger.WithValues("finalizer-name", FinalizerSyncController).
			Error(err, "Failed to remove finalizer from the federated object")
		return worker.StatusError
	}
	return worker.StatusAllOK
}

// removeManagedLabel attempts to remove the managed label from
// resources with the given name in member clusters.
func (s *SyncController) removeManagedLabel(
	ctx context.Context,
	gvk schema.GroupVersionKind,
	qualifiedName common.QualifiedName,
) error {
	ok, err := s.handleDeletionInClusters(
		ctx,
		gvk,
		qualifiedName,
		func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured) {
			if clusterObj.GetDeletionTimestamp() != nil {
				return
			}

			dispatcher.RemoveManagedLabel(ctx, clusterName, clusterObj)
		},
	)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Errorf("failed to remove the label from resources in one or more clusters.")
	}
	return nil
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
	orphaningBehavior := util.GetOrphaningBehavior(fedResource.Object())
	shouldBeOrphaned := orphaningBehavior == util.OrphanManagedResourcesAll ||
		orphaningBehavior == util.OrphanManagedResourcesAdopted && util.HasAdoptedAnnotation(clusterObj)
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
	if err := s.deleteHistory(fedResource); err != nil {
		return false, err
	}

	return false, nil
}

// ensureRemovedOrUnmanaged ensures that no resources in member
// clusters that could be managed by the given federated resources are
// present or labeled as managed.  The checks are performed without
// the informer to cover the possibility that the resources have not
// yet been cached.
func (s *SyncController) ensureRemovedOrUnmanaged(ctx context.Context, fedResource FederatedResource) error {
	clusters, err := s.informer.GetJoinedClusters()
	if err != nil {
		return errors.Wrap(err, "failed to get a list of clusters")
	}

	dispatcher := dispatch.NewCheckUnmanagedDispatcher(
		s.informer.GetClientForCluster,
		fedResource.TargetGVK(),
		fedResource.TargetName(),
	)
	unreadyClusters := []string{}
	for _, cluster := range clusters {
		if !util.IsClusterReady(&cluster.Status) {
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
	gvk schema.GroupVersionKind,
	qualifiedName common.QualifiedName,
	deletionFunc func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured),
) (bool, error) {
	clusters, err := s.informer.GetJoinedClusters()
	if err != nil {
		return false, errors.Wrap(err, "failed to get a list of clusters")
	}
	keyedLogger := klog.FromContext(ctx)

	dispatcher := dispatch.NewUnmanagedDispatcher(s.informer.GetClientForCluster, gvk, qualifiedName)
	retrievalFailureClusters := []string{}
	unreadyClusters := []string{}
	for _, cluster := range clusters {
		clusterName := cluster.Name

		if !util.IsClusterReady(&cluster.Status) {
			unreadyClusters = append(unreadyClusters, clusterName)
			continue
		}

		clusterObj, _, err := util.GetClusterObject(
			context.TODO(),
			s.informer,
			clusterName,
			qualifiedName,
			s.typeConfig.GetTargetType(),
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
	obj := fedResource.Object()
	isUpdated, err := finalizersutil.AddFinalizers(obj, sets.NewString(FinalizerSyncController))
	keyedLogger := klog.FromContext(ctx)
	if err != nil || !isUpdated {
		return err
	}
	keyedLogger.WithValues("finalizer-name", FinalizerSyncController).
		V(1).Info("Adding finalizer to federated object")
	return s.hostClusterClient.Update(context.TODO(), obj)
}

func (s *SyncController) ensureAnnotations(
	ctx context.Context,
	fedResource FederatedResource,
	lastRevision, currentRevision string,
) error {
	obj := fedResource.Object().DeepCopy()
	updated := false
	keyedLogger := klog.FromContext(ctx)

	// ensure last revision annotation
	if len(lastRevision) != 0 {
		revisionUpdated, err := annotationutil.AddAnnotation(obj, common.LastRevisionAnnotation, lastRevision)
		if err != nil {
			return err
		}
		updated = updated || revisionUpdated
	}

	// ensure current revision annotation
	if len(currentRevision) != 0 {
		revisionUpdated, err := annotationutil.AddAnnotation(obj, common.CurrentRevisionAnnotation, currentRevision)
		if err != nil {
			return err
		}
		updated = updated || revisionUpdated
	}

	if !updated {
		return nil
	}

	keyedLogger.WithValues("last-revision-annotation-name", common.LastRevisionAnnotation,
		"current-revision-annotation-name", common.CurrentRevisionAnnotation).
		V(1).Info("Adding Latest Revision Annotation and Current Revision Annotation to federated object")
	if err := s.hostClusterClient.Update(context.TODO(), obj); err != nil {
		return err
	}

	return nil
}

func (s *SyncController) removeFinalizer(ctx context.Context, fedResource FederatedResource) error {
	keyedLogger := klog.FromContext(ctx)
	obj := fedResource.Object()
	isUpdated, err := finalizersutil.RemoveFinalizers(obj, sets.NewString(FinalizerSyncController))
	if err != nil || !isUpdated {
		return err
	}
	keyedLogger.WithValues("finalizer-name", FinalizerSyncController).
		V(1).Info("Removing finalizer from federated object")
	return s.hostClusterClient.Update(context.TODO(), obj)
}

func (s *SyncController) deleteHistory(fedResource FederatedResource) error {
	return s.hostClusterClient.DeleteHistory(context.TODO(), fedResource.Object())
}

func (s *SyncController) ensureClusterFinalizer(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) error {
	clusteredKeyedLogger := klog.FromContext(ctx)
	clusteredKeyedLogger.WithValues("finalizer-name", s.cascadingDeleteFinalizer).
		V(1).Info("Adding finalizer to cluster")
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := s.hostClusterClient.Get(context.TODO(), cluster, cluster.Namespace, cluster.Name); err != nil {
			return err
		}
		isUpdated, err := finalizersutil.AddFinalizers(cluster, sets.NewString(s.cascadingDeleteFinalizer))
		if err != nil || !isUpdated {
			return err
		}
		return s.hostClusterClient.Update(context.TODO(), cluster)
	}); err != nil {
		return errors.Wrapf(
			err,
			"failed to ensure finalizer %s from cluster %q",
			s.cascadingDeleteFinalizer,
			cluster.Name,
		)
	}
	return nil
}

func (s *SyncController) removeClusterFinalizer(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) error {
	keyedLogger := klog.FromContext(ctx)
	keyedLogger.WithValues("finalizer-name", s.cascadingDeleteFinalizer).
		V(1).Info("Removing finalizer from cluster")
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := s.hostClusterClient.Get(context.TODO(), cluster, cluster.Namespace, cluster.Name); err != nil {
			return err
		}
		isUpdated, err := finalizersutil.RemoveFinalizers(cluster, sets.NewString(s.cascadingDeleteFinalizer))
		if err != nil || !isUpdated {
			return err
		}
		return s.hostClusterClient.Update(context.TODO(), cluster)
	}); err != nil {
		return errors.Wrapf(
			err,
			"failed to remove finalizer %s from cluster %q",
			s.cascadingDeleteFinalizer,
			cluster.Name,
		)
	}
	return nil
}

func (s *SyncController) reconcileCluster(qualifiedName common.QualifiedName) worker.Result {
	logger := s.logger.WithValues("cluster-name", qualifiedName.String())
	ctx := klog.NewContext(context.TODO(), logger)

	cluster, found, err := s.informer.GetCluster(qualifiedName.Name)
	if err != nil {
		logger.Error(err, "Failed to get federated cluster")
		return worker.StatusError
	}
	if !found {
		return worker.StatusAllOK
	}

	cluster = cluster.DeepCopy()
	if cluster.DeletionTimestamp == nil {
		// cluster is not yet terminating, ensure it has cascading-delete finalizer
		err := s.ensureClusterFinalizer(ctx, cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			logger.Error(err, "Failed to ensure cluster finalizer")
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if !util.IsClusterJoined(&cluster.Status) || !util.IsCascadingDeleteEnabled(cluster) {
		// cascading-delete is not required, remove cascading-delete finalizer immediately
		err := s.removeClusterFinalizer(ctx, cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			logger.Error(err, "Failed to remove cluster finalizer")
			return worker.StatusError
		}

		return worker.StatusAllOK
	}

	// cascading-delete is enabled, wait for member objects to be deleted
	client, err := s.informer.GetClientForCluster(cluster.Name)
	if err != nil {
		s.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning,
			EventReasonWaitForCascadingDeleteError,
			"unable to get cluster client: cluster is not available (check cluster conditions): %v",
			err,
		)
		logger.Error(err, "Failed to get cluster client")
		return worker.StatusError
	}

	// we need to do an actual list because federated informer returns an empty list by default
	// if the cluster is unavailable
	targetType := s.typeConfig.GetTargetType()
	objects := &unstructured.UnstructuredList{}
	objects.SetGroupVersionKind(schemautil.APIResourceToGVK(&targetType))
	err = client.ListWithOptions(
		context.TODO(),
		objects,
		runtimeclient.Limit(1),
		runtimeclient.InNamespace(corev1.NamespaceAll),
		runtimeclient.MatchingLabels{
			managedlabel.ManagedByKubeAdmiralLabelKey: managedlabel.ManagedByKubeAdmiralLabelValue,
		},
	)
	if err == nil && len(objects.Items) > 0 {
		s.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeNormal,
			EventReasonWaitForCascadingDelete,
			"waiting for cascading delete of %s",
			s.typeConfig.GetTargetType().Name,
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

	// either all member objects are deleted or the resource does not exist, remove finalizer
	err = s.removeClusterFinalizer(ctx, cluster)
	if err != nil {
		logger.Error(err, "Failed to remove cluster finalizer")
		return worker.StatusError
	}

	return worker.StatusAllOK
}
