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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/meta"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	ControllerName                       = "overridepolicy-controller"
	EventReasonMatchOverridePolicyFailed = "MatchOverridePolicyFailed"
	EventReasonParseOverridePolicyFailed = "ParseOverridePolicyFailed"
	EventReasonOverridePolicyApplied     = "OverridePolicyApplied"
)

var PrefixedControllerName = common.DefaultPrefix + ControllerName

// Controller adds override rules specified in OverridePolicies
// to federated objects.
type Controller struct {
	worker worker.ReconcileWorker[common.QualifiedName]

	informerManager               informermanager.InformerManager
	fedObjectInformer             fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer      fedcorev1a1informers.ClusterFederatedObjectInformer
	overridePolicyInformer        fedcorev1a1informers.OverridePolicyInformer
	clusterOverridePolicyInformer fedcorev1a1informers.ClusterOverridePolicyInformer
	federatedClusterInformer      fedcorev1a1informers.FederatedClusterInformer

	fedClient     fedclient.Interface
	eventRecorder record.EventRecorder
	metrics       stats.Metrics
	logger        klog.Logger
}

func NewOverridePolicyController(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,
	fedInformerFactory fedinformers.SharedInformerFactory,
	informerManager informermanager.InformerManager,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		informerManager:               informerManager,
		fedObjectInformer:             fedInformerFactory.Core().V1alpha1().FederatedObjects(),
		clusterFedObjectInformer:      fedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		overridePolicyInformer:        fedInformerFactory.Core().V1alpha1().OverridePolicies(),
		clusterOverridePolicyInformer: fedInformerFactory.Core().V1alpha1().ClusterOverridePolicies(),
		federatedClusterInformer:      fedInformerFactory.Core().V1alpha1().FederatedClusters(),
		fedClient:                     fedClient,
		metrics:                       metrics,
		logger:                        logger.WithValues("controller", ControllerName),
	}

	c.eventRecorder = eventsink.NewDefederatingRecorderMux(kubeClient, ControllerName, 4)
	c.worker = worker.NewReconcileWorker[common.QualifiedName](
		ControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	if _, err := c.fedObjectInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChanges(func(o pkgruntime.Object) {
		fedObj := o.(*fedcorev1a1.FederatedObject)
		c.worker.Enqueue(common.QualifiedName{Namespace: fedObj.Namespace, Name: fedObj.Name})
	})); err != nil {
		return nil, err
	}

	if _, err := c.clusterFedObjectInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChanges(func(o pkgruntime.Object) {
		fedObj := o.(*fedcorev1a1.ClusterFederatedObject)
		c.worker.Enqueue(common.QualifiedName{Name: fedObj.Name})
	})); err != nil {
		return nil, err
	}

	if _, err := c.overridePolicyInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChanges(func(o pkgruntime.Object) {
		policy := o.(fedcorev1a1.GenericOverridePolicy)
		c.enqueueFedObjectsUsingPolicy(policy, OverridePolicyNameLabel)
	})); err != nil {
		return nil, err
	}

	if _, err := c.clusterOverridePolicyInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChanges(func(o pkgruntime.Object) {
		policy := o.(fedcorev1a1.GenericOverridePolicy)
		c.enqueueFedObjectsUsingPolicy(policy, OverridePolicyNameLabel)
	})); err != nil {
		return nil, err
	}

	if _, err := c.federatedClusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
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
	}); err != nil {
		return nil, err
	}

	if err := informerManager.AddFTCUpdateHandler(func(lastObserved, latest *fedcorev1a1.FederatedTypeConfig) {
		if lastObserved == nil && latest != nil {
			c.enqueueFederatedObjectsForFTC(latest)
			return
		}
	}); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Controller) enqueueFederatedObjectsForFTC(ftc *fedcorev1a1.FederatedTypeConfig) {
	logger := c.logger.WithValues("ftc", ftc.GetName())

	logger.V(2).Info("Enqueue federated objects for FTC")

	allObjects, err := fedobjectadapters.ListAllFedObjsForFTC(ftc, c.fedObjectInformer, c.clusterFedObjectInformer)
	if err != nil {
		c.logger.Error(err, "Failed to list objects for FTC")
		return
	}

	for _, obj := range allObjects {
		c.worker.Enqueue(common.NewQualifiedName(obj))
	}
}

