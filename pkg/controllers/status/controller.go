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

package status

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/annotation"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/collectedstatusadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	StatusControllerName = "status-controller"
)

const (
	EventReasonGetObjectStatusError = "GetObjectStatusError"
)

// StatusController collects the status of resources in member clusters.
type StatusController struct {
	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterQueue workqueue.DelayingInterface

	fedClient          fedclient.Interface
	fedInformerManager informermanager.FederatedInformerManager
	ftcManager         informermanager.FederatedTypeConfigManager

	// Informers for federated objects
	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer

	// Informers for collected status objects
	collectedStatusInformer        fedcorev1a1informers.CollectedStatusInformer
	clusterCollectedStatusInformer fedcorev1a1informers.ClusterCollectedStatusInformer

	worker worker.ReconcileWorker[common.QualifiedName]

	clusterAvailableDelay         time.Duration
	clusterUnavailableDelay       time.Duration
	reconcileOnClusterChangeDelay time.Duration
	memberObjectEnqueueDelay      time.Duration

	metrics       stats.Metrics
	logger        klog.Logger
	eventRecorder record.EventRecorder
}

func (s *StatusController) IsControllerReady() bool {
	return s.HasSynced()
}

// NewStatusController returns a new status controller for the configuration
func NewStatusController(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,

	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	collectedStatusInformer fedcorev1a1informers.CollectedStatusInformer,
	clusterCollectedStatusInformer fedcorev1a1informers.ClusterCollectedStatusInformer,

	ftcManager informermanager.FederatedTypeConfigManager,
	fedInformerManager informermanager.FederatedInformerManager,

	clusterAvailableDelay, clusterUnavailableDelay, memberObjectEnqueueDelay time.Duration,

	logger klog.Logger,
	workerCount int,
	metrics stats.Metrics,
) (*StatusController, error) {
	s := &StatusController{
		fedClient:                      fedClient,
		fedInformerManager:             fedInformerManager,
		ftcManager:                     ftcManager,
		fedObjectInformer:              fedObjectInformer,
		clusterFedObjectInformer:       clusterFedObjectInformer,
		collectedStatusInformer:        collectedStatusInformer,
		clusterCollectedStatusInformer: clusterCollectedStatusInformer,
		clusterAvailableDelay:          clusterAvailableDelay,
		clusterUnavailableDelay:        clusterUnavailableDelay,
		reconcileOnClusterChangeDelay:  time.Second * 3,
		memberObjectEnqueueDelay:       memberObjectEnqueueDelay,
		metrics:                        metrics,
		logger:                         logger.WithValues("controller", StatusControllerName),
		eventRecorder:                  eventsink.NewDefederatingRecorderMux(kubeClient, StatusControllerName, 4),
	}

	s.worker = worker.NewReconcileWorker(
		StatusControllerName,
		s.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	// Build queue for triggering cluster reconciliations.
	s.clusterQueue = workqueue.NewNamedDelayingQueue("status-controller-cluster-queue")

	fedObjectHandler := eventhandlers.NewTriggerOnAllChangesWithTransform(
		common.NewQualifiedName,
		func(key common.QualifiedName) {
			s.enqueueEnableCollectedStatusObject(key, 0)
		},
	)

	if _, err := s.fedObjectInformer.Informer().AddEventHandler(fedObjectHandler); err != nil {
		return nil, err
	}

	if _, err := s.clusterFedObjectInformer.Informer().AddEventHandler(fedObjectHandler); err != nil {
		return nil, err
	}

	if _, err := s.collectedStatusInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChangesWithTransform(common.NewQualifiedName, s.worker.Enqueue),
	); err != nil {
		return nil, err
	}

	if _, err := s.clusterCollectedStatusInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChangesWithTransform(common.NewQualifiedName, s.worker.Enqueue),
	); err != nil {
		return nil, err
	}

	if err := s.fedInformerManager.AddEventHandlerGenerator(&informermanager.EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool {
			if lastApplied == nil || latest == nil {
				return true
			}
			return lastApplied.IsStatusCollectionEnabled() != latest.IsStatusCollectionEnabled() ||
				(lastApplied.Spec.StatusCollection != nil && latest.Spec.StatusCollection != nil &&
					!reflect.DeepEqual(lastApplied.Spec.StatusCollection.Fields, latest.Spec.StatusCollection.Fields))
		},
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			if !ftc.IsStatusCollectionEnabled() {
				return nil
			}

			return eventhandlers.NewTriggerOnAllChanges(
				func(uns *unstructured.Unstructured) {
					ftc, exists := s.ftcManager.GetResourceFTC(uns.GroupVersionKind())
					if !exists {
						return
					}

					federatedName := common.QualifiedName{
						Namespace: uns.GetNamespace(),
						Name:      naming.GenerateFederatedObjectName(uns.GetName(), ftc.GetName()),
					}
					s.worker.EnqueueWithDelay(federatedName, s.memberObjectEnqueueDelay)
				},
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler generator: %w", err)
	}

	if err := s.fedInformerManager.AddClusterEventHandlers(
		&informermanager.ClusterEventHandler{
			Predicate: func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				// Reconcile all federated objects when cluster becomes available
				return oldCluster != nil && newCluster != nil &&
					!clusterutil.IsClusterReady(&oldCluster.Status) && clusterutil.IsClusterReady(&newCluster.Status)
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				s.clusterQueue.AddAfter(struct{}{}, s.clusterAvailableDelay)
			},
		},
		&informermanager.ClusterEventHandler{
			Predicate: func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				// Reconcile all federated objects when cluster becomes unavailable
				return oldCluster != nil && newCluster != nil &&
					clusterutil.IsClusterReady(&oldCluster.Status) && !clusterutil.IsClusterReady(&newCluster.Status)
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				s.clusterQueue.AddAfter(struct{}{}, s.clusterUnavailableDelay)
			},
		},
	); err != nil {
		return nil, fmt.Errorf("failed to add cluster event handler: %w", err)
	}

	return s, nil
}

