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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
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
	deliverutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/history"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	allClustersKey                         = "ALL_CLUSTERS"
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
	FinalizerCascadingDeletePrefix = common.DefaultPrefix + "cascading-delete"
)

// KubeFedSyncController synchronizes the state of federated resources
// in the host cluster with resources in member clusters.
type KubeFedSyncController struct {
	worker        worker.ReconcileWorker
	clusterWorker worker.ReconcileWorker

	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterDeliverer *deliverutil.DelayingDeliverer

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
}

// StartKubeFedSyncController starts a new sync controller for a type config
func StartKubeFedSyncController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedNamespaceAPIResource *metav1.APIResource,
	controllerRevisionStore cache.Store,
	controllerRevisionController cache.Controller,
) error {
	controller, err := newKubeFedSyncController(
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
	klog.Infof(fmt.Sprintf("Starting sync controller for %q", typeConfig.GetFederatedType().Kind))
	controller.Run(stopChan)
	return nil
}

// newKubeFedSyncController returns a new sync controller for the configuration
func newKubeFedSyncController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedNamespaceAPIResource *metav1.APIResource,
	controllerRevisionStore cache.Store,
	controllerRevisionController cache.Controller,
) (*KubeFedSyncController, error) {
	federatedTypeAPIResource := typeConfig.GetFederatedType()
	userAgent := fmt.Sprintf("%s-federate-sync-controller", strings.ToLower(federatedTypeAPIResource.Kind))

	// Initialize non-dynamic clients first to avoid polluting config
	client := genericclient.NewForConfigOrDieWithUserAgent(controllerConfig.KubeConfig, userAgent)
	kubeClient := kubeclient.NewForConfigOrDie(controllerConfig.KubeConfig)

	configCopy := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configCopy, userAgent)

	recorder := eventsink.NewDefederatingRecorderMux(kubeClient, userAgent, 4)

	s := &KubeFedSyncController{
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
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		deliverutil.NewMetricTags("sync-worker", typeConfig.GetTargetType().Kind),
	)

	s.clusterWorker = worker.NewReconcileWorker(s.reconcileCluster, worker.WorkerTiming{}, 1, controllerConfig.Metrics,
		deliverutil.NewMetricTags("sync-cluster-worker", typeConfig.GetTargetType().Kind))

	// Build deliverer for triggering cluster reconciliations.
	s.clusterDeliverer = deliverutil.NewDelayingDeliverer()

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
				s.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(s.clusterAvailableDelay))
			},
			// When a cluster becomes unavailable process all the target resources again.
			ClusterUnavailable: func(cluster *fedcorev1a1.FederatedCluster, _ []interface{}) {
				s.clusterWorker.EnqueueObject(cluster)
				s.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(s.clusterUnavailableDelay))
			},
		},
	)
	if err != nil {
		return nil, err
	}

	s.fedAccessor, err = NewFederatedResourceAccessor(
		controllerConfig, typeConfig, fedNamespaceAPIResource,
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
func (s *KubeFedSyncController) minimizeLatency() {
	s.clusterAvailableDelay = time.Second
	s.clusterUnavailableDelay = time.Second
	s.reconcileOnClusterChangeDelay = 20 * time.Millisecond
	s.memberObjectEnqueueDelay = 50 * time.Millisecond
	s.recheckAfterDispatchDelay = 2 * time.Second
	s.ensureDeletionRecheckDelay = 2 * time.Second
	s.cascadingDeletionRecheckDelay = 3 * time.Second
}

func (s *KubeFedSyncController) Run(stopChan <-chan struct{}) {
	s.fedAccessor.Run(stopChan)
	s.informer.Start()
	s.clusterDeliverer.StartWithHandler(func(_ *deliverutil.DelayingDelivererItem) {
		s.reconcileOnClusterChange()
	})

	go s.clusterDeliverer.RunMetricLoop(stopChan, 30*time.Second, s.metrics,
		deliverutil.NewMetricTags("sync-clusterDeliverer", s.typeConfig.GetTargetType().Kind))

	s.worker.Run(stopChan)
	s.clusterWorker.Run(stopChan)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		s.informer.Stop()
		s.clusterDeliverer.Stop()
	}()
}

