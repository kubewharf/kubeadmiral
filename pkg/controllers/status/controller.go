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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	allClustersKey = "ALL_CLUSTERS"
)

// KubeFedStatusController collects the status of resources in member
// clusters.
type KubeFedStatusController struct {
	// For triggering reconciliation of all target resources. This is
	// used when a new cluster becomes available.
	clusterDeliverer *delayingdeliver.DelayingDeliverer

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

	fedNamespace string
	metrics      stats.Metrics
}

// StartKubeFedStatusController starts a new status controller for a type config
func StartKubeFedStatusController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) error {
	controller, err := newKubeFedStatusController(controllerConfig, typeConfig)
	if err != nil {
		return err
	}
	if controllerConfig.MinimizeLatency {
		controller.minimizeLatency()
	}
	klog.Infof(fmt.Sprintf("Starting status controller for %q", typeConfig.GetFederatedType().Kind))
	controller.Run(stopChan)
	return nil
}

// newKubeFedStatusController returns a new status controller for the federated type
func newKubeFedStatusController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) (*KubeFedStatusController, error) {
	federatedAPIResource := typeConfig.GetFederatedType()
	statusAPIResource := typeConfig.GetStatusType()
	if statusAPIResource == nil {
		return nil, errors.Errorf("Status collection is not supported for %q", federatedAPIResource.Kind)
	}
	userAgent := fmt.Sprintf("%s-federate-status-controller", strings.ToLower(statusAPIResource.Kind))
	configCopy := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configCopy, userAgent)
	client := genericclient.NewForConfigOrDieWithUserAgent(controllerConfig.KubeConfig, userAgent)

	federatedTypeClient, err := util.NewResourceClient(controllerConfig.KubeConfig, &federatedAPIResource)
	if err != nil {
		return nil, err
	}

	statusClient, err := util.NewResourceClient(controllerConfig.KubeConfig, statusAPIResource)
	if err != nil {
		return nil, err
	}

	s := &KubeFedStatusController{
		clusterAvailableDelay:         controllerConfig.ClusterAvailableDelay,
		clusterUnavailableDelay:       controllerConfig.ClusterUnavailableDelay,
		reconcileOnClusterChangeDelay: time.Second * 3,
		memberObjectEnqueueDelay:      time.Second * 10,
		typeConfig:                    typeConfig,
		client:                        client,
		statusClient:                  statusClient,
		fedNamespace:                  controllerConfig.FedSystemNamespace,
		metrics:                       controllerConfig.Metrics,
	}

	s.worker = worker.NewReconcileWorker(
		s.reconcile,
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags("status-worker", typeConfig.GetTargetType().Kind),
	)

	// Build deliverer for triggering cluster reconciliations.
	s.clusterDeliverer = delayingdeliver.NewDelayingDeliverer()

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
	klog.Infof("Status controller %q NewFederatedInformer", federatedAPIResource.Kind)

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
				s.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(s.clusterAvailableDelay))
			},
			// When a cluster becomes unavailable process all the target resources again.
			ClusterUnavailable: func(cluster *fedcorev1a1.FederatedCluster, _ []interface{}) {
				s.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(s.clusterUnavailableDelay))
			},
		},
	)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// minimizeLatency reduces delays and timeouts to make the controller more responsive (useful for testing).
func (s *KubeFedStatusController) minimizeLatency() {
	s.clusterAvailableDelay = time.Second
	s.clusterUnavailableDelay = time.Second
	s.reconcileOnClusterChangeDelay = 20 * time.Millisecond
	s.memberObjectEnqueueDelay = 50 * time.Millisecond
}

// Run runs the status controller
func (s *KubeFedStatusController) Run(stopChan <-chan struct{}) {
	go s.clusterDeliverer.RunMetricLoop(stopChan, 30*time.Second, s.metrics,
		delayingdeliver.NewMetricTags("status-clusterDeliverer", s.typeConfig.GetTargetType().Kind))
	go s.federatedController.Run(stopChan)
	go s.statusController.Run(stopChan)
	s.informer.Start()
	s.clusterDeliverer.StartWithHandler(func(_ *delayingdeliver.DelayingDelivererItem) {
		s.reconcileOnClusterChange()
	})

	s.worker.Run(stopChan)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		s.informer.Stop()
		s.clusterDeliverer.Stop()
	}()
}

