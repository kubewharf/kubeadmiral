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

package monitor

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
)

var (
	ErrStatusOutOfSync                = fmt.Errorf("status out of sync")
	ErrMissingObservedGenerationField = fmt.Errorf("missing observed generation")
)

var OutOfSyncRecheckDelay = 5 * time.Second

// MonitorSubController monitor the sync state of federated resources
type MonitorSubController struct {
	// name of the controller: <sourceKind>-federate-controller
	name string
	kind string
	// Store for the federated type
	federatedStore cache.Store
	// Controller for the federated type
	federatedController cache.Controller
	// Client for federated type
	federatedClient util.ResourceClient

	enableStatus bool
	// Store for the status of the federated type
	statusStore cache.Store
	// Controller for the status of the federated type
	statusController cache.Controller
	// Client for status of the federated type
	statusClient util.ResourceClient
	// Informer for resources in member clusters
	informer util.FederatedInformer

	worker     worker.ReconcileWorker
	typeConfig *fedcorev1a1.FederatedTypeConfig
	meters     *sync.Map
}

// StartMonitorSubController starts a new monitor controller to report metrics.
func StartMonitorSubController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	meters *sync.Map,
) error {
	controller, err := newMonitorSubController(controllerConfig, typeConfig, meters)
	if err != nil {
		return err
	}

	klog.Infof(fmt.Sprintf("Starting monitor subController for %q", typeConfig.GetFederatedType().Kind))
	controller.Run(stopChan)
	return nil
}

// newMonitorSubController returns a new monitor controller
func newMonitorSubController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	meters *sync.Map,
) (*MonitorSubController, error) {
	federatedTypeAPIResource := typeConfig.GetFederatedType()

	// Initialize non-dynamic clients first to avoid polluting config
	userAgent := fmt.Sprintf("%s-monitor-subcontroller", strings.ToLower(federatedTypeAPIResource.Kind))
	configCopy := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configCopy, userAgent)

	m := &MonitorSubController{
		name:       userAgent,
		kind:       federatedTypeAPIResource.Kind,
		typeConfig: typeConfig,
		meters:     meters,
	}

	var err error
	m.federatedClient, err = util.NewResourceClient(configCopy, &federatedTypeAPIResource)
	if err != nil {
		return nil, err
	}

	m.worker = worker.NewReconcileWorker(m.reconcile, worker.WorkerTiming{}, controllerConfig.WorkerCount,
		controllerConfig.Metrics, delayingdeliver.NewMetricTags("monitor-subcontroller", m.kind))

	m.federatedStore, m.federatedController = util.NewResourceInformer(m.federatedClient,
		controllerConfig.TargetNamespace, m.worker.EnqueueObject, controllerConfig.Metrics)

	m.enableStatus = typeConfig.GetStatusEnabled()

	if m.enableStatus {
		statusAPIResource := typeConfig.GetStatusType()
		m.statusClient, err = util.NewResourceClient(configCopy, statusAPIResource)
		if err != nil {
			return nil, err
		}
		m.statusStore, m.statusController = util.NewResourceInformer(m.statusClient,
			controllerConfig.TargetNamespace, m.worker.EnqueueObject, controllerConfig.Metrics)

		client := genericclient.NewForConfigOrDieWithUserAgent(configCopy, userAgent)
		targetAPIResource := typeConfig.GetTargetType()
		// Federated informer for resources in member clusters
		m.informer, err = util.NewFederatedInformer(
			controllerConfig,
			client,
			configCopy,
			&targetAPIResource,
			m.worker.EnqueueObject,
			&util.ClusterLifecycleHandlerFuncs{},
		)

	}

	klog.Infof(
		"Monitor subController %q NewResourceInformer, enable status: %t",
		federatedTypeAPIResource.Kind,
		m.enableStatus,
	)
	return m, nil
}

func (m *MonitorSubController) Run(stopChan <-chan struct{}) {
	go m.federatedController.Run(stopChan)
	if m.enableStatus {
		go m.statusController.Run(stopChan)
		m.informer.Start()
	}
	m.worker.Run(stopChan)
}

