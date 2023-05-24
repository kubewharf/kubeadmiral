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

package federatedtypeconfig

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	typedapiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/nsautoprop"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/policyrc"
	statuscontroller "github.com/kubewharf/kubeadmiral/pkg/controllers/status"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/statusaggregator"
	synccontroller "github.com/kubewharf/kubeadmiral/pkg/controllers/sync"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
)

var mutex = sync.Mutex{}

var finalizer string = "core." + common.DefaultPrefix + "federated-type-config"

const ControllerName = "federated-type-config"

// The FederatedTypeConfig controller configures sync and status
// controllers in response to FederatedTypeConfig resources in the
// KubeFed system namespace.
type Controller struct {
	// Arguments to use when starting new controllers
	controllerConfig *util.ControllerConfig

	kubeClient    kubernetes.Interface
	fedClient     fedclient.Interface
	dynamicClient dynamic.Interface

	discoveryClient discovery.DiscoveryInterface
	crdClient       typedapiextensionsv1.CustomResourceDefinitionInterface

	kubeInformerFactory    informers.SharedInformerFactory
	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory
	fedInformerFactory     fedinformers.SharedInformerFactory

	// Map of running sync controllers keyed by qualified target type
	stopChannels map[string]chan struct{}
	lock         sync.RWMutex

	// Store for the FederatedTypeConfig objects
	ftcStore cache.Store
	// Informer for the FederatedTypeConfig objects
	ftcController cache.Controller

	worker worker.ReconcileWorker

	controllerRevisionStore      cache.Store
	controllerRevisionController cache.Controller
	isControllerRevisionExists   bool
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

// NewController returns a new controller to manage FederatedTypeConfig objects.
func NewController(
	config *util.ControllerConfig,
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	fedClient fedclient.Interface,
	kubeInformerFactory informers.SharedInformerFactory,
	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory,
	fedInformerFactory fedinformers.SharedInformerFactory,
) (*Controller, error) {
	userAgent := "FederatedTypeConfig"
	kubeConfig := restclient.CopyConfig(config.KubeConfig)
	restclient.AddUserAgent(kubeConfig, userAgent)

	extClient, err := apiextensions.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	c := &Controller{
		controllerConfig:       config,
		fedClient:              fedClient,
		kubeClient:             kubeClient,
		dynamicClient:          dynamicClient,
		discoveryClient:        kubeClient.Discovery(),
		crdClient:              extClient.ApiextensionsV1().CustomResourceDefinitions(),
		kubeInformerFactory:    kubeInformerFactory,
		dynamicInformerFactory: dynamicInformerFactory,
		fedInformerFactory:     fedInformerFactory,
		stopChannels:           make(map[string]chan struct{}),
	}

	c.worker = worker.NewReconcileWorker(c.reconcile, worker.WorkerTiming{}, 1, config.Metrics,
		delayingdeliver.NewMetricTags("typeconfig-worker", "FederatedTypeConfig"))

	c.ftcStore, c.ftcController, err = util.NewGenericInformer(
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

	return c, nil
}

func (c *Controller) HasSynced() bool {
	if !c.ftcController.HasSynced() {
		klog.V(2).Infof("typeconfig controller's controller hasn't synced")
		return false
	}
	return true
}

// Run runs the Controller.
func (c *Controller) Run(stopChan <-chan struct{}) {
	klog.Infof("Starting FederatedTypeConfig controller")
	go c.ftcController.Run(stopChan)

	// wait for the caches to synchronize before starting the worker
	if !cache.WaitForNamedCacheSync("type-config-controller", stopChan, c.ftcController.HasSynced) {
		return
	}

	var nsFTC *fedcorev1a1.FederatedTypeConfig
	ftcs := c.ftcStore.List()
	for _, obj := range ftcs {
		ftc := obj.(*fedcorev1a1.FederatedTypeConfig)
		if ftc.Name == common.NamespaceResource && ftc.GetTargetType().Kind == common.NamespaceKind &&
			ftc.GetSourceType().Kind == common.NamespaceKind {
			nsFTC = ftc
			break
		}
	}
	if nsFTC == nil {
		// panic if ftc for namespace does not exist, since no other resource can be synced otherwise
		klog.Fatal("FederatedTypeConfig for namespaces not found")
	}

	// ensure the federated namespace since it is a requirement for other controllers
	if err := c.ensureFederatedObjectCrd(nsFTC); err != nil {
		klog.Fatalf("Failed to ensure FederatedNamespace CRD: %v", err)
	}

	c.worker.Run(stopChan)

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		c.shutDown()
	}()
}

func (c *Controller) reconcile(qualifiedName common.QualifiedName) worker.Result {
	key := qualifiedName.String()

	klog.V(3).Infof("Running reconcile FederatedTypeConfig for %q", key)

	cachedObj, err := c.objCopyFromCache(key)
	if err != nil {
		return worker.StatusError
	}

	if cachedObj == nil {
		return worker.StatusAllOK
	}
	typeConfig := cachedObj.(*fedcorev1a1.FederatedTypeConfig)

	// TODO(marun) Perform this defaulting in a webhook
	SetFederatedTypeConfigDefaults(typeConfig)

	if c.controllerConfig.CreateCrdForFtcs {
		if err := c.ensureFederatedObjectCrd(typeConfig); err != nil {
			klog.Error(fmt.Errorf("cannot ensure federated object CRD for %q: %w", typeConfig.Name, err))
			return worker.StatusError
		}
	}

	syncEnabled := typeConfig.GetPropagationEnabled()
	statusEnabled := typeConfig.GetStatusEnabled()
	statusAggregationEnabled := typeConfig.GetStatusAggregationEnabled()
	policyRcEnabled := typeConfig.GetPolicyRcEnabled()
	controllers := sets.New[string]()
	for _, controllerGroup := range typeConfig.GetControllers() {
		for _, controller := range controllerGroup {
			controllers.Insert(controller)
		}
	}
	namespaceAutoPropagationEnabled := controllers.Has(nsautoprop.PrefixedNamespaceAutoPropagationControllerName)
	overridePolicyEnabled := controllers.Has(override.PrefixedControllerName)

	limitedScope := c.controllerConfig.TargetNamespace != metav1.NamespaceAll
	if limitedScope && syncEnabled && !typeConfig.GetNamespaced() {
		_, ok := c.getStopChannel(typeConfig.Name)
		if !ok {
			holderChan := make(chan struct{})
			c.lock.Lock()
			c.stopChannels[typeConfig.Name] = holderChan
			c.lock.Unlock()
			klog.Infof(
				"Skipping start of sync & status controller for cluster-scoped resource %q. It is not required for a namespaced KubeFed control plane.",
				typeConfig.GetFederatedType().Kind,
			)
		}

		// typeConfig.Status.ObservedGeneration = typeConfig.Generation
		// typeConfig.Status.PropagationController = corev1a1.ControllerStatusNotRunning

		/*if typeConfig.Status.StatusController == nil {
			typeConfig.Status.StatusController = new(corev1a1.ControllerStatus)
		}
		*typeConfig.Status.StatusController = corev1a1.ControllerStatusNotRunning
		err = c.client.UpdateStatus(context.TODO(), typeConfig)
		if err != nil {
			runtime.HandleError(errors.Wrapf(err, "Could not update status fields of the CRD: %q", key))
			return worker.StatusError
		}*/
		return worker.StatusAllOK
	}

	statusKey := typeConfig.Name + "/status"
	statusAggregationKey := typeConfig.Name + "/statusAggregation"
	policyRcKey := typeConfig.Name + "/policyRc"
	federateKey := typeConfig.Name + "/federate"
	schedulerKey := typeConfig.Name + "/scheduler"
	namespaceAutoPropagationKey := typeConfig.Name + "/namespaceAutoPropagation"
	overridePolicyKey := typeConfig.Name + "/overridePolicy"

	syncStopChan, syncRunning := c.getStopChannel(typeConfig.Name)
	statusStopChan, statusRunning := c.getStopChannel(statusKey)
	statusAggregationStopChan, statusAggregationRunning := c.getStopChannel(statusAggregationKey)
	policyRcStopChan, policyRcRunning := c.getStopChannel(policyRcKey)
	federateStopChan, federateRunning := c.getStopChannel(federateKey)
	schedulerStopChan, schedulerRunning := c.getStopChannel(schedulerKey)
	namespaceAutoPropagationStopChan, namespaceAutoPropagationRunning := c.getStopChannel(namespaceAutoPropagationKey)
	overridePolicyStopChan, overridePolicyRunning := c.getStopChannel(overridePolicyKey)

	deleted := typeConfig.DeletionTimestamp != nil
	if deleted {
		if syncRunning {
			c.stopController(typeConfig.Name, syncStopChan)
		}
		if statusRunning {
			c.stopController(statusKey, statusStopChan)
		}
		if federateRunning {
			c.stopController(federateKey, federateStopChan)
		}
		if schedulerRunning {
			c.stopController(schedulerKey, schedulerStopChan)
		}
		if overridePolicyRunning {
			c.stopController(overridePolicyKey, overridePolicyStopChan)
		}

		if typeConfig.IsNamespace() {
			if namespaceAutoPropagationRunning {
				c.stopController(namespaceAutoPropagationKey, namespaceAutoPropagationStopChan)
			}
			klog.Infof("Reconciling all namespaced FederatedTypeConfig resources on deletion of %q", key)
			c.reconcileOnNamespaceFTCUpdate()
		}

		err := c.removeFinalizer(typeConfig)
		if err != nil {
			klog.Error(errors.Wrapf(err, "Failed to remove finalizer from FederatedTypeConfig %q", key))
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	typeConfig, updated, err := c.ensureFinalizer(typeConfig)
	if err != nil {
		klog.Error(errors.Wrapf(err, "Failed to ensure finalizer for FederatedTypeConfig %q", key))
		return worker.StatusError
	} else if updated && typeConfig.IsNamespace() {
		// Detected creation of the namespace FTC. If there are existing FTCs
		// which did not start their sync controllers due to the lack of a
		// namespace FTC, then reconcile them now so they can start.
		klog.Infof("Reconciling all namespaced FederatedTypeConfig resources on finalizer update for %q", key)
		c.reconcileOnNamespaceFTCUpdate()
	}

	startNewSyncController := !syncRunning && syncEnabled
	stopSyncController := syncRunning && (!syncEnabled || (typeConfig.GetNamespaced() && !c.namespaceFTCExists()))
	if startNewSyncController {
		if err := c.startSyncController(typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	} else if stopSyncController {
		c.stopController(typeConfig.Name, syncStopChan)
	}

	startNewStatusController := !statusRunning && statusEnabled
	stopStatusController := statusRunning && !statusEnabled
	if startNewStatusController {
		if err := c.startStatusController(statusKey, typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	} else if stopStatusController {
		c.stopController(statusKey, statusStopChan)
	}

	startNewStatusAggregationController := !statusAggregationRunning && statusAggregationEnabled
	stopStatusAggregationController := statusAggregationRunning && !statusAggregationEnabled
	if startNewStatusAggregationController {
		if err := c.startStatusAggregationController(statusAggregationKey, typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	} else if stopStatusAggregationController {
		c.stopController(statusAggregationKey, statusAggregationStopChan)
	}

	startNewPolicyRcController := !policyRcRunning && policyRcEnabled
	stopPolicyRcController := policyRcRunning && !policyRcEnabled
	if startNewPolicyRcController {
		if err := c.startPolicyRcController(policyRcKey, typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	} else if stopPolicyRcController {
		c.stopController(policyRcKey, policyRcStopChan)
	}

	startNewNamespaceAutoPropagationController := !namespaceAutoPropagationRunning && typeConfig.IsNamespace() &&
		namespaceAutoPropagationEnabled
	stopNamespaceAutoPropagationController := namespaceAutoPropagationRunning &&
		(!typeConfig.IsNamespace() || !namespaceAutoPropagationEnabled)
	if startNewNamespaceAutoPropagationController {
		if err := c.startNamespaceAutoPropagationController(namespaceAutoPropagationKey, typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	} else if stopNamespaceAutoPropagationController {
		c.stopController(namespaceAutoPropagationKey, namespaceAutoPropagationStopChan)
	}

	startOverridePolicyController := !overridePolicyRunning && overridePolicyEnabled
	stopOverridePolicyController := overridePolicyRunning && !overridePolicyEnabled
	if startOverridePolicyController {
		if err := c.startOverridePolicyController(overridePolicyKey, typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	} else if stopOverridePolicyController {
		c.stopController(overridePolicyKey, overridePolicyStopChan)
	}

	if !startNewSyncController && !stopSyncController &&
		typeConfig.Status.ObservedGeneration != typeConfig.Generation {
		if err := c.refreshSyncController(typeConfig); err != nil {
			klog.Error(err)
			return worker.StatusError
		}
	}

	typeConfig.Status.ObservedGeneration = typeConfig.Generation
	/*syncControllerRunning := startNewSyncController || (syncRunning && !stopSyncController)
	if syncControllerRunning {
		typeConfig.Status.PropagationController = corev1a1.ControllerStatusRunning
	} else {
		typeConfig.Status.PropagationController = corev1a1.ControllerStatusNotRunning
	}

	if typeConfig.Status.StatusController == nil {
		typeConfig.Status.StatusController = new(corev1a1.ControllerStatus)
	}

	statusControllerRunning := startNewStatusController || (statusRunning && !stopStatusController)
	if statusControllerRunning {
		*typeConfig.Status.StatusController = corev1a1.ControllerStatusRunning
	} else {
		*typeConfig.Status.StatusController = corev1a1.ControllerStatusNotRunning
	}
	err = c.client.UpdateStatus(context.TODO(), typeConfig)
	if err != nil {
		runtime.HandleError(errors.Wrapf(err, "Could not update status fields of the CRD: %q", key))
		return worker.StatusError
	}*/
	return worker.StatusAllOK
}

func (c *Controller) ensureFederatedObjectCrd(ftc *fedcorev1a1.FederatedTypeConfig) error {
	fedTy := ftc.Spec.FederatedType
	crdName := fedTy.PluralName
	if fedTy.Group != "" {
		crdName += "."
		crdName += fedTy.Group
	}

	_, err := c.crdClient.Get(context.TODO(), crdName, metav1.GetOptions{ResourceVersion: "0"})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("cannot check for existence of CRD %q: %w", crdName, err)
	}

	needObjectCrd := err != nil

	needStatusCrd := false
	statusTy := ftc.Spec.StatusType
	var statusCrdName string
	if statusTy != nil {
		statusCrdName = statusTy.PluralName
		if statusTy.Group != "" {
			statusCrdName += "."
			statusCrdName += statusTy.Group
		}

		_, err = c.crdClient.Get(context.TODO(), statusCrdName, metav1.GetOptions{ResourceVersion: "0"})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("cannot check for existence of CRD %q: %w", statusCrdName, err)
		}

		needStatusCrd = err != nil
	}

	var sourceResource *metav1.APIResource
	if ftc.Spec.SourceType != nil {
		srcTy := ftc.Spec.SourceType

		resourceList, err := c.discoveryClient.ServerResourcesForGroupVersion(schema.GroupVersion{
			Group:   srcTy.Group,
			Version: srcTy.Version,
		}.String())
		if err != nil {
			return fmt.Errorf("cannot invoke discovery client: %w", err)
		}

		for _, resource := range resourceList.APIResources {
			// we don't care about resource.Group because subresources are not supported

			if resource.Name == srcTy.PluralName {
				resource := resource
				sourceResource = &resource
				break
			}
		}
	}

	// create CRD now
	if needObjectCrd {
		klog.V(2).Infof("Creating federated CRD for %q", ftc.Name)

		fedShortNames := []string{"f" + strings.ToLower(ftc.Spec.TargetType.Kind)}
		if sourceResource != nil {
			for _, shortName := range sourceResource.ShortNames {
				fedShortNames = append(fedShortNames, "f"+shortName)
			}
		}

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: fedTy.Group,
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:     fedTy.PluralName,
					Kind:       fedTy.Kind,
					Singular:   strings.ToLower(fedTy.Kind),
					ShortNames: fedShortNames,
					ListKind:   fedTy.Kind + "List",
				},
				Scope: apiextensionsv1.ResourceScope(fedTy.Scope),
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    fedTy.Version,
						Served:  true,
						Storage: true,
						Subresources: &apiextensionsv1.CustomResourceSubresources{
							Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
						},
						Schema: &fedObjectSchema,
					},
				},
			},
		}
		_, err = c.crdClient.Create(context.TODO(), crd, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	if needStatusCrd {
		klog.V(2).Infof("Creating status CRD for %q", ftc.Name)

		statusShortNames := []string{fmt.Sprintf("f%sstatus", strings.ToLower(ftc.Spec.TargetType.Kind))}
		if sourceResource != nil {
			for _, shortName := range sourceResource.ShortNames {
				statusShortNames = append(statusShortNames, fmt.Sprintf("f%sstatus", shortName))
			}
		}

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: statusCrdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: statusTy.Group,
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:     statusTy.PluralName,
					Kind:       statusTy.Kind,
					Singular:   strings.ToLower(statusTy.Kind),
					ShortNames: statusShortNames,
					ListKind:   statusTy.Kind + "List",
				},
				Scope: apiextensionsv1.ResourceScope(statusTy.Scope),
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    statusTy.Version,
						Served:  true,
						Storage: true,
						Subresources: &apiextensionsv1.CustomResourceSubresources{
							Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
						},
						Schema: &statusObjectSchema,
					},
				},
			},
		}
		_, err = c.crdClient.Create(context.TODO(), crd, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) objCopyFromCache(key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := c.ftcStore.GetByKey(key)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "Failed to query FederatedTypeConfig store for %q", key)
		klog.Error(wrappedErr)
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}

