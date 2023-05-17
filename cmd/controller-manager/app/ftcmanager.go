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

package app

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllermanager"
	"github.com/kubewharf/kubeadmiral/pkg/controllermanager/healthcheck"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	FederateControllerName      = "federate"
	GlobalSchedulerName         = "scheduler"
	AutoMigrationControllerName = "automigration"
)

var knownFTCSubControllers = map[string]controllermanager.FTCSubControllerInitFuncs{
	GlobalSchedulerName: {
		StartFunc:     startGlobalScheduler,
		IsEnabledFunc: isGlobalSchedulerEnabled,
	},
	FederateControllerName: {
		StartFunc:     startFederateController,
		IsEnabledFunc: isFederateControllerEnabled,
	},
	AutoMigrationControllerName: {
		StartFunc:     startAutoMigrationController,
		IsEnabledFunc: isAutoMigrationControllerEnabled,
	},
}

type FederatedTypeConfigManager struct {
	informer fedcorev1a1informers.FederatedTypeConfigInformer
	handle   cache.ResourceEventHandlerRegistration

	lock                        sync.Mutex
	registeredSubControllers    map[string]controllermanager.StartFTCSubControllerFunc
	isSubControllerEnabledFuncs map[string]controllermanager.IsFTCSubControllerEnabledFunc

	subControllerContexts    map[string]context.Context
	subControllerCancelFuncs map[string]context.CancelFunc
	startedSubControllers    map[string]sets.Set[string]

	healthCheckHandler *healthcheck.MutableHealthCheckHandler
	worker             worker.ReconcileWorker
	controllerCtx      *controllercontext.Context

	metrics stats.Metrics
	logger  klog.Logger
}

func NewFederatedTypeConfigManager(
	informer fedcorev1a1informers.FederatedTypeConfigInformer,
	controllerCtx *controllercontext.Context,
	healthCheckHandler *healthcheck.MutableHealthCheckHandler,
	metrics stats.Metrics,
) *FederatedTypeConfigManager {
	m := &FederatedTypeConfigManager{
		informer:                    informer,
		lock:                        sync.Mutex{},
		registeredSubControllers:    map[string]controllermanager.StartFTCSubControllerFunc{},
		isSubControllerEnabledFuncs: map[string]controllermanager.IsFTCSubControllerEnabledFunc{},
		subControllerContexts:       map[string]context.Context{},
		subControllerCancelFuncs:    map[string]context.CancelFunc{},
		startedSubControllers:       map[string]sets.Set[string]{},
		controllerCtx:               controllerCtx,
		healthCheckHandler:          healthCheckHandler,
		metrics:                     metrics,
		logger:                      klog.LoggerWithValues(klog.Background(), "controller", "federated-type-config-manager"),
	}

	m.worker = worker.NewReconcileWorker(
		m.reconcile,
		worker.WorkerTiming{},
		1,
		metrics,
		delayingdeliver.NewMetricTags("federated-type-config-manager", "FederatedTypeConfig"),
	)

	m.handle, _ = informer.Informer().AddEventHandler(util.NewTriggerOnAllChanges(m.worker.EnqueueObject))

	return m
}

func (m *FederatedTypeConfigManager) RegisterSubController(
	name string,
	startFunc controllermanager.StartFTCSubControllerFunc,
	isEnabledFunc controllermanager.IsFTCSubControllerEnabledFunc,
) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.registeredSubControllers[name] = startFunc
	m.isSubControllerEnabledFuncs[name] = isEnabledFunc
}

func (m *FederatedTypeConfigManager) Run(ctx context.Context) {
	m.logger.Info("Starting FederatedTypeConfig manager")
	defer m.logger.Info("Stopping FederatedTypeConfig manager")

	if !cache.WaitForNamedCacheSync("federated-type-config-manager", ctx.Done(), m.informer.Informer().HasSynced) {
		return
	}

	m.worker.Run(ctx.Done())
	<-ctx.Done()
}

func (m *FederatedTypeConfigManager) reconcile(qualifiedName common.QualifiedName) (status worker.Result) {
	_ = m.metrics.Rate("federated-type-config-manager.throughput", 1)
	key := qualifiedName.String()
	logger := m.logger.WithValues("federated-type-config", key)
	startTime := time.Now()

	logger.V(3).Info("Start reconcile")
	defer m.metrics.Duration("federated-type-config-manager.latency", startTime)
	defer func() {
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	typeConfig, err := m.informer.Lister().Get(qualifiedName.Name)
	if err != nil && apierrors.IsNotFound(err) {
		logger.V(3).Info("Observed FederatedTypeConfig deletion")
		m.processFTCDeletion(qualifiedName.Name)
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get FederatedTypeConfig")
		return worker.StatusError
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	startedSubControllers, ok := m.startedSubControllers[qualifiedName.Name]
	if !ok {
		startedSubControllers = sets.New[string]()
		m.startedSubControllers[qualifiedName.Name] = startedSubControllers
	}
	subControllerCtx, ok := m.subControllerContexts[qualifiedName.Name]
	if !ok {
		subControllerCtx, m.subControllerCancelFuncs[qualifiedName.Name] = context.WithCancel(context.TODO())
		m.subControllerContexts[qualifiedName.Name] = subControllerCtx
	}

	needRetry := false
	for controllerName, startFunc := range m.registeredSubControllers {
		logger := logger.WithValues("subcontroller", controllerName)

		if startedSubControllers.Has(controllerName) {
			logger.V(3).Info("Subcontroller already started")
			continue
		}

		isEnabledFunc := m.isSubControllerEnabledFuncs[controllerName]
		if isEnabledFunc != nil && !isEnabledFunc(typeConfig) {
			logger.V(3).Info("Skip starting subcontroller, is disabled")
			continue
		}

		controller, err := startFunc(subControllerCtx, m.controllerCtx, typeConfig)
		if err != nil {
			logger.Error(err, "Failed to start subcontroller")
			needRetry = true
			continue
		} else {
			logger.Info("Started subcontroller")
			startedSubControllers.Insert(controllerName)
		}

		m.healthCheckHandler.AddReadyzChecker(
			resolveSubcontrollerName(controllerName, qualifiedName.Name),
			func(_ *http.Request) error {
				if controller.IsControllerReady() {
					return nil
				}
				return fmt.Errorf("controller not ready")
			},
		)
	}

	// Since the controllers are created dynamically, we have to start the informer factories again, in case any new
	// informers were accessed. Note that a different context is used in case a FTC is recreated and the same informer
	// needs to be used again (SharedInformerFactory and SharedInformers do not support restarts).
	ctx := context.TODO()
	m.controllerCtx.KubeInformerFactory.Start(ctx.Done())
	m.controllerCtx.DynamicInformerFactory.Start(ctx.Done())
	m.controllerCtx.FedInformerFactory.Start(ctx.Done())

	if needRetry {
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (m *FederatedTypeConfigManager) processFTCDeletion(ftcName string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	cancel, ok := m.subControllerCancelFuncs[ftcName]
	if !ok {
		return
	}

	cancel()

	for controller := range m.startedSubControllers[ftcName] {
		m.healthCheckHandler.RemoveReadyzChecker(resolveSubcontrollerName(controller, ftcName))
	}

	delete(m.subControllerCancelFuncs, ftcName)
	delete(m.subControllerContexts, ftcName)
	delete(m.startedSubControllers, ftcName)
}

func resolveSubcontrollerName(baseName, ftcName string) string {
	return fmt.Sprintf("%s[%s]", ftcName, baseName)
}