func (m *MonitorSubController) reconcile(qualifiedName common.QualifiedName) worker.Result {
	key := qualifiedName.String()
	meterKey := m.kind + "/" + qualifiedName.String()
	klog.Infof("Receive %s", meterKey)

	cachedObj, err := m.objCopyFromCache(key)
	if err != nil {
		return worker.StatusError
	}

	if cachedObj == nil {
		m.meters.Delete(meterKey)
		return worker.StatusAllOK
	}

	un := cachedObj.(*unstructured.Unstructured)
	if un.GetDeletionTimestamp() != nil {
		m.meters.Delete(meterKey)
		return worker.StatusAllOK
	}

	baseMeter := BaseMeter{}
	if v, ok := m.meters.Load(meterKey); !ok {
		baseMeter.creationTimestamp = un.GetCreationTimestamp()
	} else {
		baseMeter, ok = v.(BaseMeter)
		if !ok {
			return worker.StatusError
		}
	}

	err = m.reconcileSync(un, &baseMeter)
	if err != nil {
		return worker.StatusError
	} else {
		m.meters.Store(meterKey, baseMeter)
	}

	if m.enableStatus {
		cachedStatus, err := m.objStatusCopyFromCache(key)
		if err != nil {
			return worker.StatusError
		}

		if cachedStatus == nil {
			klog.Warningf("status %s not found", key)
			return worker.StatusAllOK
		}

		un := cachedStatus.(*unstructured.Unstructured)
		if un.GetDeletionTimestamp() != nil {
			return worker.StatusAllOK
		}

		err = m.reconcileStatus(key, &baseMeter)
		if err != nil {
			klog.Errorf(
				"reconcile %s status failed: %v, detail: lastStatus: %v, lastStatusSyncedTimestamp: %s, outOfSyncDuration: %v",
				key,
				err,
				baseMeter.lastStatus,
				baseMeter.lastStatusSyncedTimestamp,
				baseMeter.outOfSyncDuration,
			)
			if errors.Is(err, ErrStatusOutOfSync) {
				return worker.Result{RequeueAfter: &OutOfSyncRecheckDelay}
			} else if errors.Is(err, ErrMissingObservedGenerationField) {
				return worker.StatusAllOK
			}
			return worker.StatusError
		}
		klog.Infof(
			"reconcile %s status success. detail: lastStatus: %v, lastStatusSyncedTimestamp: %s, outOfSyncDuration: %v",
			key,
			baseMeter.lastStatus,
			baseMeter.lastStatusSyncedTimestamp,
			baseMeter.outOfSyncDuration,
		)
		m.meters.Store(meterKey, baseMeter)
	}
	return worker.StatusAllOK
}

func (m *MonitorSubController) reconcileSync(obj *unstructured.Unstructured, baseMeter *BaseMeter) error {
	annotations := obj.GetAnnotations()
	if v, ok := annotations[annotationutil.SyncSuccessTimestamp]; ok {
		syncSuccessTimestamp, err := time.Parse(time.RFC3339Nano, v)
		if err == nil {
			baseMeter.syncSuccessTimestamp = metav1.NewTime(syncSuccessTimestamp)
		}
	}

	generation := obj.GetGeneration()
	updateAnnotation := true
	if v, ok := annotations[annotationutil.LastGeneration]; ok {
		// generation isn't synced yet
		if strconv.FormatInt(generation, 10) != v {
			baseMeter.lastUpdateTimestamp = Now()

			lastSyncSuccessGeneration, ok := annotations[annotationutil.LastSyncSucceessGeneration]
			if ok {
				if strconv.FormatInt(generation, 10) == lastSyncSuccessGeneration {
					// it seems the sync has already happened.
					d, _ := time.ParseDuration("-10ms")
					baseMeter.lastUpdateTimestamp = metav1.NewTime(baseMeter.syncSuccessTimestamp.Add(d))
				}
			}
		} else {
			// generation is synced
			lastSyncSuccessGeneration, ok := annotations[annotationutil.LastSyncSucceessGeneration]
			if ok {
				if strconv.FormatInt(generation, 10) == lastSyncSuccessGeneration {
					// adjustment for race conditions.
					if baseMeter.lastUpdateTimestamp.After(baseMeter.syncSuccessTimestamp.Time) {
						d, _ := time.ParseDuration("-10ms")
						baseMeter.lastUpdateTimestamp = metav1.NewTime(baseMeter.syncSuccessTimestamp.Add(d))
					}
				}
			}
			updateAnnotation = false
		}
	}

	if updateAnnotation {
		annotationutil.AddAnnotation(obj, annotationutil.LastGeneration, strconv.FormatInt(generation, 10))
		m.federatedClient.Resources(obj.GetNamespace()).Update(context.TODO(), obj, metav1.UpdateOptions{})
	}
	return nil
}

