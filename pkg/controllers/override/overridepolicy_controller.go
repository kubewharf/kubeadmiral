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

package override

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	ControllerName                       = "overridepolicy-controller"
	EventReasonMatchOverridePolicyFailed = "MatchOverridePolicyFailed"
	EventReasonParseOverridePolicyFailed = "ParseOverridePolicyFailed"
	EventReasonOverridePolicyApplied     = "OverridePolicyApplied"

	OverridePolicyNameLabel        = common.DefaultPrefix + "override-policy-name"
	ClusterOverridePolicyNameLabel = common.DefaultPrefix + "cluster-override-policy-name"
)

var PrefixedControllerName = common.DefaultPrefix + ControllerName

// OverrideController adds override rules specified in OverridePolicies
// to federated objects.
type Controller struct {
	// name of controller
	name string

	// FederatedTypeConfig for this controller
	typeConfig *fedcorev1a1.FederatedTypeConfig

	// Store for federated objects
	federatedStore cache.Store
	// Controller for federated objects
	federatedController cache.Controller
	// Client for federated objects
	federatedClient util.ResourceClient

	// Store for OverridePolicy
	overridePolicyStore cache.Store
	// Controller for OverridePolicy
	overridePolicyController cache.Controller

	// Store for ClusterOverridePolicy
	clusterOverridePolicyStore cache.Store
	// Controller for ClusterOverridePolicy
	clusterOverridePolicyController cache.Controller

	// Store for FederatedCluster
	clusterStore cache.Store
	// Controller for FederatedCluster
	clusterController cache.Controller

	worker        worker.ReconcileWorker
	eventRecorder record.EventRecorder
	metrics       stats.Metrics
}

func StartController(
	controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{},
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) error {
	controller, err := newController(controllerConfig, typeConfig)
	if err != nil {
		return err
	}
	klog.V(4).Infof("Starting %s", controller.name)
	controller.Run(stopChan)
	return nil
}