// Check whether all data stores are in sync. False is returned if any of the informer/stores is not yet
// synced with the corresponding api server.
func (s *KubeFedStatusController) HasSynced() bool {
	if !s.informer.ClustersSynced() {
		klog.V(2).Infof("Cluster list not synced")
		return false
	}
	if !s.federatedController.HasSynced() {
		klog.V(2).Infof("Federated type not synced")
		return false
	}
	if !s.statusController.HasSynced() {
		klog.V(2).Infof("Status not synced")
		return false
	}

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
func (s *KubeFedStatusController) reconcileOnClusterChange() {
	if !s.HasSynced() {
		s.clusterDeliverer.DeliverAt(allClustersKey, nil, time.Now().Add(s.clusterAvailableDelay))
	}
	for _, obj := range s.federatedStore.List() {
		qualifiedName := common.NewQualifiedName(obj.(pkgruntime.Object))
		s.worker.EnqueueWithDelay(qualifiedName, s.reconcileOnClusterChangeDelay)
	}
}

func (s *KubeFedStatusController) reconcile(qualifiedName common.QualifiedName) worker.Result {
	if !s.HasSynced() {
		return worker.Result{RequeueAfter: &s.clusterAvailableDelay}
	}

	federatedKind := s.typeConfig.GetFederatedType().Kind
	targetType := s.typeConfig.GetTargetType()
	targetIsDeployment := schemautil.APIResourceToGVK(&targetType) == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind)
	statusKind := s.typeConfig.GetStatusType().Kind
	key := qualifiedName.String()

	s.metrics.Rate("status.throughput", 1)
	klog.V(4).Infof("Starting to reconcile %v %v", statusKind, key)
	startTime := time.Now()
	defer func() {
		s.metrics.Duration("status.latency", startTime)
		klog.V(4).Infof("Finished reconciling %v %v (duration: %v)", statusKind, key, time.Since(startTime))
	}()

	fedObject, err := s.objFromCache(s.federatedStore, federatedKind, key)
	if err != nil {
		return worker.StatusError
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		klog.V(4).Infof("No federated type for %v %v found, about to delete status object", federatedKind, key)
		err = s.statusClient.Resources(qualifiedName.Namespace).
			Delete(context.TODO(), qualifiedName.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	clusterNames, err := s.clusterNames()
	if err != nil {
		runtime.HandleError(errors.Wrap(err, "Failed to get cluster list"))
		return worker.Result{RequeueAfter: &s.clusterAvailableDelay}
	}

	clusterStatus, err := s.clusterStatuses(clusterNames, key)
	if err != nil {
		return worker.StatusError
	}

	existingStatus, err := s.objFromCache(s.statusStore, statusKind, key)
	if err != nil {
		return worker.StatusError
	}

	var rsDigestsAnnotation string
	if targetIsDeployment {
		latestReplicasetDigests, err := s.latestReplicasetDigests(clusterNames, key)
		if err != nil {
			return worker.StatusError
		}
		rsDigestsAnnotationBytes, err := json.Marshal(latestReplicasetDigests)
		if err != nil {
			return worker.StatusError
		}
		rsDigestsAnnotation = string(rsDigestsAnnotationBytes)
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
			&federatedResource,
			fedObject,
			clusterNames,
			key,
		)
		if err != nil {
			return worker.StatusError
		}
	}

	status, err := util.GetUnstructured(federatedResource)
	if err != nil {
		klog.Errorf("Failed to convert to Unstructured: %s %q: %v", statusKind, key, err)
		return worker.StatusError
	}

	if existingStatus == nil {
		_, err = s.statusClient.Resources(qualifiedName.Namespace).
			Create(context.TODO(), status, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				return worker.StatusConflict
			}
			runtime.HandleError(
				errors.Wrapf(err, "Failed to create status object for federated type %s %q", statusKind, key),
			)
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
			runtime.HandleError(errors.Wrapf(err, "Failed to update status object for federated type %s %q", statusKind, key))
			return worker.StatusError
		}
	}

	return worker.StatusAllOK
}

func (s *KubeFedStatusController) rawObjFromCache(store cache.Store, kind, key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := store.GetByKey(key)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "Failed to query %s store for %q", kind, key)
		runtime.HandleError(wrappedErr)
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}

func (s *KubeFedStatusController) objFromCache(
	store cache.Store,
	kind, key string,
) (*unstructured.Unstructured, error) {
	obj, err := s.rawObjFromCache(store, kind, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*unstructured.Unstructured), nil
}