func (c *Controller) shutDown() {
	c.lock.Lock()
	defer c.lock.Unlock()

	// Stop all sync and status controllers
	for key, stopChannel := range c.stopChannels {
		close(stopChannel)
		delete(c.stopChannels, key)
	}
}

func (c *Controller) getStopChannel(name string) (chan struct{}, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	stopChan, ok := c.stopChannels[name]
	return stopChan, ok
}

func (c *Controller) startSyncController(tc *fedcorev1a1.FederatedTypeConfig) error {
	ftc := tc.DeepCopyObject().(*fedcorev1a1.FederatedTypeConfig)
	kind := ftc.Spec.FederatedType.Kind
	controllerConfig := new(util.ControllerConfig)
	*controllerConfig = *(c.controllerConfig)

	// A sync controller for a namespaced resource must be supplied
	// with the ftc for namespaces so that it can consider federated
	// namespace placement when determining the placement for
	// contained resources.
	var fedNamespaceAPIResource *metav1.APIResource
	if ftc.GetNamespaced() {
		var err error
		fedNamespaceAPIResource, err = c.getFederatedNamespaceAPIResource()
		if err != nil {
			return errors.Wrapf(
				err,
				"Unable to start sync controller for %q due to missing FederatedTypeConfig for namespaces",
				kind,
			)
		}
	}

	mutex.Lock()
	defer mutex.Unlock()
	stopChan := make(chan struct{})

	if !c.isControllerRevisionExists {
		controllerRevision := util.GetResourceKind(&appsv1.ControllerRevision{})
		controllerRevisionsResource := &metav1.APIResource{
			Name:       util.GetPluralName(controllerRevision),
			Group:      appsv1.SchemeGroupVersion.Group,
			Version:    appsv1.SchemeGroupVersion.Version,
			Kind:       controllerRevision,
			Namespaced: true,
		}
		userAgent := "controller-revision-federate-sync-controller"
		configWithUserAgent := restclient.CopyConfig(controllerConfig.KubeConfig)
		restclient.AddUserAgent(configWithUserAgent, userAgent)
		controllerRevisionClient, err := util.NewResourceClient(configWithUserAgent, controllerRevisionsResource)
		if err != nil {
			klog.Errorf("Failed to initiate controller revision client.")
			return err
		}

		triggerFunc := func(obj pkgruntime.Object) {
			if accessor, err := meta.Accessor(obj); err == nil {
				klog.V(4).
					Infof("ControllerRevision changement observed: %s/%s", accessor.GetNamespace(), accessor.GetName())
			}
		}
		c.controllerRevisionStore, c.controllerRevisionController = util.NewResourceInformer(
			controllerRevisionClient,
			"",
			triggerFunc,
			c.controllerConfig.Metrics,
		)
		c.isControllerRevisionExists = true
		go c.controllerRevisionController.Run(stopChan)
	}

	err := synccontroller.StartKubeFedSyncController(
		controllerConfig,
		stopChan,
		ftc,
		fedNamespaceAPIResource,
		c.controllerRevisionStore,
		c.controllerRevisionController)
	if err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting sync controller for %q", kind)
	}
	klog.Infof("Started sync controller for %q", kind)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[ftc.Name] = stopChan
	return nil
}