func (c *Controller) enqueueFedObjectsUsingPolicy(policy fedcorev1a1.GenericOverridePolicy, labelKey string) {
	logger := c.logger.WithValues("override-policy", policy.GetKey())
	logger.V(2).Info("observed a policy change")

	selector := labels.SelectorFromSet(labels.Set{labelKey: policy.GetName()})
	clusterFedObjects, err := c.clusterFedObjectInformer.Lister().List(selector)
	if err != nil {
		logger.Error(err, "Failed to list reference cluster federated objects")
		return
	}

	for _, clusterFedObject := range clusterFedObjects {
		c.worker.Enqueue(common.QualifiedName{Name: clusterFedObject.GetName()})
	}

	if policy.GetNamespace() != "" {
		fedObjects, err := c.fedObjectInformer.Lister().FederatedObjects(policy.GetNamespace()).List(selector)
		if err != nil {
			logger.Error(err, "Failed to list reference federated objects")
			return
		}

		for _, fedObject := range fedObjects {
			c.worker.Enqueue(common.QualifiedName{
				Namespace: fedObject.GetNamespace(),
				Name:      fedObject.GetName(),
			})
		}
	}
}

func (c *Controller) reconcileOnClusterChange(cluster *fedcorev1a1.FederatedCluster) {
	logger := c.logger.WithValues("federated-cluster", cluster.GetName())
	logger.V(2).Info("Observed a cluster change")

	opRequirement, _ := labels.NewRequirement(OverridePolicyNameLabel, selection.Exists, nil)
	copRequirement, _ := labels.NewRequirement(ClusterOverridePolicyNameLabel, selection.Exists, nil)

	for _, requirement := range []labels.Requirement{*opRequirement, *copRequirement} {
		fedObjects, err := c.fedObjectInformer.Lister().List(labels.NewSelector().Add(requirement))
		if err != nil {
			logger.Error(err, "Failed to list federated objects")
			return
		}
		for _, fedObject := range fedObjects {
			c.worker.Enqueue(common.QualifiedName{
				Namespace: fedObject.Namespace,
				Name:      fedObject.Name,
			})
		}

		// no need to list cluster federated object for override policy
		if requirement.Key() == ClusterOverridePolicyNameLabel {
			clusterFedObjects, err := c.clusterFedObjectInformer.Lister().List(labels.NewSelector().Add(requirement))
			if err != nil {
				logger.Error(err, "Failed to list cluster federated objects")
				return
			}
			for _, clusterFedObject := range clusterFedObjects {
				c.worker.Enqueue(common.QualifiedName{
					Name: clusterFedObject.Name,
				})
			}
		}
	}
}

