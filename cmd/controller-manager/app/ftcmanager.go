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
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

const (
	FederateControllerName = "federate-controller"
	GlobalSchedulerName    = "global-scheduler"
)

var knownFTCSubControllers = map[string]StartFTCSubControllerFunc{
	GlobalSchedulerName:    startGlobalScheduler,
	FederateControllerName: startFederateController,
}

// StartFTCSubControllerFunc is responsible for constructing and starting a FTC subcontroller. A FTC subcontroller is started/stopped
// dynamically for every FTC. StartFTCSubControllerFunc should be asynchronous and an error is only returned if we fail to start the
// controller.
//
//nolint:lll
type StartFTCSubControllerFunc func(ctx context.Context, controllerCtx *controllercontext.Context, typeConfig *fedcorev1a1.FederatedTypeConfig) error

type FederatedTypeConfigManager struct {
	informer fedcorev1a1informers.FederatedTypeConfigInformer
	handle   cache.ResourceEventHandlerRegistration

	lock                     sync.Mutex
	registeredSubControllers map[string]StartFTCSubControllerFunc
	subControllerCancelFuncs map[string]context.CancelFunc
	workqueue                workqueue.Interface

	enabledControllers []string
	controllerCtx      *controllercontext.Context

	logger klog.Logger
}

func NewFederatedTypeConfigManager(
	enabledControllers []string,
	informer fedcorev1a1informers.FederatedTypeConfigInformer,
	controllerCtx *controllercontext.Context,
) *FederatedTypeConfigManager {
	m := &FederatedTypeConfigManager{
		informer:                 informer,
		lock:                     sync.Mutex{},
		registeredSubControllers: map[string]StartFTCSubControllerFunc{},
		subControllerCancelFuncs: map[string]context.CancelFunc{},
		workqueue:                workqueue.New(),
		enabledControllers:       enabledControllers,
		controllerCtx:            controllerCtx,
		logger:                   klog.LoggerWithName(klog.TODO(), "federated-type-config-manager"),
	}

	m.handle, _ = informer.Informer().AddEventHandler(util.NewTriggerOnAllChanges(func(o runtime.Object) {
		tc := o.(*fedcorev1a1.FederatedTypeConfig)
		m.enqueueTypeConfig(tc)
	}))

	return m
}

func (m *FederatedTypeConfigManager) RegisterSubController(name string, startFunc StartFTCSubControllerFunc) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.registeredSubControllers[name] = startFunc
}

func (m *FederatedTypeConfigManager) Start(ctx context.Context) {
	go func() {
		m.logger.Info("Starting FederatedTypeConfig manager")
		defer m.logger.Info("Stopping FederatedTypeConfig manager")
		defer m.workqueue.ShutDown()
		defer m.informer.Informer().RemoveEventHandler(m.handle)

		if !cache.WaitForNamedCacheSync("federated-type-config-manager", ctx.Done(), m.informer.Informer().HasSynced) {
			return
		}

		go wait.UntilWithContext(ctx, m.processQueueItem, 0)
		<-ctx.Done()
	}()
}

func (m *FederatedTypeConfigManager) enqueueTypeConfig(obj runtime.Object) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		m.logger.Error(err, "Failed to enqueue federated type config")
	}
	m.workqueue.Add(key)
}

func (m *FederatedTypeConfigManager) processQueueItem(ctx context.Context) {
	key, quit := m.workqueue.Get()
	if quit {
		return
	}
	defer m.workqueue.Done(key)
	keyedLogger := m.logger.WithValues("federated-type-config", key)

	keyedLogger.V(4).Info("Process queue item")

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		keyedLogger.Error(err, "Failed to process queue item")
		return
	}

	typeConfig, err := m.informer.Lister().Get(name)
	if err != nil && apierrors.IsNotFound(err) {
		keyedLogger.V(4).Info("Observed FederatedTypeConfig deletion")
		m.processFTCDeletion(name)
		return
	}
	if err != nil {
		keyedLogger.Error(err, "Failed to process queue item")
		return
	}

	m.lock.Lock()
	defer m.lock.Unlock()
	if _, ok := m.subControllerCancelFuncs[name]; ok {
		keyedLogger.V(4).Info("Subcontrollers already started")
		return
	}

	subControllerCtx, cancel := context.WithCancel(ctx)
	m.subControllerCancelFuncs[name] = cancel

	for controllerName, startFunc := range m.registeredSubControllers {
		if !isControllerEnabled(controllerName, controllersDisabledByDefault, m.enabledControllers) {
			keyedLogger.WithValues("controller", controllerName).Info("Skip starting disabled subcontroller")
			continue
		}

		if err := startFunc(subControllerCtx, m.controllerCtx, typeConfig); err != nil {
			keyedLogger.Error(err, "Failed to start subcontrolelr")
		} else {
			keyedLogger.WithValues("controller", controllerName).Info("Started subcontroller")
		}
	}

	// Since the controllers are created dynamically, we have to start the informer factories again, in case any new
	// informers were accessed. Note that the top level context is used in case a FTC is recreated and the same informer
	// needs to be used again (SharedInformerFactory and SharedInformers do not support restarts).
	m.controllerCtx.KubeInformerFactory.Start(ctx.Done())
	m.controllerCtx.DynamicInformerFactory.Start(ctx.Done())
	m.controllerCtx.FedInformerFactory.Start(ctx.Done())
}

func (m *FederatedTypeConfigManager) processFTCDeletion(ftcName string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	cancel, ok := m.subControllerCancelFuncs[ftcName]
	if !ok {
		return
	}

	cancel()
	delete(m.subControllerCancelFuncs, ftcName)
}
