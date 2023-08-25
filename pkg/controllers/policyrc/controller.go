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

package policyrc

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	ControllerName = "policyrc-controller"
)

type Controller struct {
	fedObjectInformer                fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer         fedcorev1a1informers.ClusterFederatedObjectInformer
	overridePolicyInformer           fedcorev1a1informers.OverridePolicyInformer
	clusterOverridePolicyInformer    fedcorev1a1informers.ClusterOverridePolicyInformer
	propagationPolicyInformer        fedcorev1a1informers.PropagationPolicyInformer
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer

	ppCounter, opCounter *Counter

	// updates the local counter upon fed object updates
	countWorker worker.ReconcileWorker[common.QualifiedName]
	// pushes values from local counter to apiserver
	persistPpWorker, persistOpWorker worker.ReconcileWorker[common.QualifiedName]

	client  generic.Client
	metrics stats.Metrics
	logger  klog.Logger
}

func NewPolicyRCController(
	restConfig *rest.Config,
	fedInformerFactory fedinformers.SharedInformerFactory,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		client:                           generic.NewForConfigOrDie(restConfig),
		fedObjectInformer:                fedInformerFactory.Core().V1alpha1().FederatedObjects(),
		clusterFedObjectInformer:         fedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		propagationPolicyInformer:        fedInformerFactory.Core().V1alpha1().PropagationPolicies(),
		clusterPropagationPolicyInformer: fedInformerFactory.Core().V1alpha1().ClusterPropagationPolicies(),
		overridePolicyInformer:           fedInformerFactory.Core().V1alpha1().OverridePolicies(),
		clusterOverridePolicyInformer:    fedInformerFactory.Core().V1alpha1().ClusterOverridePolicies(),
		metrics:                          metrics,
		logger:                           logger.WithValues("controller", ControllerName),
	}

	c.countWorker = worker.NewReconcileWorker[common.QualifiedName](
		"policyrc-controller-count-worker",
		c.reconcileCount,
		worker.RateLimiterOptions{},
		1, // currently only one worker is meaningful due to the global mutex
		metrics,
	)

	c.persistPpWorker = worker.NewReconcileWorker[common.QualifiedName](
		"policyrc-controller-persist-worker",
		func(ctx context.Context, qualifiedName common.QualifiedName) worker.Result {
			return c.reconcilePersist(
				ctx,
				"propagation_policy_reference_count",
				qualifiedName,
				c.propagationPolicyInformer.Informer().GetStore(),
				c.clusterPropagationPolicyInformer.Informer().GetStore(),
				c.ppCounter,
			)
		},
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	c.persistOpWorker = worker.NewReconcileWorker[common.QualifiedName](
		"policyrc-controller-persist-worker",
		func(ctx context.Context, qualifiedName common.QualifiedName) worker.Result {
			return c.reconcilePersist(
				ctx,
				"override_policy_reference_count",
				qualifiedName,
				c.overridePolicyInformer.Informer().GetStore(),
				c.clusterOverridePolicyInformer.Informer().GetStore(),
				c.opCounter,
			)
		},
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	if _, err := c.fedObjectInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChangesWithTransform(
		common.NewQualifiedName,
		c.countWorker.Enqueue,
	)); err != nil {
		return nil, err
	}

	if _, err := c.clusterFedObjectInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChangesWithTransform(
		common.NewQualifiedName,
		c.countWorker.Enqueue,
	)); err != nil {
		return nil, err
	}

	if _, err := c.propagationPolicyInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChangesWithTransform(common.NewQualifiedName, c.persistPpWorker.Enqueue),
	); err != nil {
		return nil, err
	}

	if _, err := c.clusterPropagationPolicyInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChangesWithTransform(common.NewQualifiedName, c.persistPpWorker.Enqueue),
	); err != nil {
		return nil, err
	}

	if _, err := c.overridePolicyInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChangesWithTransform(common.NewQualifiedName, c.persistOpWorker.Enqueue),
	); err != nil {
		return nil, err
	}

	if _, err := c.clusterOverridePolicyInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChangesWithTransform(common.NewQualifiedName, c.persistPpWorker.Enqueue),
	); err != nil {
		return nil, err
	}

	c.ppCounter = NewCounter(func(keys []PolicyKey) {
		for _, key := range keys {
			c.persistPpWorker.Enqueue(common.QualifiedName(key))
		}
	})

	c.opCounter = NewCounter(func(keys []PolicyKey) {
		for _, key := range keys {
			c.persistOpWorker.Enqueue(common.QualifiedName(key))
		}
	})

	return c, nil
}