// Check whether all data stores are in sync. False is returned if any of the informer/stores is not yet
// synced with the corresponding api server.
func (s *KubeFedSyncController) HasSynced() bool {
	if !s.informer.ClustersSynced() {
		klog.V(2).Infof("Cluster list not synced")
		return false
	}
	if !s.fedAccessor.HasSynced() {
		// The fed accessor will have logged why sync is not yet
		// complete.
		return false
	}

	if s.typeConfig.GetRevisionHistoryEnabled() && !s.revListerSynced() {
		klog.V(2).Infof("controllerRevision list not synced")
		return false
	}

	// TODO set clusters as ready in the test fixture?
	clusters, err := s.informer.GetReadyClusters()
	if err != nil {
		runtime.HandleError(errors.Wrap(err, "Failed to get ready clusters"))
		return false
	}
	if !s.informer.GetTargetStore().ClustersSynced(clusters) {
		return false
	}
	return true
}

// The function triggers reconciliation of all target federated resources.
func (s *KubeFedSyncController) reconcileOnClusterChange() {
	if !s.HasSynced() {
		s.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(s.clusterAvailableDelay))
	}
	s.fedAccessor.VisitFederatedResources(func(obj interface{}) {
		qualifiedName := common.NewQualifiedName(obj.(pkgruntime.Object))
		s.worker.EnqueueWithDelay(qualifiedName, s.reconcileOnClusterChangeDelay)
	})
}

func (s *KubeFedSyncController) reconcile(qualifiedName common.QualifiedName) worker.Result {
	if !s.HasSynced() {
		return worker.Result{RequeueAfter: &s.clusterAvailableDelay}
	}

	kind := s.typeConfig.GetFederatedType().Kind

	fedResource, possibleOrphan, err := s.fedAccessor.FederatedResource(qualifiedName)
	if err != nil {
		runtime.HandleError(errors.Wrapf(err, "Error creating FederatedResource helper for %s %q", kind, qualifiedName))
		return worker.StatusError
	}
	if possibleOrphan {
		apiResource := s.typeConfig.GetTargetType()
		gvk := schemautil.APIResourceToGVK(&apiResource)
		klog.V(2).
			Infof("Ensuring the removal of the label %q from %s %q in member clusters.", util.ManagedByKubeFedLabelKey, gvk.Kind, qualifiedName)
		err = s.removeManagedLabel(gvk, qualifiedName)
		if err != nil {
			wrappedErr := errors.Wrapf(
				err,
				"failed to remove the label %q from %s %q in member clusters",
				util.ManagedByKubeFedLabelKey,
				gvk.Kind,
				qualifiedName,
			)
			runtime.HandleError(wrappedErr)
			return worker.StatusError
		}

		return worker.StatusAllOK
	}
	if fedResource == nil {
		return worker.StatusAllOK
	}

	key := fedResource.FederatedName().String()

	s.metrics.Rate("sync.throughput", 1)
	klog.V(4).Infof("Starting to reconcile %s %q", kind, key)
	startTime := time.Now()
	defer func() {
		s.metrics.Duration("sync.latency", startTime)
		klog.V(4).Infof("Finished reconciling %s %q (duration: %v)", kind, key, time.Since(startTime))
	}()

	if fedResource.Object().GetDeletionTimestamp() != nil {
		return s.ensureDeletion(fedResource)
	}

	pendingControllers, err := pendingcontrollers.GetPendingControllers(fedResource.Object())
	if err != nil {
		runtime.HandleError(fmt.Errorf("failed to get pending controllers for %s %q: %w", kind, key, err))
		return worker.StatusError
	}
	if len(pendingControllers) > 0 {
		// upstream controllers have not finished processing, we wait for our turn
		return worker.StatusAllOK
	}

	err = s.ensureFinalizer(fedResource)
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
		klog.V(4).Infof("Starting to sync revisions for %s %s", kind, key)
		collisionCount, lastRevisionNameWithHash, currentRevisionName, err = s.syncRevisions(fedResource)
		if err != nil {
			klog.Errorf("debug - Failed to sync revisions for %s %s: %v", kind, key, err)
			fedResource.RecordError("SyncRevisionHistoryError", errors.Wrap(err, "Failed to sync revisions"))
			return worker.StatusError
		}
	}
	err = s.ensureAnnotations(fedResource, lastRevisionNameWithHash, currentRevisionName)
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		klog.Errorf("debug - Failed to ensureAnnotations for %s %s: %v", kind, key, err)
		fedResource.RecordError("EnsureAnnotationsErr", errors.Wrap(err, "Failed to ensure annotations"))
		return worker.StatusError
	}

	return s.syncToClusters(fedResource, collisionCount)
}