func (c *Controller) startStatusController(statusKey string, tc *fedcorev1a1.FederatedTypeConfig) error {
	kind := tc.Spec.FederatedType.Kind
	stopChan := make(chan struct{})
	ftc := tc.DeepCopyObject().(*fedcorev1a1.FederatedTypeConfig)
	err := statuscontroller.StartKubeFedStatusController(c.controllerConfig, stopChan, ftc)
	if err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting status controller for %q", kind)
	}
	klog.Infof("Started status controller for %q", kind)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[statusKey] = stopChan
	return nil
}

func (c *Controller) startStatusAggregationController(
	statusAggregationKey string,
	tc *fedcorev1a1.FederatedTypeConfig,
) error {
	kind := tc.Spec.FederatedType.Kind
	stopChan := make(chan struct{})
	ftc := tc.DeepCopyObject().(*fedcorev1a1.FederatedTypeConfig)
	err := statusaggregator.StartStatusAggregator(c.controllerConfig, stopChan, ftc)
	if err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting status aggregator for %q", kind)
	}
	klog.Infof("Started status aggregator for %q", kind)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[statusAggregationKey] = stopChan
	return nil
}

func (c *Controller) startPolicyRcController(policyRcKey string, tc *fedcorev1a1.FederatedTypeConfig) error {
	kind := tc.Spec.FederatedType.Kind
	stopChan := make(chan struct{})
	ftc := tc.DeepCopyObject().(*fedcorev1a1.FederatedTypeConfig)
	err := policyrc.StartController(c.controllerConfig, stopChan, ftc)
	if err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting policy-rc controller for %q", kind)
	}
	klog.Infof("Started policy-rc controller for %q", kind)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[policyRcKey] = stopChan
	return nil
}

