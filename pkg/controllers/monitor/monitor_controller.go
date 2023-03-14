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
	"sync"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federatedtypeconfig"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	finalizersutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
)

const (
	ReportInterval time.Duration = 1 * time.Minute

	finalizer = "core." + common.DefaultPrefix + "monitor-controller"
)

// The Monitor controller monitor the sync latency of resources defined in the
// KubeFed system namespace.
type MonitorController struct {
	// Arguments to use when starting new controllers
	controllerConfig *util.ControllerConfig
	client           genericclient.Client
	// Map of running sync controllers keyed by qualified target type
	stopChannels map[string]chan struct{}
	lock         sync.RWMutex
	// Store for the FederatedTypeConfig objects
	store cache.Store
	// Informer for the FederatedTypeConfig objects
	controller      cache.Controller
	informerFactory informers.SharedInformerFactory
	worker          worker.ReconcileWorker
	// TBD, may be need a safe map
	meters *sync.Map
}

type BaseMeter struct {
	creationTimestamp         metav1.Time
	lastUpdateTimestamp       metav1.Time
	syncSuccessTimestamp      metav1.Time
	lastStatus                map[string]int64
	lastStatusSyncedTimestamp metav1.Time
	outOfSyncDuration         time.Duration
}

// NewController returns a new controller to monitor FederatedTypeConfig objects.
func NewMonitorController(config *util.ControllerConfig) (*MonitorController, error) {
	userAgent := "Monitor"
	kubeConfig := restclient.CopyConfig(config.KubeConfig)
	restclient.AddUserAgent(kubeConfig, userAgent)
	genericclient, err := genericclient.New(kubeConfig)
	if err != nil {
		return nil, err
	}

	c := &MonitorController{
		controllerConfig: config,
		client:           genericclient,
		stopChannels:     make(map[string]chan struct{}),
	}

	c.worker = worker.NewReconcileWorker(c.reconcile, worker.WorkerTiming{}, 1, config.Metrics,
		delayingdeliver.NewMetricTags("monitor-worker", ""))
	c.meters = &sync.Map{}

	// Only watch the KubeFed namespace to ensure
	// restrictive authz can be applied to a namespaced
	// control plane.
	c.store, c.controller, err = util.NewGenericInformer(
		kubeConfig,
		"",
		&fedcorev1a1.FederatedTypeConfig{},
		util.NoResyncPeriod,
		c.worker.EnqueueObject,
		config.Metrics,
	)
	if err != nil {
		return nil, err
	}

	kubeClient := kubeclient.NewForConfigOrDie(kubeConfig)
	c.informerFactory = informers.NewSharedInformerFactory(kubeClient, util.NoResyncPeriod)
	return c, nil
}

// Run runs the Controller.
func (c *MonitorController) Run(stopChan <-chan struct{}) error {
	klog.Infof("Starting Monitor controller")
	go c.controller.Run(stopChan)

	// wait for the caches to synchronize before starting the worker
	if !cache.WaitForCacheSync(stopChan, c.controller.HasSynced) {
		return errors.New("Timed out waiting for cache to sync")
	}

	c.worker.Run(stopChan)

	klog.Infof("Starting reporter to report meters to metrics.")
	go wait.Until(c.report, ReportInterval, stopChan)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		c.shutDown()
	}()
	return nil
}

func (c *MonitorController) HasSynced() bool {
	if !c.controller.HasSynced() {
		klog.V(2).Infof("monitor controller's controller hasn't synced")
		return false
	}
	return true
}