// Run runs the status controller
func (s *StatusController) Run(ctx context.Context) {
	go func() {
		for {
			item, shutdown := s.clusterQueue.Get()
			if shutdown {
				break
			}
			s.reconcileOnClusterChange()
			s.clusterQueue.Done(item)
		}
	}()

	if !cache.WaitForNamedCacheSync(StatusControllerName, ctx.Done(), s.HasSynced) {
		s.logger.Error(nil, "Timed out waiting for cache sync")
		return
	}
	s.logger.Info("Caches are synced")
	s.worker.Run(ctx)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-ctx.Done()
		s.clusterQueue.ShutDown()
	}()
}

// Check whether all data stores are in sync. False is returned if any of the informer/stores is not yet
// synced with the corresponding api server.
func (s *StatusController) HasSynced() bool {
	return s.ftcManager.HasSynced() &&
		s.fedInformerManager.HasSynced() &&
		s.fedObjectInformer.Informer().HasSynced() &&
		s.clusterFedObjectInformer.Informer().HasSynced() &&
		s.collectedStatusInformer.Informer().HasSynced() &&
		s.clusterCollectedStatusInformer.Informer().HasSynced()
}

// The function triggers reconciliation of all target federated resources.
func (s *StatusController) reconcileOnClusterChange() {
	visitFunc := func(obj fedcorev1a1.GenericFederatedObject) {
		s.enqueueEnableCollectedStatusObject(common.NewQualifiedName(obj), s.reconcileOnClusterChangeDelay)
	}

	fedObjects, err := s.fedObjectInformer.Lister().List(labels.Everything())
	if err == nil {
		for _, obj := range fedObjects {
			visitFunc(obj)
		}
	} else {
		s.logger.Error(err, "Failed to list FederatedObjects from lister")
	}

	clusterFedObjects, err := s.clusterFedObjectInformer.Lister().List(labels.Everything())
	if err == nil {
		for _, obj := range clusterFedObjects {
			visitFunc(obj)
		}
	} else {
		s.logger.Error(err, "Failed to list ClusterFederatedObjects from lister")
	}
}