func (c *Controller) startNamespaceAutoPropagationController(
	namespaceAutoPropagationKey string,
	tc *fedcorev1a1.FederatedTypeConfig,
) error {
	kind := tc.Spec.FederatedType.Kind
	stopChan := make(chan struct{})
	ftc := tc.DeepCopyObject().(*fedcorev1a1.FederatedTypeConfig)
	err := nsautoprop.StartController(
		c.controllerConfig,
		stopChan,
		ftc,
		c.kubeClient,
		c.dynamicClient,
		c.dynamicInformerFactory,
		c.fedInformerFactory,
	)
	if err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting namespace-auto-propagation controller for %q", kind)
	}
	klog.Infof("Started namespace-auto-propagation controller for %q", kind)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[namespaceAutoPropagationKey] = stopChan
	return nil
}

func (c *Controller) startOverridePolicyController(
	overridePolicyKey string,
	tc *fedcorev1a1.FederatedTypeConfig,
) error {
	kind := tc.Spec.FederatedType.Kind
	stopChan := make(chan struct{})
	if err := override.StartController(c.controllerConfig, stopChan, tc); err != nil {
		close(stopChan)
		return errors.Wrapf(err, "Error starting overridepolicy-controller for %q", kind)
	}
	klog.Infof("Started overridepolicy-controller for %q", kind)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.stopChannels[overridePolicyKey] = stopChan
	return nil
}