func newController(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) (*Controller, error) {
	userAgent := fmt.Sprintf("%s-%s", strings.ToLower(typeConfig.GetFederatedType().Kind), ControllerName)
	configWithUserAgent := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configWithUserAgent, userAgent)

	kubeClient := kubeclient.NewForConfigOrDie(configWithUserAgent)
	recorder := eventsink.NewDefederatingRecorderMux(kubeClient, userAgent, 4)

	c := &Controller{
		name:          userAgent,
		typeConfig:    typeConfig,
		eventRecorder: recorder,
		metrics:       controllerConfig.Metrics,
	}

	var err error

	federatedApiResource := typeConfig.GetFederatedType()
	c.federatedClient, err = util.NewResourceClient(configWithUserAgent, &federatedApiResource)
	if err != nil {
		return nil, fmt.Errorf("NewResourceClient failed: %w", err)
	}

	c.worker = worker.NewReconcileWorker(
		c.reconcile,
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags(c.name, federatedApiResource.Kind),
	)
	enqueueObj := c.worker.EnqueueObject
	c.federatedStore, c.federatedController = util.NewResourceInformer(
		c.federatedClient,
		controllerConfig.TargetNamespace,
		enqueueObj,
		controllerConfig.Metrics,
	)

	getPolicyHandlers := func(labelKey string) *cache.ResourceEventHandlerFuncs {
		return &cache.ResourceEventHandlerFuncs{
			// Policy added/updated: we need to reconcile all fedObjects referencing this policy
			AddFunc: func(obj interface{}) {
				policy := obj.(fedcorev1a1.GenericOverridePolicy)
				c.enqueueFedObjectsUsingPolicy(policy, labelKey)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldPolicy := oldObj.(fedcorev1a1.GenericOverridePolicy)
				newPolicy := newObj.(fedcorev1a1.GenericOverridePolicy)
				if !equality.Semantic.DeepEqual(oldPolicy.GetSpec(), newPolicy.GetSpec()) {
					c.enqueueFedObjectsUsingPolicy(newPolicy, labelKey)
				}
			},
			DeleteFunc: nil,
		}
	}

	c.overridePolicyStore, c.overridePolicyController, err = util.NewGenericInformerWithEventHandler(
		controllerConfig.KubeConfig,
		"",
		&fedcorev1a1.OverridePolicy{},
		util.NoResyncPeriod,
		getPolicyHandlers(OverridePolicyNameLabel),
		controllerConfig.Metrics,
	)
	if err != nil {
		return nil, err
	}

	c.clusterOverridePolicyStore, c.clusterOverridePolicyController, err = util.NewGenericInformerWithEventHandler(
		controllerConfig.KubeConfig,
		"",
		&fedcorev1a1.ClusterOverridePolicy{},
		util.NoResyncPeriod,
		getPolicyHandlers(ClusterOverridePolicyNameLabel),
		controllerConfig.Metrics,
	)
	if err != nil {
		return nil, err
	}

	c.clusterStore, c.clusterController, err = util.NewGenericInformerWithEventHandler(
		controllerConfig.KubeConfig,
		"",
		&fedcorev1a1.FederatedCluster{},
		util.NoResyncPeriod,
		&cache.ResourceEventHandlerFuncs{
			/*
				No need to reconcile on Add and Delete. Since we only resolve overrides for
				scheduled clusters, there's no point in reconciling before scheduler does rescheduling.
			*/
			AddFunc:    nil,
			DeleteFunc: nil,
			// We only care about label change, since that is the only cluster change
			// that can affect overrider computation.
			// Currently MatchFields only matches /metadata/name.
			// If we extend MatchFields to match new fields, we may need to revise UpdateFunc
			// to expand the trigger conditions.
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldCluster := oldObj.(*fedcorev1a1.FederatedCluster)
				newCluster := newObj.(*fedcorev1a1.FederatedCluster)
				if !equality.Semantic.DeepEqual(oldCluster.Labels, newCluster.Labels) {
					c.reconcileOnClusterChange(newCluster)
				}
			},
		},
		controllerConfig.Metrics,
	)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Controller) enqueueFedObjectsUsingPolicy(policy fedcorev1a1.GenericOverridePolicy, labelKey string) {
	klog.V(2).Infof("%s observed a policy change for %s %q", c.name, util.GetResourceKind(policy), policy.GetKey())
	for _, fedObjectInterface := range c.federatedStore.List() {
		fedObject := fedObjectInterface.(*unstructured.Unstructured)
		labelValue, exists := fedObject.GetLabels()[labelKey]
		if exists &&
			// fedObject must reference the policy
			labelValue == policy.GetName() &&
			// for ClusterOverridePolicy, fedObject can be cluster-scoped or belong to any namespace
			// for OverridePolicy, policy and fedObject must belong to the same namespace;
			(policy.GetNamespace() == "" || policy.GetNamespace() == fedObject.GetNamespace()) {
			c.worker.EnqueueObject(fedObject)
		}
	}
}

func (c *Controller) reconcileOnClusterChange(cluster *fedcorev1a1.FederatedCluster) {
	klog.V(2).Infof("%s observed a cluster change for %q", c.name, cluster.GetName())
	for _, fedObjectInterface := range c.federatedStore.List() {
		fedObject := fedObjectInterface.(*unstructured.Unstructured)
		labels := fedObject.GetLabels()
		// only enqueue fedObjects with a policy since we only need to recompute policies that are already applied
		if len(labels[OverridePolicyNameLabel]) > 0 || len(labels[ClusterOverridePolicyNameLabel]) > 0 {
			c.worker.EnqueueObject(fedObject)
		}
	}
}