func (s *StatusController) reconcile(
	ctx context.Context,
	qualifiedName common.QualifiedName,
) (reconcileStatus worker.Result) {
	keyedLogger := s.logger.WithValues("federated-name", qualifiedName.String())
	ctx = klog.NewContext(ctx, keyedLogger)

	s.metrics.Counter(metrics.StatusThroughput, 1)
	keyedLogger.V(3).Info("Starting reconcile")
	startTime := time.Now()
	defer func() {
		s.metrics.Duration(metrics.StatusLatency, startTime)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", reconcileStatus.String()).
			V(3).Info("Finished reconcile")
	}()

	fedObject, err := fedobjectadapters.GetFromLister(
		s.fedObjectInformer.Lister(),
		s.clusterFedObjectInformer.Lister(),
		qualifiedName.Namespace, qualifiedName.Name,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return worker.StatusAllOK
		} else {
			keyedLogger.Error(err, "Failed to get federated object from cache")
			return worker.StatusError
		}
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		keyedLogger.V(1).Info("No federated type found, deleting status object")
		err = collectedstatusadapters.Delete(
			ctx,
			s.fedClient.CoreV1alpha1(),
			qualifiedName.Namespace,
			qualifiedName.Name,
			metav1.DeleteOptions{},
		)
		if err != nil && !apierrors.IsNotFound(err) {
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	template, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		keyedLogger.Error(err, "Failed to unmarshal template")
		return worker.StatusError
	}

	templateGVK := template.GroupVersionKind()
	targetIsDeployment := templateGVK == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind)

	templateQualifiedName := common.NewQualifiedName(template)

	typeConfig, exists := s.ftcManager.GetResourceFTC(templateGVK)
	if !exists || typeConfig == nil {
		keyedLogger.V(3).Info("Resource ftc not found")
		return worker.StatusAllOK
	}

	if typeConfig.Spec.StatusCollection == nil || !typeConfig.Spec.StatusCollection.Enabled {
		keyedLogger.V(3).Info("StatusCollection is not enabled")
		return worker.StatusAllOK
	}

	clusterNames, err := s.clusterNames()
	if err != nil {
		keyedLogger.Error(err, "Failed to get cluster list")
		return worker.Result{RequeueAfter: &s.clusterAvailableDelay}
	}

	clusterStatuses := s.clusterStatuses(ctx, fedObject, templateQualifiedName, templateGVK, typeConfig, clusterNames)

	existingStatus, err := collectedstatusadapters.GetFromLister(
		s.collectedStatusInformer.Lister(),
		s.clusterCollectedStatusInformer.Lister(),
		qualifiedName.Namespace, qualifiedName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get status from cache")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) {
		existingStatus = nil
	}
	if existingStatus != nil {
		existingStatus = existingStatus.DeepCopyGenericCollectedStatusObject()
	}

	var rsDigestsAnnotation string
	if targetIsDeployment {
		latestReplicasetDigests, err := s.latestReplicasetDigests(
			ctx,
			clusterNames,
			templateQualifiedName,
			templateGVK,
			typeConfig,
		)
		if err != nil {
			keyedLogger.Error(err, "Failed to get latest replicaset digests")
		} else {
			rsDigestsAnnotationBytes, err := json.Marshal(latestReplicasetDigests)
			if err != nil {
				return worker.StatusError
			}
			rsDigestsAnnotation = string(rsDigestsAnnotationBytes)
		}
	}

	var hasRSDigestsAnnotation bool
	if existingStatus != nil {
		hasRSDigestsAnnotation, err = annotation.HasAnnotationKeyValue(
			existingStatus,
			common.LatestReplicasetDigestsAnnotation,
			rsDigestsAnnotation,
		)
		if err != nil {
			return worker.StatusError
		}
	}

	collectedStatus := newCollectedStatusObject(fedObject, clusterStatuses)

	if rsDigestsAnnotation != "" {
		collectedStatus.SetAnnotations(map[string]string{common.LatestReplicasetDigestsAnnotation: rsDigestsAnnotation})
	}

	if existingStatus == nil {
		collectedStatus.GetLastUpdateTime().Time = time.Now()
		_, err = collectedstatusadapters.Create(
			ctx,
			s.fedClient.CoreV1alpha1(),
			collectedStatus,
			metav1.CreateOptions{},
		)
		if err != nil {
			keyedLogger.Error(err, "Failed to set annotations about replicas")
		}
	}

	if existingStatus == nil {
		collectedStatus.GetLastUpdateTime().Time = time.Now()
		_, err = collectedstatusadapters.Create(
			ctx,
			s.fedClient.CoreV1alpha1(),
			collectedStatus,
			metav1.CreateOptions{},
		)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				return worker.StatusConflict
			}
			keyedLogger.Error(err, "Failed to create collected status object")
			return worker.StatusError
		}
	} else if !reflect.DeepEqual(existingStatus.GetGenericCollectedStatus().Clusters, collectedStatus.GetGenericCollectedStatus().Clusters) ||
		!reflect.DeepEqual(collectedStatus.GetLabels(), existingStatus.GetLabels()) ||
		(rsDigestsAnnotation != "" && !hasRSDigestsAnnotation) {
		collectedStatus.GetLastUpdateTime().Time = time.Now()
		existingStatus.GetGenericCollectedStatus().Clusters = collectedStatus.GetGenericCollectedStatus().Clusters
		existingStatus.SetLabels(collectedStatus.GetLabels())
		anns := existingStatus.GetAnnotations()
		if anns == nil {
			anns = make(map[string]string)
		}
		for key, value := range collectedStatus.GetAnnotations() {
			anns[key] = value
		}
		existingStatus.SetAnnotations(anns)
		_, err = collectedstatusadapters.Update(ctx, s.fedClient.CoreV1alpha1(), existingStatus, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			keyedLogger.Error(err, "Failed to update status object")
			return worker.StatusError
		}
	}

	return worker.StatusAllOK
}