func (c *Controller) stopController(key string, stopChan chan struct{}) {
	klog.Infof("Stopping controller for %q", key)
	close(stopChan)
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.stopChannels, key)
}

func (c *Controller) refreshSyncController(tc *fedcorev1a1.FederatedTypeConfig) error {
	klog.Infof("refreshing sync controller for %q", tc.Name)

	syncStopChan, ok := c.getStopChannel(tc.Name)
	if ok {
		c.stopController(tc.Name, syncStopChan)
	}

	return c.startSyncController(tc)
}

func (c *Controller) ensureFinalizer(
	tc *fedcorev1a1.FederatedTypeConfig,
) (*fedcorev1a1.FederatedTypeConfig, bool, error) {
	accessor, err := meta.Accessor(tc)
	if err != nil {
		return nil, false, err
	}
	finalizers := sets.NewString(accessor.GetFinalizers()...)
	if finalizers.Has(finalizer) {
		return tc, false, nil
	}
	finalizers.Insert(finalizer)
	accessor.SetFinalizers(finalizers.List())
	tc, err = c.fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(context.TODO(), tc, metav1.UpdateOptions{})
	return tc, true, err
}

func (c *Controller) removeFinalizer(tc *fedcorev1a1.FederatedTypeConfig) error {
	accessor, err := meta.Accessor(tc)
	if err != nil {
		return err
	}
	finalizers := sets.NewString(accessor.GetFinalizers()...)
	if !finalizers.Has(finalizer) {
		return nil
	}
	finalizers.Delete(finalizer)
	accessor.SetFinalizers(finalizers.List())
	_, err = c.fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(context.TODO(), tc, metav1.UpdateOptions{})
	return err
}

