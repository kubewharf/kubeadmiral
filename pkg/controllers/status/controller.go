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
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	StatusControllerName = "status-controller"
)

const (
	EventReasonGetObjectStatusError = "GetObjectStatusError"
)

// StatusController collects the status of resources in member clusters.
type StatusController struct {
	name string

	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterQueue workqueue.DelayingInterface

	// Informer for resources in member clusters
	informer util.FederatedInformer

	// Store for the federated type
	federatedStore cache.Store
	// Informer for the federated type
	federatedController cache.Controller

	// Store for the status of the federated type
	statusStore cache.Store
	// Informer for the status of the federated type
	statusController cache.Controller

	worker worker.ReconcileWorker

	clusterAvailableDelay         time.Duration
	clusterUnavailableDelay       time.Duration
	reconcileOnClusterChangeDelay time.Duration
	memberObjectEnqueueDelay      time.Duration

	typeConfig *fedcorev1a1.FederatedTypeConfig

	client       genericclient.Client
	statusClient util.ResourceClient

	fedNamespace  string
	metrics       stats.Metrics
	logger        klog.Logger
	eventRecorder record.EventRecorder
}

// StartStatusController starts a new status controller for a type config
func StartStatusController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) error {
	controller, err := newStatusController(controllerConfig, typeConfig)
	if err != nil {
		return err
	}
	if controllerConfig.MinimizeLatency {
		controller.minimizeLatency()
	}
	controller.logger.Info("Starting status controller")
	controller.Run(stopChan)
	return nil
}

// newStatusController returns a new status controller for the federated type
func newStatusController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) (*StatusController, error) {
	federatedAPIResource := typeConfig.GetFederatedType()
	statusAPIResource := typeConfig.GetStatusType()
	if statusAPIResource == nil {
		return nil, errors.Errorf("Status collection is not supported for %q", federatedAPIResource.Kind)
	}
	userAgent := fmt.Sprintf("%s-federate-status-controller", strings.ToLower(statusAPIResource.Kind))
	configCopy := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configCopy, userAgent)
	kubeClient, err := kubeclient.NewForConfig(configCopy)
	if err != nil {
		return nil, err
	}
	client := genericclient.NewForConfigOrDieWithUserAgent(controllerConfig.KubeConfig, userAgent)

	federatedTypeClient, err := util.NewResourceClient(controllerConfig.KubeConfig, &federatedAPIResource)
	if err != nil {
		return nil, err
	}

	statusClient, err := util.NewResourceClient(controllerConfig.KubeConfig, statusAPIResource)
	if err != nil {
		return nil, err
	}

	logger := klog.LoggerWithValues(klog.Background(), "controller", StatusControllerName,
		"ftc", typeConfig.Name, "status-kind", typeConfig.GetStatusType().Kind)

	s := &StatusController{
		name:                          userAgent,
		clusterAvailableDelay:         controllerConfig.ClusterAvailableDelay,
		clusterUnavailableDelay:       controllerConfig.ClusterUnavailableDelay,
		reconcileOnClusterChangeDelay: time.Second * 3,
		memberObjectEnqueueDelay:      time.Second * 10,
		typeConfig:                    typeConfig,
		client:                        client,
		statusClient:                  statusClient,
		fedNamespace:                  controllerConfig.FedSystemNamespace,
		metrics:                       controllerConfig.Metrics,
		logger:                        logger,
		eventRecorder:                 eventsink.NewDefederatingRecorderMux(kubeClient, StatusControllerName, 6),
	}

	s.worker = worker.NewReconcileWorker(
		s.reconcile,
		worker.RateLimiterOptions{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags("status-worker", typeConfig.GetTargetType().Kind),
	)

	// Build queue for triggering cluster reconciliations.
	s.clusterQueue = workqueue.NewNamedDelayingQueue("status-controller-cluster-queue")

	// Start informers on the resources for the federated type
	enqueueObj := s.worker.EnqueueObject

	targetNamespace := controllerConfig.TargetNamespace

	s.federatedStore, s.federatedController = util.NewResourceInformer(
		federatedTypeClient,
		targetNamespace,
		enqueueObj,
		controllerConfig.Metrics,
	)
	s.statusStore, s.statusController = util.NewResourceInformer(
		statusClient,
		targetNamespace,
		enqueueObj,
		controllerConfig.Metrics,
	)
	logger.Info("Creating new FederatedInformer")

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
				s.clusterQueue.AddAfter(struct{}{}, s.clusterAvailableDelay)
			},
			// When a cluster becomes unavailable process all the target resources again.
			ClusterUnavailable: func(cluster *fedcorev1a1.FederatedCluster, _ []interface{}) {
				s.clusterQueue.AddAfter(struct{}{}, s.clusterUnavailableDelay)
			},
		},
	)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// minimizeLatency reduces delays and timeouts to make the controller more responsive (useful for testing).