func (s *KubeFedStatusController) clusterNames() ([]string, error) {
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
func (s *KubeFedStatusController) clusterStatuses(
	clusterNames []string,
	key string,
) ([]util.ResourceClusterStatus, error) {
	clusterStatus := []util.ResourceClusterStatus{}

	targetKind := s.typeConfig.GetTargetType().Kind
	for _, clusterName := range clusterNames {
		clusterObj, exist, err := s.informer.GetTargetStore().GetByKey(clusterName, key)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "Failed to get %s %q from cluster %q", targetKind, key, clusterName)
			runtime.HandleError(wrappedErr)
			return nil, wrappedErr
		}
		if !exist {
			continue
		}

		resourceClusterStatus := util.ResourceClusterStatus{ClusterName: clusterName}
		collectedFields := map[string]interface{}{}

		unstructuredClusterObj := clusterObj.(*unstructured.Unstructured)

		if s.typeConfig.Spec.StatusCollection != nil {
			for _, field := range s.typeConfig.Spec.StatusCollection.Fields {
				fieldVal, found, err := unstructured.NestedFieldCopy(
					unstructuredClusterObj.Object,
					strings.Split(field, ".")...)
				if err != nil || !found {
					wrappedErr := errors.Wrapf(
						err,
						"Failed to get status field value %s of cluster resource object %s %q for cluster %q",
						field,
						targetKind,
						key,
						clusterName,
					)
					runtime.HandleError(wrappedErr)
					continue
				}

				err = unstructured.SetNestedField(collectedFields, fieldVal, strings.Split(field, ".")...)
				if err != nil {
					return nil, err
				}
			}
		}

		resourceClusterStatus.CollectedFields = collectedFields
		clusterStatus = append(clusterStatus, resourceClusterStatus)
	}

	sort.Slice(clusterStatus, func(i, j int) bool {
		return clusterStatus[i].ClusterName < clusterStatus[j].ClusterName
	})
	return clusterStatus, nil
}

// latestReplicasetDigests returns digests of latest replicaSets in member cluster
func (s *KubeFedStatusController) latestReplicasetDigests(
	clusterNames []string,
	key string,
) ([]util.LatestReplicasetDigest, error) {
	digests := []util.LatestReplicasetDigest{}
	targetKind := s.typeConfig.GetTargetType().Kind

	for _, clusterName := range clusterNames {
		obj, exist, err := s.informer.GetTargetStore().GetByKey(clusterName, key)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "Failed to get %s %q from cluster %q", targetKind, key, clusterName)
			runtime.HandleError(wrappedErr)
			return nil, wrappedErr
		}
		if !exist {
			continue
		}

		utd := obj.(*unstructured.Unstructured)
		digest, errs := util.LatestReplicasetDigestFromObject(clusterName, utd)

		if len(errs) == 0 {
			digests = append(digests, digest)
		} else {
			for _, err := range errs {
				runtime.HandleError(err)
			}
		}
	}

	sort.Slice(digests, func(i, j int) bool {
		return digests[i].ClusterName < digests[j].ClusterName
	})
	return digests, nil
}

func (s *KubeFedStatusController) realUpdatedReplicas(clusterNames []string, key, revision string) (string, error) {
	var updatedReplicas int64
	targetKind := s.typeConfig.GetTargetType().Kind

	for _, clusterName := range clusterNames {
		obj, exist, err := s.informer.GetTargetStore().GetByKey(clusterName, key)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "Failed to get %s %q from cluster %q", targetKind, key, clusterName)
			runtime.HandleError(wrappedErr)
			return "", wrappedErr
		}
		if !exist {
			continue
		}
		utd := obj.(*unstructured.Unstructured)
		// ignore digest errors for now since we want to try the best to collect the status
		digest, err := util.ReplicaSetDigestFromObject(utd)
		if err != nil {
			klog.Errorf("failed to get digest for %s in %s: %v", key, clusterName, err)
			continue
		}
		klog.V(4).Infof("%s in %s, replicas digest: %v", key, clusterName, digest)
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

func (s *KubeFedStatusController) setReplicasAnnotations(
	federatedResource *util.FederatedResource,
	fedObject *unstructured.Unstructured,
	clusterNames []string,
	key string,
) (bool, error) {
	revision, ok := fedObject.GetAnnotations()[common.CurrentRevisionAnnotation]
	if !ok {
		return false, nil
	}
	updatedReplicas, err := s.realUpdatedReplicas(clusterNames, key, revision)
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