func (c *Controller) namespaceFTCExists() bool {
	_, err := c.getFederatedNamespaceAPIResource()
	return err == nil
}

func (c *Controller) getFederatedNamespaceAPIResource() (*metav1.APIResource, error) {
	qualifiedName := common.QualifiedName{
		Namespace: "",
		Name:      common.NamespaceResource,
	}
	key := qualifiedName.String()
	cachedObj, exists, err := c.ftcStore.GetByKey(key)
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

func (c *Controller) reconcileOnNamespaceFTCUpdate() {
	for _, cachedObj := range c.ftcStore.List() {
		typeConfig := cachedObj.(*fedcorev1a1.FederatedTypeConfig)
		if typeConfig.GetNamespaced() && !typeConfig.IsNamespace() {
			c.worker.EnqueueObject(typeConfig)
		}
	}
}

// pluralName computes the plural name from the kind by lowercasing and suffixing with 's' or `es`.
func SetFederatedTypeConfigDefaults(obj *fedcorev1a1.FederatedTypeConfig) {
	nameParts := strings.SplitN(obj.Name, ".", 2)
	targetPluralName := nameParts[0]
	setStringDefault(&obj.Spec.TargetType.PluralName, targetPluralName)
	if len(nameParts) > 1 {
		group := nameParts[1]
		setStringDefault(&obj.Spec.TargetType.Group, group)
	}
	setStringDefault(&obj.Spec.FederatedType.PluralName, pluralName(obj.Spec.FederatedType.Kind))
}

func pluralName(kind string) string {
	lowerKind := strings.ToLower(kind)
	if strings.HasSuffix(lowerKind, "s") || strings.HasSuffix(lowerKind, "x") ||
		strings.HasSuffix(lowerKind, "ch") || strings.HasSuffix(lowerKind, "sh") ||
		strings.HasSuffix(lowerKind, "z") || strings.HasSuffix(lowerKind, "o") {
		return fmt.Sprintf("%ses", lowerKind)
	}
	if strings.HasSuffix(lowerKind, "y") {
		lowerKind = strings.TrimSuffix(lowerKind, "y")
		return fmt.Sprintf("%sies", lowerKind)
	}
	return fmt.Sprintf("%ss", lowerKind)
}

func setStringDefault(value *string, defaultValue string) {
	if value == nil || len(*value) > 0 {
		return
	}
	*value = defaultValue
}