func (c *Controller) reconcile(qualifiedName common.QualifiedName) worker.Result {
	kind := c.typeConfig.GetFederatedType().Kind
	key := qualifiedName.String()

	c.metrics.Rate(fmt.Sprintf("%v.throughput", c.name), 1)
	klog.V(4).Infof("%s starting to reconcile %s %v", c.name, kind, key)
	startTime := time.Now()
	defer func() {
		c.metrics.Duration(fmt.Sprintf("%s.latency", c.name), startTime)
		klog.V(4).Infof("%s finished reconciling %s %v (duration: %v)", c.name, kind, key, time.Since(startTime))
	}()

	fedObject, err := c.getFederatedObject(qualifiedName)
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		return worker.StatusAllOK
	}

	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(fedObject, PrefixedControllerName); err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to check controller dependencies for %s %q: %w", kind, key, err))
		return worker.StatusError
	} else if !ok {
		return worker.StatusAllOK
	}

	// TODO: don't apply a policy until it has the required finalizer for deletion protection
	policies, recheckOnErr, err := lookForMatchedPolicies(
		fedObject,
		c.typeConfig.GetNamespaced(),
		c.overridePolicyStore,
		c.clusterOverridePolicyStore,
	)
	if err != nil {
		c.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonMatchOverridePolicyFailed,
			"failed to find matched policy: %v",
			err.Error(),
		)
		if recheckOnErr {
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	placedClusters, err := c.getPlacedClusters(fedObject)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to get placed clusters for %s %q: %w", kind, key, err))
		return worker.StatusError
	}

	var overrides util.OverridesMap
	// Apply overrides from each policy in order
	for _, policy := range policies {
		newOverrides, err := parseOverrides(policy, placedClusters)
		if err != nil {
			c.eventRecorder.Eventf(
				fedObject,
				corev1.EventTypeWarning,
				EventReasonParseOverridePolicyFailed,
				"failed to parse overrides from %s %q: %v",
				util.GetResourceKind(policy),
				policy.GetKey(),
				err.Error(),
			)
			return worker.StatusError
		}
		overrides = mergeOverrides(overrides, newOverrides)
	}

	currentOverrides, err := util.GetOverrides(fedObject, PrefixedControllerName)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to get overrides from %s %q: %w", kind, key, err))
		return worker.StatusError
	}

	needsUpdate := !equality.Semantic.DeepEqual(overrides, currentOverrides)

	if needsUpdate {
		err = util.SetOverrides(fedObject, PrefixedControllerName, overrides)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to set overrides for %s %q: %w", kind, key, err))
			return worker.StatusError
		}
	}

	pendingControllersUpdated, err := pendingcontrollers.UpdatePendingControllers(
		fedObject,
		PrefixedControllerName,
		needsUpdate,
		c.typeConfig.GetControllers(),
	)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to update pending controllers for %s %q: %w", kind, key, err))
		return worker.StatusError
	}
	needsUpdate = needsUpdate || pendingControllersUpdated

	if needsUpdate {
		_, err = c.federatedClient.Resources(fedObject.GetNamespace()).
			Update(context.TODO(), fedObject, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			utilruntime.HandleError(fmt.Errorf("failed to update %s %q for applying overrides: %w", kind, key, err))
			return worker.StatusAllOK
		}

		c.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeNormal,
			EventReasonOverridePolicyApplied,
			"updated overrides according to override polic(ies)",
		)
	}

	return worker.StatusAllOK
}

func (c *Controller) getPlacedClusters(fedObject *unstructured.Unstructured) ([]*fedcorev1a1.FederatedCluster, error) {
	placementObj, err := util.UnmarshalGenericPlacements(fedObject)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal placements: %w", err)
	}

	placedClusterNames := placementObj.ClusterNameUnion()

	clusterObjs := c.clusterStore.List()
	placedClusters := make([]*fedcorev1a1.FederatedCluster, 0, len(clusterObjs))
	for _, clusterObj := range clusterObjs {
		cluster, ok := clusterObj.(*fedcorev1a1.FederatedCluster)
		if !ok {
			return nil, fmt.Errorf("got wrong type %T from cluster store", cluster)
		}
		if _, exists := placedClusterNames[cluster.Name]; exists {
			placedClusters = append(placedClusters, cluster)
		}
	}

	return placedClusters, nil
}

func (c *Controller) Run(stopChan <-chan struct{}) {
	go c.federatedController.Run(stopChan)
	go c.overridePolicyController.Run(stopChan)
	go c.clusterOverridePolicyController.Run(stopChan)
	go c.clusterController.Run(stopChan)

	if !cache.WaitForNamedCacheSync(c.name, stopChan,
		c.federatedController.HasSynced,
		c.overridePolicyController.HasSynced,
		c.clusterOverridePolicyController.HasSynced,
		c.clusterController.HasSynced,
	) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync for controller: %s", c.name))
	}
	c.worker.Run(stopChan)
}

func (c *Controller) getFederatedObject(qualifiedName common.QualifiedName) (*unstructured.Unstructured, error) {
	cachedObj, exist, err := c.federatedStore.GetByKey(qualifiedName.String())
	if err != nil {
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(*unstructured.Unstructured).DeepCopy(), nil
}
