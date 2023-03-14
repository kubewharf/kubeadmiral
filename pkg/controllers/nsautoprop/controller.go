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

package nsautoprop

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	NamespaceAutoPropagationControllerName         = "nsautoprop-controller"
	PrefixedNamespaceAutoPropagationControllerName = common.DefaultPrefix + NamespaceAutoPropagationControllerName
	EventReasonNamespaceAutoPropagation            = "NamespaceAutoPropagation"
	NoAutoPropagationAnnotation                    = common.DefaultPrefix + "no-auto-propagation"
)

/*
NamespacesAutoPropagationController automatically propagates namespaces to all
clusters without requiring a ClusterPropagationPolicy for scheduling.
It
  - sets placement of federated namespaces to all clusters
  - ensures preexisting namespaces in member clusters are adopted
  - ensures adopted namespaces in member clusters are not deleted when the
    federated namespace is deleted

Note that since both NamespaceAutoPropagationController and global-scheduler sets the placements,
if both are enabled, they will conflict with each other and reconcile indefinitely.
*/
type Controller struct {
	// name of controller
	name string

	// FederatedTypeConfig for namespaces
	typeConfig *fedcorev1a1.FederatedTypeConfig

	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory
	fedInformerFactory     fedinformers.SharedInformerFactory

	// Informer for FederatedCluster
	clusterInformer fedcorev1a1informers.FederatedClusterInformer
	// Informer for FederatedNamespace
	fedNamespaceInformer informers.GenericInformer
	// Client for FederatedNamespace
	fedNamespaceClient dynamic.NamespaceableResourceInterface

	worker        worker.ReconcileWorker
	eventRecorder record.EventRecorder

	fedSystemNamespace string
	excludeRegexp      *regexp.Regexp

	metrics stats.Metrics
}

func StartController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory,
	fedInformerFactory fedinformers.SharedInformerFactory,
) error {
	controller, err := newController(
		controllerConfig,
		typeConfig,
		kubeClient,
		dynamicClient,
		fedInformerFactory,
		dynamicInformerFactory,
	)
	if err != nil {
		return err
	}
	klog.V(4).Infof("Starting namespace auto propagation controller")
	go controller.Run(stopChan)
	return nil
}

func newController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	kubeClient kubeclient.Interface,
	dynamicClient dynamic.Interface,
	fedInformerFactory fedinformers.SharedInformerFactory,
	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory,
) (*Controller, error) {
	userAgent := NamespaceAutoPropagationControllerName
	if !typeConfig.IsNamespace() {
		return nil, fmt.Errorf("%s expects a FederatedTypeConfig for namespaces", userAgent)
	}

	federatedNamespaceApiResource := typeConfig.GetFederatedType()
	fedNamespaceGVR := schemautil.APIResourceToGVR(&federatedNamespaceApiResource)
	c := &Controller{
		name:                   userAgent,
		typeConfig:             typeConfig,
		eventRecorder:          eventsink.NewDefederatingRecorderMux(kubeClient, userAgent, 4),
		dynamicInformerFactory: dynamicInformerFactory,
		fedInformerFactory:     fedInformerFactory,
		fedSystemNamespace:     controllerConfig.FedSystemNamespace,
		excludeRegexp:          controllerConfig.NamespaceAutoPropagationExcludeRegexp,
		metrics:                controllerConfig.Metrics,
		fedNamespaceClient:     dynamicClient.Resource(fedNamespaceGVR),
		clusterInformer:        fedInformerFactory.Core().V1alpha1().FederatedClusters(),
		fedNamespaceInformer:   dynamicInformerFactory.ForResource(fedNamespaceGVR),
	}

	c.worker = worker.NewReconcileWorker(
		c.reconcile,
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags(userAgent, federatedNamespaceApiResource.Kind),
	)
	enqueueObj := c.worker.EnqueueObject
	c.fedNamespaceInformer.Informer().
		AddEventHandlerWithResyncPeriod(util.NewTriggerOnAllChanges(enqueueObj), util.NoResyncPeriod)

	reconcileAll := func() {
		for _, fns := range c.fedNamespaceInformer.Informer().GetStore().List() {
			enqueueObj(fns.(runtime.Object))
		}
	}
	c.clusterInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			reconcileAll()
		},
		DeleteFunc: func(obj interface{}) {
			reconcileAll()
		},
	}, util.NoResyncPeriod)
	return c, nil
}