// syncToClusters ensures that the state of the given object is
// synchronized to member clusters.
func (s *KubeFedSyncController) syncToClusters(fedResource FederatedResource, collisionCount *int32) worker.Result {
	clusters, err := s.informer.GetJoinedClusters()
	if err != nil {
		fedResource.RecordError(
			string(fedtypesv1a1.ClusterRetrievalFailed),
			errors.Wrap(err, "Failed to retrieve list of clusters"),
		)
		return s.setFederatedStatus(fedResource, collisionCount, fedtypesv1a1.ClusterRetrievalFailed, nil)
	}

	selectedClusterNames, err := fedResource.ComputePlacement(clusters)
	if err != nil {
		fedResource.RecordError(
			string(fedtypesv1a1.ComputePlacementFailed),
			errors.Wrap(err, "Failed to compute placement"),
		)
		return s.setFederatedStatus(fedResource, collisionCount, fedtypesv1a1.ComputePlacementFailed, nil)
	}

	kind := fedResource.TargetKind()
	key := fedResource.TargetName().String()
	klog.V(4).Infof("Ensuring %s %q in clusters: %s", kind, key, strings.Join(selectedClusterNames.List(), ","))

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

		rawClusterObj, _, err := s.informer.GetTargetStore().GetByKey(clusterName, key)
		if err != nil {
			wrappedErr := errors.Wrap(err, "Failed to retrieve cached cluster object")
			dispatcher.RecordClusterError(fedtypesv1a1.CachedRetrievalFailed, clusterName, wrappedErr)
			continue
		}
		var clusterObj *unstructured.Unstructured
		if rawClusterObj != nil {
			clusterObj = rawClusterObj.(*unstructured.Unstructured)
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
			s.deleteFromCluster(dispatcher, clusterName, fedResource, clusterObj, isCascadingDeletionTriggered)
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
			dispatcher.Create(clusterName)
		} else {
			dispatcher.Update(clusterName, clusterObj)
		}
	}

	dispatchOk, timeoutErr := dispatcher.Wait()
	if !dispatchOk {
		klog.Errorf("clusterObj %s syncToCluster failed", key)
	}
	if timeoutErr != nil {
		fedResource.RecordError("OperationTimeoutError", timeoutErr)
		klog.Errorf("clusterObj %s syncToCluster timeout", key)
		return worker.StatusError
	}

	if dispatchOk {
		// Update SyncSuccessTimestamp annotation to federated resource.
		obj := fedResource.Object()
		annotations := obj.GetAnnotations()
		generation := obj.GetGeneration()
		updateAnnotation := true

		if v, ok := annotations[annotationutil.LastSyncSucceessGeneration]; ok {
			if strconv.FormatInt(generation, 10) == v {
				updateAnnotation = false
			}
		}

		if updateAnnotation {
			_, err = annotationutil.AddAnnotation(
				obj,
				annotationutil.LastSyncSucceessGeneration,
				strconv.FormatInt(generation, 10),
			)
			if err != nil {
				return worker.StatusError
			}

			syncSuccessTimestamp := metav1.Now().UTC().Format(time.RFC3339Nano)
			_, err = annotationutil.AddAnnotation(obj, annotationutil.SyncSuccessTimestamp, syncSuccessTimestamp)
			if err != nil {
				return worker.StatusError
			}

			err = s.hostClusterClient.Update(context.TODO(), obj)
			if err != nil {
				if apierrors.IsConflict(err) {
					return worker.StatusConflict
				}
				klog.Errorf(
					"Failed to update syncSuccessTimestamp annotation of %s: %v",
					fedResource.FederatedName(),
					err,
				)
				return worker.StatusError
			}
		}
	}

	// Write updated versions to the API.
	updatedVersionMap := dispatcher.VersionMap()
	err = fedResource.UpdateVersions(selectedClusterNames.List(), updatedVersionMap)
	if err != nil {
		// Versioning of federated resources is an optimization to
		// avoid unnecessary updates, and failure to record version
		// information does not indicate a failure of propagation.
		runtime.HandleError(err)
	}

	collectedStatus := dispatcher.CollectedStatus()
	if reconcileStatus := s.setFederatedStatus(
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

func (s *KubeFedSyncController) setFederatedStatus(fedResource FederatedResource, collisionCount *int32,
	reason fedtypesv1a1.AggregateReason, collectedStatus *status.CollectedPropagationStatus,
) worker.Result {
	if collectedStatus == nil {
		collectedStatus = &status.CollectedPropagationStatus{}
	}

	kind := fedResource.FederatedKind()
	name := fedResource.FederatedName()
	obj := fedResource.Object()

	// Only a single reason for propagation failure is reported at any one time, so only report
	// NamespaceNotFederated if no other explicit error has been indicated.
	if reason == fedtypesv1a1.AggregateSuccess {
		// For a cluster-scoped control plane, report when the containing namespace of a federated
		// resource is not federated.  The KubeFed system namespace is implicitly federated in a
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
			klog.V(4).Infof("No status update necessary for %s %q", kind, name)
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
		runtime.HandleError(errors.Wrapf(err, "failed to set propagation status for %s %q", kind, name))
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
		runtime.HandleError(errors.Wrapf(err, "failed to update syncing annotation for %s %q", kind, name))
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (s *KubeFedSyncController) ensureDeletion(fedResource FederatedResource) worker.Result {
	fedResource.DeleteVersions()

	key := fedResource.FederatedName().String()
	kind := fedResource.FederatedKind()

	klog.V(2).Infof("Ensuring deletion of %s %q", kind, key)

	obj := fedResource.Object()

	finalizers := sets.NewString(obj.GetFinalizers()...)
	if !finalizers.Has(FinalizerSyncController) {
		klog.V(2).Infof("%s %q does not have the %q finalizer. Nothing to do.", kind, key, FinalizerSyncController)
		return worker.StatusAllOK
	}

	if util.GetOrphaningBehavior(obj) == util.OrphanManagedResourcesAll {
		klog.V(2).Infof("Found %q annotation on %s %q. Removing the finalizer.",
			util.OrphanManagedResourcesAnnotation, kind, key)
		err := s.deleteHistory(fedResource)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to delete history for %s %q", kind, key)
			runtime.HandleError(wrappedErr)
			return worker.StatusError
		}
		err = s.removeFinalizer(fedResource)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			wrappedErr := errors.Wrapf(
				err,
				"failed to remove finalizer %q from %s %q",
				FinalizerSyncController,
				kind,
				key,
			)
			runtime.HandleError(wrappedErr)
			return worker.StatusError
		}
		klog.V(2).
			Infof("Initiating the removal of the label %q from resources previously managed by %s %q.", util.ManagedByKubeFedLabelKey, kind, key)
		err = s.removeManagedLabel(fedResource.TargetGVK(), fedResource.TargetName())
		if err != nil {
			wrappedErr := errors.Wrapf(
				err,
				"failed to remove the label %q from all resources previously managed by %s %q",
				util.ManagedByKubeFedLabelKey,
				kind,
				key,
			)
			runtime.HandleError(wrappedErr)
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	klog.V(2).Infof("Deleting resources managed by %s %q from member clusters.", kind, key)
	recheckRequired, err := s.deleteFromClusters(fedResource)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "failed to delete %s %q", kind, key)
		fedResource.RecordError(string(fedtypesv1a1.EnsureDeletionFailed), wrappedErr)
		runtime.HandleError(wrappedErr)
		return worker.StatusError
	}
	if recheckRequired {
		return worker.Result{RequeueAfter: &s.ensureDeletionRecheckDelay}
	}
	if err := s.removeFinalizer(fedResource); err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		runtime.HandleError(
			fmt.Errorf("failed to remove finalizer %q from %s %q: %w", FinalizerSyncController, kind, key, err),
		)
		return worker.StatusError
	}
	return worker.StatusAllOK
}