func (c *Controller) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer c.logger.Info("Stopping controller")

	// wait for all counts to finish sync before persisting the values
	if !cache.WaitForNamedCacheSync(ControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for caches to sync")
		return
	}
	logger.Info("Caches are synced")
	c.countWorker.Run(ctx)
	c.persistPpWorker.Run(ctx)
	c.persistOpWorker.Run(ctx)
	<-ctx.Done()
}

func (c *Controller) HasSynced() bool {
	return c.propagationPolicyInformer.Informer().HasSynced() &&
		c.clusterPropagationPolicyInformer.Informer().HasSynced() &&
		c.overridePolicyInformer.Informer().HasSynced() &&
		c.clusterOverridePolicyInformer.Informer().HasSynced() &&
		c.fedObjectInformer.Informer().HasSynced() &&
		c.clusterFedObjectInformer.Informer().HasSynced()
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func (c *Controller) reconcileCount(ctx context.Context, qualifiedName common.QualifiedName) (status worker.Result) {
	//nolint:staticcheck
	ctx, logger := logging.InjectLoggerValues(ctx, "object", qualifiedName.String())

	c.metrics.Counter(metrics.PolicyRCCountControllerThroughput, 1)
	logger.V(3).Info("Policyrc count controller starting to reconcile")
	startTime := time.Now()
	defer func() {
		c.metrics.Duration(metrics.PolicyRCCountLatency, startTime)
		logger.V(3).WithValues("duration", time.Since(startTime), "status", status.String()).
			Info("Policyrc count controller finished reconciling")
	}()

	fedObj, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		qualifiedName.Namespace,
		qualifiedName.Name,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get federated object")
		return worker.StatusError
	}

	var newPps []PolicyKey
	if fedObj != nil {
		newPolicy, newHasPolicy := scheduler.GetMatchedPolicyKey(fedObj)
		if newHasPolicy {
			newPps = []PolicyKey{PolicyKey(newPolicy)}
		}
	} else {
		// we still want to remove the count from the cache.
	}
	c.ppCounter.Update(ObjectKey(qualifiedName), newPps)

	var newOps []PolicyKey
	if fedObj != nil {
		if op, exists := fedObj.GetLabels()[override.OverridePolicyNameLabel]; exists {
			newOps = append(newOps, PolicyKey{Namespace: fedObj.GetNamespace(), Name: op})
		}
		if cop, exists := fedObj.GetLabels()[override.ClusterOverridePolicyNameLabel]; exists {
			newOps = append(newOps, PolicyKey{Name: cop})
		}
	} else {
		// we still want to remove the count from the cache.
	}
	c.opCounter.Update(ObjectKey(qualifiedName), newOps)

	return worker.StatusAllOK
}

func (c *Controller) reconcilePersist(
	ctx context.Context,
	metricName string,
	qualifiedName common.QualifiedName,
	nsScopeStore, clusterScopeStore cache.Store,
	counter *Counter,
) worker.Result {
	ctx, logger := logging.InjectLoggerValues(ctx, "object", qualifiedName.String())

	c.metrics.Counter(metrics.PolicyRCPersistThroughput, 1)
	logger.V(3).Info("Policyrc persist controller starting to reconcile")
	startTime := time.Now()
	defer func() {
		c.metrics.Duration(metrics.PolicyRCPersistLatency, startTime)
		logger.V(3).
			WithValues("duration", time.Since(startTime)).
			Info("Policyrc persist controller finished reconciling")
	}()

	store := clusterScopeStore
	if qualifiedName.Namespace != "" {
		store = nsScopeStore
	}

	policyAny, exists, err := store.GetByKey(qualifiedName.String())
	if err != nil {
		logger.Error(err, "Failed to get policy")
		return worker.StatusError
	}

	if !exists {
		// wait for the object to get created, which would trigger another reconcile
		return worker.StatusAllOK
	}

	policy := policyAny.(fedcorev1a1.GenericRefCountedPolicy)
	policy = policy.DeepCopyObject().(fedcorev1a1.GenericRefCountedPolicy)

	status := policy.GetRefCountedStatus()

	newRefCount := counter.GetPolicyCounts([]PolicyKey{PolicyKey(qualifiedName)})[0]

	hasChange := false
	if newRefCount != status.RefCount {
		status.RefCount = newRefCount
		hasChange = true
	}

	if hasChange {
		err := c.client.UpdateStatus(ctx, policy)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			logger.Error(err, "Failed to update policy status")
			return worker.StatusError
		}
	}

	c.metrics.Store(metricName, newRefCount, []stats.Tag{
		{Name: "name", Value: qualifiedName.Name},
		{Name: "namespace", Value: qualifiedName.Namespace},
	}...)

	return worker.StatusAllOK
}