func (c *MonitorController) reconcile(qualifiedName common.QualifiedName) worker.Result {
	key := qualifiedName.String()

	klog.V(3).Infof("Running reconcile Monitor for %q", key)

	cachedObj, err := c.objCopyFromCache(key)
	if err != nil {
		return worker.StatusError
	}

	if cachedObj == nil {
		return worker.StatusAllOK
	}
	typeConfig := cachedObj.(*fedcorev1a1.FederatedTypeConfig)

	// TODO(marun) Perform this defaulting in a webhook
	federatedtypeconfig.SetFederatedTypeConfigDefaults(typeConfig)

	limitedScope := c.controllerConfig.TargetNamespace != metav1.NamespaceAll
	if limitedScope && !typeConfig.GetNamespaced() {
		_, ok := c.getStopChannel(typeConfig.Name)
		if !ok {
			holderChan := make(chan struct{})
			c.lock.Lock()
			c.stopChannels[typeConfig.Name] = holderChan
			c.lock.Unlock()
			klog.Infof(
				"Skipping start of monitor controller for cluster-scoped resource %q. It is not required for a namespaced KubeFed control plane.",
				typeConfig.GetFederatedType().Kind,
			)
		}
		return worker.StatusAllOK
	}

	monitorStopChan, monitorRunning := c.getStopChannel(typeConfig.Name)

	deleted := typeConfig.DeletionTimestamp != nil
	if deleted {
		if monitorRunning {
			c.stopController(typeConfig.Name, monitorStopChan)
		}

		if typeConfig.IsNamespace() {
			klog.Infof("Reconciling all namespaced monitor resources on deletion of %q", key)
			c.reconcileOnNamespaceFTCUpdate()
		}

		err := c.removeFinalizer(typeConfig)
		if err != nil {
			runtime.HandleError(errors.Wrapf(err, "Failed to remove finalizer from FederatedTypeConfig %q", key))
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	updated, err := c.ensureFinalizer(typeConfig)
	if err != nil {
		runtime.HandleError(errors.Wrapf(err, "Failed to ensure finalizer for FederatedTypeConfig %q", key))
		return worker.StatusError
	} else if updated && typeConfig.IsNamespace() {
		// Detected creation of the namespace FTC. If there are existing FTCs
		// which did not start their sync controllers due to the lack of a
		// namespace FTC, then reconcile them now so they can start.
		klog.Infof("Reconciling all namespaced monitor resources on finalizer update for %q", key)
		c.reconcileOnNamespaceFTCUpdate()
	}

	startNewMonitorSubController := !monitorRunning
	stopMonitorSubController := monitorRunning && (typeConfig.GetNamespaced() && !c.namespaceFTCExists())
	if startNewMonitorSubController {
		if err := c.startMonitorSubController(typeConfig); err != nil {
			runtime.HandleError(err)
			return worker.StatusError
		}
	} else if stopMonitorSubController {
		c.stopController(typeConfig.Name, monitorStopChan)
	}

	if !startNewMonitorSubController && !stopMonitorSubController &&
		typeConfig.Status.ObservedGeneration != typeConfig.Generation {
		if err := c.refreshMonitorSubController(typeConfig); err != nil {
			runtime.HandleError(err)
			return worker.StatusError
		}
	}

	typeConfig.Status.ObservedGeneration = typeConfig.Generation
	return worker.StatusAllOK
}

func (c *MonitorController) objCopyFromCache(key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := c.store.GetByKey(key)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "Failed to query FederatedTypeConfig store for %q", key)
		runtime.HandleError(wrappedErr)
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}

func (c *MonitorController) shutDown() {
	c.lock.Lock()
	defer c.lock.Unlock()

	// Stop all sync and status controllers
	for key, stopChannel := range c.stopChannels {
		close(stopChannel)
		delete(c.stopChannels, key)
	}
}

func (c *MonitorController) getStopChannel(name string) (chan struct{}, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	stopChan, ok := c.stopChannels[name]
	return stopChan, ok
}

func (c *MonitorController) startMonitorSubController(tc *fedcorev1a1.FederatedTypeConfig) error {
	ftc := tc.DeepCopyObject().(*fedcorev1a1.FederatedTypeConfig)
	kind := ftc.Spec.FederatedType.Kind
	controllerConfig := new(util.ControllerConfig)
	*controllerConfig = *(c.controllerConfig)

	stopChan := make(chan struct{})
	err := StartMonitorSubController(
		controllerConfig,
		stopChan,
		ftc,
		c.meters)
	if err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting monitor subController for %q", kind)
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[ftc.Name] = stopChan
	return nil
}

func (c *MonitorController) stopController(key string, stopChan chan struct{}) {
	klog.Infof("Stopping monitor controller for %q", key)
	close(stopChan)
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.stopChannels, key)
}

func (c *MonitorController) refreshMonitorSubController(tc *fedcorev1a1.FederatedTypeConfig) error {
	klog.Infof("refreshing monitor controller for %q", tc.Name)

	monitorStopChan, ok := c.getStopChannel(tc.Name)
	if ok {
		c.stopController(tc.Name, monitorStopChan)
	}

	return c.startMonitorSubController(tc)
}

func (c *MonitorController) ensureFinalizer(tc *fedcorev1a1.FederatedTypeConfig) (bool, error) {
	isUpdated, err := finalizersutil.AddFinalizers(tc, sets.NewString(finalizer))
	if err != nil || !isUpdated {
		return false, err
	}
	err = c.client.Update(context.TODO(), tc)
	return true, err
}

func (c *MonitorController) removeFinalizer(tc *fedcorev1a1.FederatedTypeConfig) error {
	isUpdated, err := finalizersutil.RemoveFinalizers(tc, sets.NewString(finalizer))
	if err != nil || !isUpdated {
		return err
	}
	err = c.client.Update(context.TODO(), tc)
	return err
}

func (c *MonitorController) namespaceFTCExists() bool {
	_, err := c.getFederatedNamespaceAPIResource()
	return err == nil
}

func (c *MonitorController) getFederatedNamespaceAPIResource() (*metav1.APIResource, error) {
	qualifiedName := common.QualifiedName{
		Namespace: "",
		Name:      common.NamespaceResource,
	}
	key := qualifiedName.String()
	cachedObj, exists, err := c.store.GetByKey(key)
	if err != nil {
		return nil, errors.Wrapf(err, "Error retrieving %q from the informer cache", key)
	}
	if !exists {
		return nil, errors.Errorf("Unable to find %q in the informer cache", key)
	}
	namespaceTypeConfig := cachedObj.(*fedcorev1a1.FederatedTypeConfig)
	apiResource := namespaceTypeConfig.GetFederatedType()
	return &apiResource, nil
}

func (c *MonitorController) reconcileOnNamespaceFTCUpdate() {
	for _, cachedObj := range c.store.List() {
		typeConfig := cachedObj.(*fedcorev1a1.FederatedTypeConfig)
		if typeConfig.GetNamespaced() && !typeConfig.IsNamespace() {
			c.worker.EnqueueObject(typeConfig)
		}
	}
}

func (c *MonitorController) report() {
	client := c.controllerConfig.Metrics
	DoReport(c.meters, client)
}