func (s *StatusController) enqueueEnableCollectedStatusObject(qualifiedName common.QualifiedName, delay time.Duration) {
	keyedLogger := s.logger.WithValues("federated-name", qualifiedName.String())

	fedObject, err := fedobjectadapters.GetFromLister(
		s.fedObjectInformer.Lister(),
		s.clusterFedObjectInformer.Lister(),
		qualifiedName.Namespace, qualifiedName.Name,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return
		} else {
			keyedLogger.Error(err, "Failed to get federated object from cache")
			return
		}
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		// enqueue to delete reference collectedstatus object
		s.worker.Enqueue(qualifiedName)
		return
	}

	templateMetadata, err := fedObject.GetSpec().GetTemplateMetadata()
	if err != nil {
		keyedLogger.Error(err, "Failed to get template gvk")
		return
	}
	templateGVK := templateMetadata.GroupVersionKind()

	typeConfig, exists := s.ftcManager.GetResourceFTC(templateGVK)
	if !exists || typeConfig == nil {
		keyedLogger.V(3).Info("Resource ftc not found")
		return
	}

	if typeConfig.Spec.StatusCollection == nil || !typeConfig.Spec.StatusCollection.Enabled {
		return
	}

	s.worker.EnqueueWithDelay(qualifiedName, delay)
}

func (s *StatusController) clusterNames() ([]string, error) {
	clusters, err := s.fedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	clusterNames := []string{}
	for _, cluster := range clusters {
		clusterNames = append(clusterNames, cluster.Name)
	}

	return clusterNames, nil
}