func (s *StatusController) minimizeLatency() {
	s.clusterAvailableDelay = time.Second
	s.clusterUnavailableDelay = time.Second
	s.reconcileOnClusterChangeDelay = 20 * time.Millisecond
	s.memberObjectEnqueueDelay = 50 * time.Millisecond
}

// Run runs the status controller
func (s *StatusController) Run(stopChan <-chan struct{}) {
	go s.federatedController.Run(stopChan)
	go s.statusController.Run(stopChan)
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

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		s.informer.Stop()
		s.clusterQueue.ShutDown()
	}()
}

// Check whether all data stores are in sync. False is returned if any of the informer/stores is not yet
// synced with the corresponding api server.
func (s *StatusController) HasSynced() bool {
	if !s.informer.ClustersSynced() {
		s.logger.V(3).Info("Cluster list not synced")
		return false
	}
	if !s.federatedController.HasSynced() {
		s.logger.V(3).Info("Federated type not synced")
		return false
	}
	if !s.statusController.HasSynced() {
		s.logger.V(3).Info("Status not synced")
		return false
	}
	return true
}

// The function triggers reconciliation of all target federated resources.
func (s *StatusController) reconcileOnClusterChange() {
	for _, obj := range s.federatedStore.List() {
		qualifiedName := common.NewQualifiedName(obj.(pkgruntime.Object))
		s.worker.EnqueueWithDelay(qualifiedName, s.reconcileOnClusterChangeDelay)
	}
}