// removeManagedLabel attempts to remove the managed label from
// resources with the given name in member clusters.
func (s *KubeFedSyncController) removeManagedLabel(
	gvk schema.GroupVersionKind,
	qualifiedName common.QualifiedName,
) error {
	ok, err := s.handleDeletionInClusters(
		gvk,
		qualifiedName,
		func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured) {
			if clusterObj.GetDeletionTimestamp() != nil {
				return
			}

			dispatcher.RemoveManagedLabel(clusterName, clusterObj)
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

func (s *KubeFedSyncController) deleteFromCluster(
	dispatcher dispatch.UnmanagedDispatcher,
	clusterName string,
	fedResource FederatedResource,
	clusterObj *unstructured.Unstructured,
	respectOrphaningBehavior bool,
) {
	// Avoid attempting any operation on a deleted resource.
	if clusterObj.GetDeletionTimestamp() != nil {
		return
	}

	if !respectOrphaningBehavior {
		dispatcher.Delete(clusterName)
		return
	}

	// Respect orphaning behavior
	orphaningBehavior := util.GetOrphaningBehavior(fedResource.Object())
	shouldBeOrphaned := orphaningBehavior == util.OrphanManagedResourcesAll ||
		orphaningBehavior == util.OrphanManagedResourcesAdopted && util.HasAdoptedAnnotation(clusterObj)
	if shouldBeOrphaned {
		klog.V(4).
			Infof("Resource %s %q in cluster %q is going to be orphaned",
				fedResource.TargetGVK(), fedResource.TargetName(), clusterName)
		dispatcher.RemoveManagedLabel(clusterName, clusterObj)
	} else {
		dispatcher.Delete(clusterName)
	}
}

func (s *KubeFedSyncController) deleteFromClusters(fedResource FederatedResource) (bool, error) {
	gvk := fedResource.TargetGVK()
	qualifiedName := fedResource.TargetName()

	remainingClusters := []string{}
	ok, err := s.handleDeletionInClusters(
		gvk,
		qualifiedName,
		func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured) {
			remainingClusters = append(remainingClusters, clusterName)
			s.deleteFromCluster(dispatcher, clusterName, fedResource, clusterObj, true)
		},
	)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errors.Errorf("failed to remove managed resources from one or more clusters.")
	}
	if len(remainingClusters) > 0 {
		fedKind := fedResource.FederatedKind()
		fedName := fedResource.FederatedName()
		klog.V(2).Infof(
			"Waiting for resources managed by %s %q to be removed from the following clusters: %s",
			fedKind, fedName, strings.Join(remainingClusters, ", "),
		)
		return true, nil
	}
	err = s.ensureRemovedOrUnmanaged(fedResource)
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
func (s *KubeFedSyncController) ensureRemovedOrUnmanaged(fedResource FederatedResource) error {
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
		dispatcher.CheckRemovedOrUnlabeled(cluster.Name)
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
func (s *KubeFedSyncController) handleDeletionInClusters(
	gvk schema.GroupVersionKind,
	qualifiedName common.QualifiedName,
	deletionFunc func(dispatcher dispatch.UnmanagedDispatcher, clusterName string, clusterObj *unstructured.Unstructured),
) (bool, error) {
	clusters, err := s.informer.GetJoinedClusters()
	if err != nil {
		return false, errors.Wrap(err, "failed to get a list of clusters")
	}

	dispatcher := dispatch.NewUnmanagedDispatcher(s.informer.GetClientForCluster, gvk, qualifiedName)
	retrievalFailureClusters := []string{}
	unreadyClusters := []string{}
	for _, cluster := range clusters {
		clusterName := cluster.Name

		if !util.IsClusterReady(&cluster.Status) {
			unreadyClusters = append(unreadyClusters, clusterName)
			continue
		}

		key := util.QualifiedNameForCluster(clusterName, qualifiedName).String()
		rawClusterObj, _, err := s.informer.GetTargetStore().GetByKey(clusterName, key)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retrieve %s %q for cluster %q", gvk.Kind, key, clusterName)
			runtime.HandleError(wrappedErr)
			retrievalFailureClusters = append(retrievalFailureClusters, clusterName)
			continue
		}
		if rawClusterObj == nil {
			continue
		}
		clusterObj := rawClusterObj.(*unstructured.Unstructured)
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

func (s *KubeFedSyncController) ensureFinalizer(fedResource FederatedResource) error {
	obj := fedResource.Object()
	isUpdated, err := finalizersutil.AddFinalizers(obj, sets.NewString(FinalizerSyncController))
	if err != nil || !isUpdated {
		return err
	}
	klog.V(2).
		Infof("Adding finalizer %s to %s %q", FinalizerSyncController, fedResource.FederatedKind(), fedResource.FederatedName())
	return s.hostClusterClient.Update(context.TODO(), obj)
}

func (s *KubeFedSyncController) ensureAnnotations(
	fedResource FederatedResource,
	lastRevision, currentRevision string,
) error {
	obj := fedResource.Object().DeepCopy()
	updated := false

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

	klog.V(2).
		Infof("Adding Latest Revision Annotation %s and Current Revision Annotation %s to %s %q", common.LastRevisionAnnotation,
			common.CurrentRevisionAnnotation, fedResource.FederatedKind(), fedResource.FederatedName())
	if err := s.hostClusterClient.Update(context.TODO(), obj); err != nil {
		return err
	}

	return nil
}

func (s *KubeFedSyncController) removeFinalizer(fedResource FederatedResource) error {
	obj := fedResource.Object()
	isUpdated, err := finalizersutil.RemoveFinalizers(obj, sets.NewString(FinalizerSyncController))
	if err != nil || !isUpdated {
		return err
	}
	klog.V(2).
		Infof("Removing finalizer %s from %s %q", FinalizerSyncController, fedResource.FederatedKind(), fedResource.FederatedName())
	return s.hostClusterClient.Update(context.TODO(), obj)
}

func (s *KubeFedSyncController) deleteHistory(fedResource FederatedResource) error {
	return s.hostClusterClient.DeleteHistory(context.TODO(), fedResource.Object())
}

func (s *KubeFedSyncController) enableRollout(fedResource FederatedResource) bool {
	return s.typeConfig.GetRolloutPlanEnabled()
}

func (s *KubeFedSyncController) ensureClusterFinalizer(cluster *fedcorev1a1.FederatedCluster) error {
	klog.V(2).Infof("Adding finalizer %s to cluster %q", s.cascadingDeleteFinalizer, cluster.Name)
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

func (s *KubeFedSyncController) removeClusterFinalizer(cluster *fedcorev1a1.FederatedCluster) error {
	klog.V(2).Infof("Removing finalizer %s from s %q", s.cascadingDeleteFinalizer, cluster.Name)
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

func (s *KubeFedSyncController) reconcileCluster(qualifiedName common.QualifiedName) worker.Result {
	cluster, found, err := s.informer.GetCluster(qualifiedName.Name)
	if err != nil {
		runtime.HandleError(err)
		return worker.StatusError
	}
	if !found {
		return worker.StatusAllOK
	}

	cluster = cluster.DeepCopy()
	if cluster.DeletionTimestamp == nil {
		// cluster is not yet terminating, ensure it has cascading-delete finalizer
		err := s.ensureClusterFinalizer(cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			runtime.HandleError(err)
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if !util.IsClusterJoined(&cluster.Status) || !util.IsCascadingDeleteEnabled(cluster) {
		// cascading-delete is not required, remove cascading-delete finalizer immediately
		err := s.removeClusterFinalizer(cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			runtime.HandleError(err)
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
		runtime.HandleError(errors.Wrapf(err, "failed to get cluster client for cluster %s", cluster.Name))
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
			util.ManagedByKubeFedLabelKey: util.ManagedByKubeFedLabelValue,
		},
	)
	if err != nil {
		runtime.HandleError(
			errors.Wrapf(
				err,
				"failed to list target objects %s from cluster %s",
				s.typeConfig.GetTargetType().Name,
				cluster.Name,
			),
		)
		return worker.StatusError
	}
	if len(objects.Items) > 0 {
		s.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeNormal,
			EventReasonWaitForCascadingDelete,
			"waiting for cascading delete of %s",
			s.typeConfig.GetTargetType().Name,
		)
		return worker.Result{RequeueAfter: &s.cascadingDeletionRecheckDelay}
	}

	// all member objects are deleted, remove finalizer
	err = s.removeClusterFinalizer(cluster)
	if err != nil {
		runtime.HandleError(err)
		return worker.StatusError
	}

	return worker.StatusAllOK
}