// clusterStatuses returns the resource status in member cluster.
func (s *StatusController) clusterStatuses(
	ctx context.Context,
	fedObject fedcorev1a1.GenericFederatedObject,
	targetQualifiedName common.QualifiedName,
	targetGVK schema.GroupVersionKind,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	clusterNames []string,
) []fedcorev1a1.CollectedFieldsWithCluster {
	clusterStatus := []fedcorev1a1.CollectedFieldsWithCluster{}
	keyedLogger := klog.FromContext(ctx)
	// collect errors during status collection and record them as event
	var errList []string

	for _, clusterName := range clusterNames {
		startTime := time.Now()
		resourceClusterStatus := fedcorev1a1.CollectedFieldsWithCluster{Cluster: clusterName}

		clusterObj, exist, err := informermanager.GetClusterObject(
			ctx,
			s.ftcManager,
			s.fedInformerManager,
			clusterName,
			targetQualifiedName,
			targetGVK,
		)
		if err != nil {
			keyedLogger.WithValues("cluster-name", clusterName).Error(err, "Failed to get object from cluster")
			errMsg := fmt.Sprintf("Failed to get object from cluster, error info: %s", err.Error())
			resourceClusterStatus.Error = errMsg
			clusterStatus = append(clusterStatus, resourceClusterStatus)
			errList = append(errList, fmt.Sprintf("cluster-name: %s, error-info: %s", clusterName, errMsg))
			s.recordStatusCollectionError(targetQualifiedName.Name, targetQualifiedName.Namespace, clusterName, targetGVK)
			continue
		}
		if !exist {
			continue
		}

		collectedFields := map[string]interface{}{}
		failedFields := []string{}

		if typeConfig.Spec.StatusCollection != nil {
			for _, field := range typeConfig.Spec.StatusCollection.Fields {
				fieldVal, found, err := unstructured.NestedFieldCopy(
					clusterObj.Object,
					strings.Split(field, ".")...)
				if err != nil || !found {
					keyedLogger.WithValues("status-field", field, "cluster-name", clusterName).
						Error(err, "Failed to get status field value")
					if err != nil {
						failedFields = append(failedFields, fmt.Sprintf("%s: %s", field, err.Error()))
					} else {
						failedFields = append(failedFields, fmt.Sprintf("%s: not found", field))
					}
					continue
				}

				err = unstructured.SetNestedField(collectedFields, fieldVal, strings.Split(field, ".")...)
				if err != nil {
					keyedLogger.WithValues("status-field", field, "cluster-name", clusterName).
						Error(err, "Failed to set status field value")
					continue
				}
			}
		}

		collectedFieldsBytes, err := json.Marshal(collectedFields)
		if err != nil {
			keyedLogger.WithValues("cluster-name", clusterName).
				Error(err, "Failed to marshal collected fields")
			s.recordStatusCollectionError(targetQualifiedName.Name, targetQualifiedName.Namespace, clusterName, targetGVK)
			continue
		}

		resourceClusterStatus.CollectedFields = apiextensionsv1.JSON{Raw: collectedFieldsBytes}
		if len(failedFields) > 0 {
			sort.Slice(failedFields, func(i, j int) bool {
				return failedFields[i] < failedFields[j]
			})
			resourceClusterStatus.Error = fmt.Sprintf(
				"Failed to get those fields: %s",
				strings.Join(failedFields, ", "),
			)
			errList = append(
				errList,
				fmt.Sprintf("cluster-name: %s, error-info: %s", clusterName, resourceClusterStatus.Error),
			)
		}
		clusterStatus = append(clusterStatus, resourceClusterStatus)
		s.metrics.Duration(metrics.StatusCollectionDuration, startTime, []stats.Tag{
			{Name: "name", Value: targetQualifiedName.Name},
			{Name: "namespace", Value: targetQualifiedName.Namespace},
			{Name: "cluster", Value: clusterName},
			{Name: "group", Value: targetGVK.Group},
			{Name: "version", Value: targetGVK.Version},
			{Name: "kind", Value: targetGVK.Kind},
		}...)
	}

	if len(errList) != 0 {
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning, EventReasonGetObjectStatusError,
			fmt.Sprintf("Failed to get some cluster status, error info: %s", strings.Join(errList, ". ")),
		)
	}

	sort.Slice(clusterStatus, func(i, j int) bool {
		return clusterStatus[i].Cluster < clusterStatus[j].Cluster
	})
	return clusterStatus
}