func (c *Controller) reconcile(ctx context.Context, qualifiedName common.QualifiedName) (status worker.Result) {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "federated-name", qualifiedName.String())

	c.metrics.Counter(metrics.OverridePolicyControllerThroughput, 1)
	keyedLogger.V(3).Info("Starting to reconcile")
	startTime := time.Now()
	defer func() {
		c.metrics.Duration(metrics.OverridePolicyControllerLatency, startTime)
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status).V(3).Info("Finished reconciling")
	}()

	fedObject, err := c.getFederatedObject(qualifiedName)
	if err != nil {
		keyedLogger.Error(err, "Failed to get federated object")
		return worker.StatusError
	}

	if fedObject == nil || fedObject.GetDeletionTimestamp() != nil {
		return worker.StatusAllOK
	}

	templateMetadata, err := fedObject.GetSpec().GetTemplateMetadata()
	if err != nil {
		keyedLogger.Error(err, "Failed to get template metadata")
		return worker.StatusError
	}
	templateGVK := templateMetadata.GroupVersionKind()

	ctx, keyedLogger = logging.InjectLoggerValues(ctx, "source-gvk", templateGVK.String())
	typeConfig, exist := c.informerManager.GetResourceFTC(templateGVK)
	if !exist {
		keyedLogger.V(3).Info("Resource ftc not found")
		return worker.StatusAllOK
	}

	ctx, keyedLogger = logging.InjectLoggerValues(ctx, "ftc", typeConfig.Name)
	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(fedObject, PrefixedControllerName); err != nil {
		keyedLogger.Error(err, "Failed to check controller dependencies")
		return worker.StatusError
	} else if !ok {
		return worker.StatusAllOK
	}

	// TODO: don't apply a policy until it has the required finalizer for deletion protection
	policies, recheckOnErr, err := lookForMatchedPolicies(
		fedObject,
		fedObject.GetNamespace() != "",
		c.overridePolicyInformer.Lister(),
		c.clusterOverridePolicyInformer.Lister(),
	)
	if err != nil {
		keyedLogger.Error(err, "Failed to look for matched policy")
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
		keyedLogger.Error(err, "Failed to get placed clusters")
		return worker.StatusError
	}

	var overrides, currentOverrides overridesMap
	// Apply overrides from each policy in order
	for _, policy := range policies {
		newOverrides, err := parseOverrides(policy, placedClusters, fedObject)
		if err != nil {
			c.eventRecorder.Eventf(
				fedObject,
				corev1.EventTypeWarning,
				EventReasonParseOverridePolicyFailed,
				"failed to parse overrides from %s %q: %v",
				meta.GetResourceKind(policy),
				policy.GetKey(),
				err.Error(),
			)
			keyedLogger.Error(err, "Failed to parse overrides")
			return worker.StatusError
		}
		overrides = mergeOverrides(overrides, newOverrides)
	}

	currentOverridesList := fedObject.GetSpec().GetControllerOverrides(PrefixedControllerName)
	currentOverrides = convertOverridesListToMap(currentOverridesList)
	needsUpdate := !equality.Semantic.DeepEqual(overrides, currentOverrides)

	if needsUpdate {
		err = setOverrides(fedObject, overrides)
		if err != nil {
			keyedLogger.Error(err, "Failed to set overrides")
			return worker.StatusError
		}
	}

	pendingControllersUpdated, err := pendingcontrollers.UpdatePendingControllers(
		fedObject,
		PrefixedControllerName,
		needsUpdate,
		typeConfig.GetControllers(),
	)
	if err != nil {
		keyedLogger.Error(err, "Failed to update pending controllers")
		return worker.StatusError
	}
	needsUpdate = needsUpdate || pendingControllersUpdated

	if needsUpdate {
		_, err = fedobjectadapters.Update(ctx, c.fedClient.CoreV1alpha1(), fedObject, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			keyedLogger.Error(err, "Failed to update federated object for applying overrides")
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

func (c *Controller) getPlacedClusters(fedObject fedcorev1a1.GenericFederatedObject) ([]*fedcorev1a1.FederatedCluster, error) {
	placedClusterNames := fedObject.GetSpec().GetPlacementUnion()
	clusterObjs, err := c.federatedClusterInformer.Lister().List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list federated cluster: %w", err)
	}

	placedClusters := make([]*fedcorev1a1.FederatedCluster, 0, len(clusterObjs))
	for _, clusterObj := range clusterObjs {
		if _, exists := placedClusterNames[clusterObj.Name]; exists {
			placedClusters = append(placedClusters, clusterObj)
		}
	}

	return placedClusters, nil
}

func (c *Controller) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(ControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for caches to sync")
		return
	}
	logger.Info("Caches are synced")
	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *Controller) HasSynced() bool {
	return c.federatedClusterInformer.Informer().HasSynced() &&
		c.overridePolicyInformer.Informer().HasSynced() &&
		c.clusterOverridePolicyInformer.Informer().HasSynced() &&
		c.fedObjectInformer.Informer().HasSynced() &&
		c.clusterFedObjectInformer.Informer().HasSynced() &&
		c.informerManager.HasSynced()
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func (c *Controller) getFederatedObject(qualifiedName common.QualifiedName) (fedcorev1a1.GenericFederatedObject, error) {
	cachedObj, err := fedobjectadapters.GetFromLister(c.fedObjectInformer.Lister(), c.clusterFedObjectInformer.Lister(),
		qualifiedName.Namespace, qualifiedName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	return cachedObj.DeepCopyGenericFederatedObject(), nil
}
