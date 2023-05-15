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
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

var PolicyrcControllerName = common.DefaultPrefix + "policyrc-controller"

type informerPair struct {
	store      cache.Store
	controller cache.Controller
}

type Controller struct {
	// name of controller: <federatedKind>-policyrc-controller
	name string

	// Informer store and controller for the federated type, PropagationPolicy,
	// ClusterPropagationPolicy, OverridePolicy and ClusterOverridePolicy respectively.
	federated, pp, cpp, op, cop informerPair

	client generic.Client

	ppCounter, opCounter *Counter

	// updates the local counter upon fed object updates
	countWorker worker.ReconcileWorker
	// pushes values from local counter to apiserver
	persistPpWorker, persistOpWorker worker.ReconcileWorker

	typeConfig *fedcorev1a1.FederatedTypeConfig

	metrics stats.Metrics
}

func StartController(controllerConfig *util.ControllerConfig,
	stopChan <-chan struct{}, typeConfig *fedcorev1a1.FederatedTypeConfig,
) error {
	controller, err := newController(controllerConfig, typeConfig)
	if err != nil {
		return err
	}
	klog.V(4).Infof("Starting policyrc controller for %q", typeConfig.GetFederatedType().Kind)
	controller.Run(stopChan)
	return nil
}

func newController(controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
) (*Controller, error) {
	federatedAPIResource := typeConfig.GetFederatedType()

	userAgent := fmt.Sprintf("%s-policyrc-controller", strings.ToLower(federatedAPIResource.Kind))
	configWithUserAgent := rest.CopyConfig(controllerConfig.KubeConfig)
	rest.AddUserAgent(configWithUserAgent, userAgent)

	c := &Controller{
		name:       userAgent,
		typeConfig: typeConfig,
		metrics:    controllerConfig.Metrics,
	}

	c.countWorker = worker.NewReconcileWorker(
		c.reconcileCount,
		worker.WorkerTiming{},
		1, // currently only one worker is meaningful due to the global mutex
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags("policyrc-controller-count-worker", c.typeConfig.GetFederatedType().Kind),
	)

	c.persistPpWorker = worker.NewReconcileWorker(
		func(qualifiedName common.QualifiedName) worker.Result {
			return c.reconcilePersist("propagation-policy", qualifiedName, c.pp.store, c.cpp.store, c.ppCounter)
		},
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags("policyrc-controller-persist-worker", c.typeConfig.GetFederatedType().Kind),
	)
	c.persistOpWorker = worker.NewReconcileWorker(
		func(qualifiedName common.QualifiedName) worker.Result {
			return c.reconcilePersist("override-policy", qualifiedName, c.op.store, c.cop.store, c.opCounter)
		},
		worker.WorkerTiming{},
		controllerConfig.WorkerCount,
		controllerConfig.Metrics,
		delayingdeliver.NewMetricTags("policyrc-controller-persist-worker", c.typeConfig.GetFederatedType().Kind),
	)

	targetNamespace := controllerConfig.TargetNamespace

	federatedClient, err := util.NewResourceClient(configWithUserAgent, &federatedAPIResource)
	if err != nil {
		return nil, err
	}
	c.federated.store, c.federated.controller = util.NewResourceInformer(
		federatedClient,
		targetNamespace,
		c.countWorker.EnqueueObject,
		controllerConfig.Metrics,
	)

	c.client = generic.NewForConfigOrDie(configWithUserAgent)
	c.pp.store, c.pp.controller, err = util.NewGenericInformer(
		configWithUserAgent,
		targetNamespace,
		&fedcorev1a1.PropagationPolicy{},
		0,
		c.persistPpWorker.EnqueueObject,
		controllerConfig.Metrics,
	)
	if err != nil {
		return nil, err
	}

	c.cpp.store, c.cpp.controller, err = util.NewGenericInformer(
		configWithUserAgent,
		targetNamespace,
		&fedcorev1a1.ClusterPropagationPolicy{},
		0,
		c.persistPpWorker.EnqueueObject,
		controllerConfig.Metrics,
	)
	if err != nil {
		return nil, err
	}

	c.op.store, c.op.controller, err = util.NewGenericInformer(
		configWithUserAgent,
		targetNamespace,
		&fedcorev1a1.OverridePolicy{},
		0,
		c.persistOpWorker.EnqueueObject,
		controllerConfig.Metrics,
	)
	if err != nil {
		return nil, err
	}

	c.cop.store, c.cop.controller, err = util.NewGenericInformer(
		configWithUserAgent,
		targetNamespace,
		&fedcorev1a1.ClusterOverridePolicy{},
		0,
		c.persistOpWorker.EnqueueObject,
		controllerConfig.Metrics,
	)
	if err != nil {
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

func (c *Controller) Run(stopChan <-chan struct{}) {
	for _, pair := range []informerPair{c.federated, c.pp, c.cpp, c.op, c.cop} {
		go pair.controller.Run(stopChan)
	}

	c.countWorker.Run(stopChan)

	// wait for all counts to finish sync before persisting the values
	if !cache.WaitForNamedCacheSync(c.name, stopChan, c.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync for controller: %s", c.name))
	}
	c.persistPpWorker.Run(stopChan)
	c.persistOpWorker.Run(stopChan)
}

func (c *Controller) HasSynced() bool {
	return c.federated.controller.HasSynced()
}

func (c *Controller) reconcileCount(qualifiedName common.QualifiedName) worker.Result {
	federatedKind := c.typeConfig.GetFederatedType().Kind
	key := qualifiedName.String()

	c.metrics.Rate("policyrc-count-controller.throughput", 1)
	klog.V(4).Infof("policyrc count controller for %v starting to reconcile %v", federatedKind, key)
	startTime := time.Now()
	defer func() {
		c.metrics.Duration("policyrc-count-controller.latency", startTime)
		klog.V(4).
			Infof("policyrc count controller for %v finished reconciling %v (duration: %v)", federatedKind, key, time.Since(startTime))
	}()

	fedObjAny, fedObjExists, err := c.federated.store.GetByKey(qualifiedName.String())
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}
	var fedObj *unstructured.Unstructured
	if fedObjExists {
		fedObj = fedObjAny.(*unstructured.Unstructured)
	}

	var newPps []PolicyKey
	if fedObjExists {
		newPolicy, newHasPolicy := scheduler.MatchedPolicyKey(fedObj, c.typeConfig.GetNamespaced())
		if newHasPolicy {
			newPps = []PolicyKey{PolicyKey(newPolicy)}
		}
	} else {
		// we still want to remove the count from the cache.
	}
	c.ppCounter.Update(ObjectKey(qualifiedName), newPps)

	var newOps []PolicyKey
	if fedObjExists {
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
	metricName string,
	qualifiedName common.QualifiedName,
	nsScopeStore, clusterScopeStore cache.Store,
	counter *Counter,
) worker.Result {
	federatedKind := c.typeConfig.GetFederatedType().Kind
	key := qualifiedName.String()

	c.metrics.Rate(fmt.Sprintf("policyrc-persist-%s-controller.throughput", metricName), 1)
	klog.V(4).Infof("policyrc persist %s controller for %v starting to reconcile %v", metricName, federatedKind, key)
	startTime := time.Now()
	defer func() {
		c.metrics.Duration(fmt.Sprintf("policyrc-persist-%s-controller.latency", metricName), startTime)
		klog.V(4).Infof(
			"policyrc persist %s controller for %v finished reconciling %v (duration: %v)",
			metricName, federatedKind, key, time.Since(startTime),
		)
	}()

	store := clusterScopeStore
	if qualifiedName.Namespace != "" {
		store = nsScopeStore
	}

	policyAny, exists, err := store.GetByKey(qualifiedName.String())
	if err != nil {
		utilruntime.HandleError(err)
		return worker.StatusError
	}

	if !exists {
		// wait for the object to get created, which would trigger another reconcile
		return worker.StatusAllOK
	}

	policy := policyAny.(fedcorev1a1.GenericRefCountedPolicy)
	policy = policy.DeepCopyObject().(fedcorev1a1.GenericRefCountedPolicy)

	status := policy.GetRefCountedStatus()

	group := c.typeConfig.GetTargetType().Group
	resource := c.typeConfig.GetTargetType().Name

	var matchedTypedRefCount *fedcorev1a1.TypedRefCount
	for i := range status.TypedRefCount {
		typed := &status.TypedRefCount[i]
		if typed.Group == group && typed.Resource == resource {
			matchedTypedRefCount = typed
			break
		}
	}

	if matchedTypedRefCount == nil {
		status.TypedRefCount = append(status.TypedRefCount, fedcorev1a1.TypedRefCount{
			Group:    group,
			Resource: resource,
		})
		matchedTypedRefCount = &status.TypedRefCount[len(status.TypedRefCount)-1]
	}

	newTypedRefCount := counter.GetPolicyCounts([]PolicyKey{PolicyKey(qualifiedName)})[0]

	hasChange := false
	if newTypedRefCount != matchedTypedRefCount.Count {
		matchedTypedRefCount.Count = newTypedRefCount
		hasChange = true
	}

	sum := int64(0)
	for _, typed := range status.TypedRefCount {
		sum += typed.Count
	}
	if sum != status.RefCount {
		status.RefCount = sum
		hasChange = true
	}

	if hasChange {
		err := c.client.UpdateStatus(context.TODO(), policy)
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