func (s *StatusController) recordStatusCollectionError(name, namespace, cluster string, targetGVK schema.GroupVersionKind) {
	s.metrics.Counter(metrics.StatusCollectionErrorTotal, 1, []stats.Tag{
		{Name: "name", Value: name},
		{Name: "namespace", Value: namespace},
		{Name: "cluster", Value: cluster},
		{Name: "group", Value: targetGVK.Group},
		{Name: "version", Value: targetGVK.Version},
		{Name: "kind", Value: targetGVK.Kind},
	}...)
}

// latestReplicasetDigests returns digests of latest replicaSets in member cluster
func (s *StatusController) latestReplicasetDigests(
	ctx context.Context,
	clusterNames []string,
	targetQualifiedName common.QualifiedName,
	targetGVK schema.GroupVersionKind,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) ([]util.LatestReplicasetDigest, error) {
	key := targetQualifiedName.String()
	digests := []util.LatestReplicasetDigest{}
	targetKind := typeConfig.Spec.SourceType.Kind
	keyedLogger := klog.FromContext(ctx)

	for _, clusterName := range clusterNames {
		clusterObj, exist, err := informermanager.GetClusterObject(
			ctx,
			s.ftcManager,
			s.fedInformerManager,
			clusterName,
			targetQualifiedName,
			targetGVK,
		)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get %s %q from cluster %q", targetKind, key, clusterName)
		}
		if !exist {
			continue
		}

		digest, errs := util.LatestReplicasetDigestFromObject(clusterName, clusterObj)

		if len(errs) == 0 {
			digests = append(digests, digest)
		} else {
			errList := make([]string, len(errs))
			for i := range errs {
				errList[i] = errs[i].Error()
			}
			// use a higher log level since replicaset digests are currently not a supported feature
			keyedLogger.WithValues("cluster-name", clusterName, "error-info", strings.Join(errList, ",")).
				V(4).Info("Failed to get replicaset digest from cluster")
		}
	}

	sort.Slice(digests, func(i, j int) bool {
		return digests[i].ClusterName < digests[j].ClusterName
	})
	return digests, nil
}

func newCollectedStatusObject(
	fedObj fedcorev1a1.GenericFederatedObject,
	clusterStatus []fedcorev1a1.CollectedFieldsWithCluster,
) fedcorev1a1.GenericCollectedStatusObject {
	var colletcedStatusObj fedcorev1a1.GenericCollectedStatusObject

	if fedObj.GetNamespace() == "" {
		colletcedStatusObj = &fedcorev1a1.ClusterCollectedStatus{}
	} else {
		colletcedStatusObj = &fedcorev1a1.CollectedStatus{}
	}

	colletcedStatusObj.SetName(fedObj.GetName())
	colletcedStatusObj.SetNamespace(fedObj.GetNamespace())
	colletcedStatusObj.SetLabels(fedObj.GetLabels())

	fedGVK := fedcorev1a1.SchemeGroupVersion.WithKind(reflect.TypeOf(fedObj).Elem().Name())
	colletcedStatusObj.SetOwnerReferences(
		[]metav1.OwnerReference{*metav1.NewControllerRef(fedObj, fedGVK)},
	)

	colletcedStatusObj.GetGenericCollectedStatus().Clusters = clusterStatus
	return colletcedStatusObj
}