func (m *MonitorSubController) reconcileStatus(key string, baseMeter *BaseMeter) error {
	fedStatus, err := m.getFedStatusObservedGeneration(key)
	if err != nil {
		return err
	}

	clusterStatus, err := m.getClusterObservedGeneration(key)
	if err != nil {
		return err
	}

	if baseMeter.lastStatus == nil {
		baseMeter.lastStatus = clusterStatus
		baseMeter.lastStatusSyncedTimestamp = Now()
		return nil
	}

	if reflect.DeepEqual(clusterStatus, fedStatus) {
		// synced
		baseMeter.outOfSyncDuration = time.Duration(0)
		baseMeter.lastStatusSyncedTimestamp = Now()
		baseMeter.lastStatus = clusterStatus
	} else {
		// out of synced
		if reflect.DeepEqual(baseMeter.lastStatus, clusterStatus) {
			// cluster status is not updated since last time
			baseMeter.outOfSyncDuration = time.Since(baseMeter.lastStatusSyncedTimestamp.Time)
		} else {
			// cluster status is updated since last time
			if baseMeter.outOfSyncDuration == time.Duration(0) {
				// first time out of synced
				baseMeter.outOfSyncDuration = time.Second
				baseMeter.lastStatusSyncedTimestamp = Now()
			} else {
				baseMeter.outOfSyncDuration = time.Since(baseMeter.lastStatusSyncedTimestamp.Time)
			}
			baseMeter.lastStatus = clusterStatus
		}
		return ErrStatusOutOfSync
	}
	return nil
}

func (m *MonitorSubController) objCopyFromCache(key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := m.federatedStore.GetByKey(key)
	if err != nil {
		wrappedErr := fmt.Errorf("Failed to query FederatedTypeConfig store for %q: %w", key, err)
		utilruntime.HandleError(wrappedErr)
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}

func (m *MonitorSubController) objStatusCopyFromCache(key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := m.statusStore.GetByKey(key)
	if err != nil {
		wrappedErr := fmt.Errorf("Failed to query FederatedTypeConfig store for %q: %w", key, err)
		utilruntime.HandleError(wrappedErr)
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}

func (m *MonitorSubController) getFedStatusObservedGeneration(key string) (map[string]int64, error) {
	cachedObj, exist, err := m.statusStore.GetByKey(key)
	if err != nil {
		wrappedErr := fmt.Errorf("Failed to query FederatedTypeConfig store for %q: %w", key, err)
		utilruntime.HandleError(wrappedErr)
		return nil, err
	}
	if !exist {
		err = fmt.Errorf("Fed status for %q not found", key)
		return nil, err
	}

	status := cachedObj.(*unstructured.Unstructured)
	clusterStatuses, _, _ := unstructured.NestedSlice(status.Object, "clusterStatus")

	ret := make(map[string]int64)
	var clusterName string
	for _, clusterStatus := range clusterStatuses {
		if clusterStatus != nil && clusterStatus.(map[string]interface{})["clusterName"] != nil {
			clusterName = clusterStatus.(map[string]interface{})["clusterName"].(string)
		} else {
			return nil, ErrMissingObservedGenerationField
		}
		// prevent status from missing observedGeneration field

		if clusterStatus.(map[string]interface{})["status"] != nil &&
			clusterStatus.(map[string]interface{})["status"].(map[string]interface{})["observedGeneration"] != nil {
			observedGeneration := clusterStatus.(map[string]interface{})["status"].(map[string]interface{})["observedGeneration"].(int64)
			ret[clusterName] = observedGeneration
		} else {
			return nil, ErrMissingObservedGenerationField
		}
	}
	return ret, nil
}

func (m *MonitorSubController) getClusterObservedGeneration(key string) (map[string]int64, error) {
	ret := make(map[string]int64)
	clusterObjs, err := m.informer.GetTargetStore().GetFromAllClusters(key)
	if err != nil {
		wrappedErr := fmt.Errorf("Failed to get %q from all clusters: %w", key, err)
		runtime.HandleError(wrappedErr)
		return nil, wrappedErr
	}

	for _, clusterObj := range clusterObjs {
		obj := clusterObj.Object.(*unstructured.Unstructured)
		observedGeneration, found, err := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
		if err != nil || !found {
			wrappedErr := fmt.Errorf(
				"Failed to get status of cluster resource object %q for cluster %q, found: %t, err: %w",
				key,
				clusterObj.ClusterName,
				found,
				err,
			)
			runtime.HandleError(wrappedErr)
			return ret, wrappedErr
		}
		ret[clusterObj.ClusterName] = observedGeneration
	}
	return ret, nil
}

// ensure the same time format.
func Now() metav1.Time {
	timeString := time.Now().UTC().Format(time.RFC3339Nano)
	now, _ := time.Parse(time.RFC3339Nano, timeString)
	return metav1.NewTime(now)
}