func (s *StatusController) reconcile(qualifiedName common.QualifiedName) (reconcileStatus worker.Result) {
	targetType := s.typeConfig.GetTargetType()
	targetIsDeployment := schemautil.APIResourceToGVK(&targetType) == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind)
	statusKind := s.typeConfig.GetStatusType().Kind
	key := qualifiedName.String()
	keyedLogger := s.logger.WithValues("object", key)
	ctx := klog.NewContext(context.TODO(), keyedLogger)

	s.metrics.Rate("status.throughput", 1)
	keyedLogger.V(3).Info("Starting reconcile")
	startTime := time.Now()
	defer func() {
		s.metrics.Duration("status.latency", startTime)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", reconcileStatus.String()).
			V(3).Info("Finished reconcile")
	}()

	fedObject, err := s.objFromCache(s.federatedStore, key)
	if err != nil {
		keyedLogger.Error(err, "Failed to get federated object from cache")
		return worker.StatusError
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		keyedLogger.V(1).Info("No federated type found, deleting status object")
		err = s.statusClient.Resources(qualifiedName.Namespace).
			Delete(ctx, qualifiedName.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	clusterNames, err := s.clusterNames()
	if err != nil {
		keyedLogger.Error(err, "Failed to get cluster list")
		return worker.Result{RequeueAfter: &s.clusterAvailableDelay}
	}

	clusterStatus := s.clusterStatuses(ctx, fedObject, clusterNames, qualifiedName)

	existingStatus, err := s.objFromCache(s.statusStore, key)
	if err != nil {
		keyedLogger.Error(err, "Failed to get status from cache")
		return worker.StatusError
	}

	var rsDigestsAnnotation string
	if targetIsDeployment {
		latestReplicasetDigests, err := s.latestReplicasetDigests(ctx, clusterNames, qualifiedName)
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
			util.LatestReplicasetDigestsAnnotation,
			rsDigestsAnnotation,
		)
		if err != nil {
			return worker.StatusError
		}
	}

	resourceGroupVersion := schema.GroupVersion{
		Group:   s.typeConfig.GetStatusType().Group,
		Version: s.typeConfig.GetStatusType().Version,
	}
	federatedResource := util.FederatedResource{
		TypeMeta: metav1.TypeMeta{
			Kind:       statusKind,
			APIVersion: resourceGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      qualifiedName.Name,
			Namespace: qualifiedName.Namespace,
			// Add ownership of status object to corresponding
			// federated object, so that status object is deleted when
			// the federated object is deleted.
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: fedObject.GetAPIVersion(),
				Kind:       fedObject.GetKind(),
				Name:       fedObject.GetName(),
				UID:        fedObject.GetUID(),
			}},
			Labels: fedObject.GetLabels(),
		},
		ClusterStatus: clusterStatus,
	}
	if rsDigestsAnnotation != "" {
		federatedResource.Annotations = map[string]string{util.LatestReplicasetDigestsAnnotation: rsDigestsAnnotation}
	}
	replicasAnnotationUpdated := false
	if targetIsDeployment {
		replicasAnnotationUpdated, err = s.setReplicasAnnotations(
			ctx,
			&federatedResource,
			fedObject,
			clusterNames,
			qualifiedName,
		)
		if err != nil {
			keyedLogger.Error(err, "Failed to set annotations about replicas")
		}
	}

	status, err := util.GetUnstructured(federatedResource)
	if err != nil {
		keyedLogger.Error(err, "Failed to convert to unstructured")
		return worker.StatusError
	}

	if existingStatus == nil {
		_, err = s.statusClient.Resources(qualifiedName.Namespace).
			Create(context.TODO(), status, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				return worker.StatusConflict
			}
			keyedLogger.Error(err, "Failed to create status object")
			return worker.StatusError
		}
	} else if !reflect.DeepEqual(existingStatus.Object["clusterStatus"], status.Object["clusterStatus"]) ||
		!reflect.DeepEqual(status.GetLabels(), existingStatus.GetLabels()) ||
		replicasAnnotationUpdated ||
		(rsDigestsAnnotation != "" && !hasRSDigestsAnnotation) {
		if status.Object["clusterStatus"] == nil {
			status.Object["clusterStatus"] = make([]util.ResourceClusterStatus, 0)
		}
		existingStatus.Object["clusterStatus"] = status.Object["clusterStatus"]
		existingStatus.SetLabels(status.GetLabels())
		anns := existingStatus.GetAnnotations()
		if anns == nil {
			anns = make(map[string]string)
		}
		for key, value := range federatedResource.GetAnnotations() {
			anns[key] = value
		}
		existingStatus.SetAnnotations(anns)
		_, err = s.statusClient.Resources(qualifiedName.Namespace).Update(context.TODO(), existingStatus, metav1.UpdateOptions{})
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

func (s *StatusController) rawObjFromCache(store cache.Store, key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := store.GetByKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to query store for %q, err info: %w", key, err)
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}

func (s *StatusController) objFromCache(
	store cache.Store,
	key string,
) (*unstructured.Unstructured, error) {
	obj, err := s.rawObjFromCache(store, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*unstructured.Unstructured), nil
}

func (s *StatusController) clusterNames() ([]string, error) {
	clusters, err := s.informer.GetReadyClusters()
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
	fedObject *unstructured.Unstructured,
	clusterNames []string,
	qualifiedName common.QualifiedName,
) []util.ResourceClusterStatus {
	clusterStatus := []util.ResourceClusterStatus{}
	keyedLogger := klog.FromContext(ctx)
	// collect errors during status collection and record them as event
	errList := []string{}

	for _, clusterName := range clusterNames {
		resourceClusterStatus := util.ResourceClusterStatus{ClusterName: clusterName}

		clusterObj, exist, err := util.GetClusterObject(
			ctx,
			s.informer,
			clusterName,
			qualifiedName,
			s.typeConfig.GetTargetType(),
		)
		if err != nil {
			keyedLogger.WithValues("cluster-name", clusterName).Error(err, "Failed to get object from cluster")
			errMsg := fmt.Sprintf("Failed to get object from cluster, error info: %s", err.Error())
			resourceClusterStatus.Error = errMsg
			clusterStatus = append(clusterStatus, resourceClusterStatus)
			errList = append(errList, fmt.Sprintf("cluster-name: %s, error-info: %s", clusterName, errMsg))
			continue
		}
		if !exist {
			continue
		}

		collectedFields := map[string]interface{}{}
		failedFields := []string{}

		if s.typeConfig.Spec.StatusCollection != nil {
			for _, field := range s.typeConfig.Spec.StatusCollection.Fields {
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

		resourceClusterStatus.CollectedFields = collectedFields
		if len(failedFields) > 0 {
			sort.Slice(failedFields, func(i, j int) bool {
				return failedFields[i] < failedFields[j]
			})
			resourceClusterStatus.Error = fmt.Sprintf("Failed to get those fields: %s", strings.Join(failedFields, ", "))
			errList = append(errList, fmt.Sprintf("cluster-name: %s, error-info: %s", clusterName, resourceClusterStatus.Error))
		}
		clusterStatus = append(clusterStatus, resourceClusterStatus)
	}

	if len(errList) != 0 {
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning, EventReasonGetObjectStatusError,
			fmt.Sprintf("Failed to get some cluster status, error info: %s", strings.Join(errList, ". ")),
		)
	}

	sort.Slice(clusterStatus, func(i, j int) bool {
		return clusterStatus[i].ClusterName < clusterStatus[j].ClusterName
	})
	return clusterStatus
}

// latestReplicasetDigests returns digests of latest replicaSets in member cluster
func (s *StatusController) latestReplicasetDigests(
	ctx context.Context,
	clusterNames []string,
	qualifiedName common.QualifiedName,
) ([]util.LatestReplicasetDigest, error) {
	key := qualifiedName.String()
	digests := []util.LatestReplicasetDigest{}
	targetKind := s.typeConfig.GetTargetType().Kind
	keyedLogger := klog.FromContext(ctx)

	for _, clusterName := range clusterNames {
		clusterObj, exist, err := util.GetClusterObject(
			ctx,
			s.informer,
			clusterName,
			qualifiedName,
			s.typeConfig.GetTargetType(),
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

func (s *StatusController) realUpdatedReplicas(
	ctx context.Context,
	clusterNames []string,
	qualifiedName common.QualifiedName,
	revision string,
) (string, error) {
	key := qualifiedName.String()
	var updatedReplicas int64
	targetKind := s.typeConfig.GetTargetType().Kind
	keyedLogger := klog.FromContext(ctx)

	for _, clusterName := range clusterNames {
		clusterObj, exist, err := util.GetClusterObject(
			ctx,
			s.informer,
			clusterName,
			qualifiedName,
			s.typeConfig.GetTargetType(),
		)
		if err != nil {
			return "", errors.Wrapf(err, "Failed to get %s %q from cluster %q", targetKind, key, clusterName)
		}
		if !exist {
			continue
		}
		// ignore digest errors for now since we want to try the best to collect the status
		digest, err := util.ReplicaSetDigestFromObject(clusterObj)
		if err != nil {
			keyedLogger.WithValues("cluster-name", clusterName).Error(err, "Failed to get latestreplicaset digest")
			continue
		}
		keyedLogger.WithValues("cluster-name", clusterName, "replicas-digest", digest).V(4).Info("Got latestreplicaset digest")
		if digest.CurrentRevision != revision {
			continue
		}
		if digest.ObservedGeneration < digest.Generation {
			continue
		}
		updatedReplicas += digest.UpdatedReplicas
	}
	return strconv.FormatInt(updatedReplicas, 10), nil
}

func (s *StatusController) setReplicasAnnotations(
	ctx context.Context,
	federatedResource *util.FederatedResource,
	fedObject *unstructured.Unstructured,
	clusterNames []string,
	qualifedName common.QualifiedName,
) (bool, error) {
	revision, ok := fedObject.GetAnnotations()[common.CurrentRevisionAnnotation]
	if !ok {
		return false, nil
	}
	updatedReplicas, err := s.realUpdatedReplicas(ctx, clusterNames, qualifedName, revision)
	if err != nil {
		return false, err
	}
	if federatedResource.Annotations == nil {
		federatedResource.Annotations = make(map[string]string)
	}
	federatedResource.Annotations[util.AggregatedUpdatedReplicas] = updatedReplicas
	federatedResource.Annotations[common.CurrentRevisionAnnotation] = revision
	return true, nil
}