func (c *Controller) reconcile(qualifiedName common.QualifiedName) worker.Result {
	key := qualifiedName.String()

	c.metrics.Rate("namespace-auto-propagation-controller.throughput", 1)
	klog.V(4).Infof("namespace auto propagation controller starting to reconcile %v", key)
	startTime := time.Now()
	defer func() {
		c.metrics.Duration("namespace-auto-propagation-controller.latency", startTime)
		klog.V(4).
			Infof("namespace auto propagation controller finished reconciling %v (duration: %v)", key, time.Since(startTime))
	}()

	fedNamespace, err := c.getFederatedObject(qualifiedName)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	if fedNamespace == nil || fedNamespace.GetDeletionTimestamp() != nil {
		return worker.StatusAllOK
	}

	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(
		fedNamespace,
		PrefixedNamespaceAutoPropagationControllerName,
	); err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to check controller dependencies for %q: %w", key, err))
		return worker.StatusError
	} else if !ok {
		return worker.StatusAllOK
	}

	if !c.shouldBeAutoPropagated(fedNamespace) {
		updated, err := pendingcontrollers.UpdatePendingControllers(
			fedNamespace,
			PrefixedNamespaceAutoPropagationControllerName,
			false,
			c.typeConfig.GetControllers(),
		)
		if err != nil {
			utilruntime.HandleError(err)
			return worker.StatusError
		}

		if updated {
			_, err = c.fedNamespaceClient.Update(context.TODO(), fedNamespace, metav1.UpdateOptions{})
			if err != nil {
				if apierrors.IsConflict(err) {
					return worker.StatusConflict
				}
				utilruntime.HandleError(err)
				return worker.StatusError
			}
		}
		return worker.StatusAllOK
	}

	needsUpdate := false

	// Set placement to propagate to all clusters
	clusters, err := c.clusterInformer.Lister().List(labels.Everything())
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to list from cluster store: %w", err))
		return worker.StatusError
	}
	clusterNames := make(map[string]struct{}, len(clusters))
	for _, cluster := range clusters {
		clusterNames[cluster.Name] = struct{}{}
	}

	isDirty, err := util.SetPlacementClusterNames(
		fedNamespace,
		PrefixedNamespaceAutoPropagationControllerName,
		clusterNames,
	)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	// Set internal versions of the annotations so they do not get overriden by federate controller

	// Ensure we adopt pre-existing namespaces in member clusters
	isDirty, err = c.ensureAnnotation(
		fedNamespace,
		util.ConflictResolutionInternalAnnotation,
		string(util.ConflictResolutionAdopt),
	)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	// Ensure we don't delete adopted member namespaces when the federated namespace is deleted
	isDirty, err = c.ensureAnnotation(
		fedNamespace,
		util.OrphanManagedResourcesInternalAnnotation,
		string(util.OrphanManagedResourcesAdopted),
	)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	// Update the pending controllers to unblock downstream controllers
	isDirty, err = pendingcontrollers.UpdatePendingControllers(
		fedNamespace,
		PrefixedNamespaceAutoPropagationControllerName,
		needsUpdate,
		c.typeConfig.GetControllers(),
	)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	if !needsUpdate {
		return worker.StatusAllOK
	}

	_, err = c.fedNamespaceClient.Update(context.TODO(), fedNamespace, metav1.UpdateOptions{})
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		c.eventRecorder.Eventf(fedNamespace, corev1.EventTypeWarning, EventReasonNamespaceAutoPropagation,
			"failed to update %s %q for auto propagation, err: %v",
			fedNamespace.GetKind(), fedNamespace.GetName(), err)
		return worker.StatusError
	}

	c.eventRecorder.Eventf(fedNamespace, corev1.EventTypeNormal, EventReasonNamespaceAutoPropagation,
		"updated %s %q for auto propagation", fedNamespace.GetKind(), fedNamespace.GetName())

	return worker.StatusAllOK
}

func (c *Controller) shouldBeAutoPropagated(fedNamespace *unstructured.Unstructured) bool {
	name := fedNamespace.GetName()

	if strings.HasPrefix(name, "kube-") {
		// don't propagate system namespaces
		return false
	}

	if name == c.fedSystemNamespace {
		// don't propagate our own namespace
		return false
	}

	if c.excludeRegexp != nil && c.excludeRegexp.MatchString(name) {
		// don't propagate namespaces matched by exclusion regex
		return false
	}

	if fedNamespace.GetAnnotations()[NoAutoPropagationAnnotation] == common.AnnotationValueTrue {
		return false
	}

	return true
}

func (c *Controller) ensureAnnotation(fedNamespace *unstructured.Unstructured, key, value string) (bool, error) {
	needsUpdate, err := annotationutil.AddAnnotation(fedNamespace, key, value)
	if err != nil {
		return false, fmt.Errorf(
			"failed to add %s annotation to %s %q, err: %w",
			key, fedNamespace.GetKind(), fedNamespace.GetName(), err)
	}

	return needsUpdate, nil
}

func (c *Controller) Run(stopChan <-chan struct{}) {
	c.dynamicInformerFactory.Start(stopChan)
	c.fedInformerFactory.Start(stopChan)
	if !cache.WaitForNamedCacheSync(c.name, stopChan, c.HasSynced) {
		return
	}
	c.worker.Run(stopChan)
}

func (c *Controller) HasSynced() bool {
	return c.clusterInformer.Informer().HasSynced() &&
		c.fedNamespaceInformer.Informer().HasSynced()
}

func (c *Controller) getFederatedObject(qualifiedName common.QualifiedName) (*unstructured.Unstructured, error) {
	cachedObj, err := c.fedNamespaceInformer.Lister().Get(qualifiedName.String())
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err != nil {
		return nil, nil
	}
	return cachedObj.(*unstructured.Unstructured).DeepCopy(), nil
}
