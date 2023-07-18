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

package informermanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/bijection"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
)

type informerManager struct {
	lock sync.RWMutex

	started  bool
	shutdown bool

	client                   dynamic.Interface
	informerTweakListOptions dynamicinformer.TweakListOptionsFunc
	ftcInformer              fedcorev1a1informers.FederatedTypeConfigInformer

	eventHandlerGenerators []*EventHandlerGenerator

	gvkMapping *bijection.Bijection[string, schema.GroupVersionKind]

	lastObservedFTCs          map[string]*fedcorev1a1.FederatedTypeConfig
	informers                 map[string]informers.GenericInformer
	informerCancelFuncs       map[string]context.CancelFunc
	eventHandlerRegistrations map[string]map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration
	lastAppliedFTCsCache      map[string]map[*EventHandlerGenerator]*fedcorev1a1.FederatedTypeConfig

	queue workqueue.RateLimitingInterface
}

func NewInformerManager(
	client dynamic.Interface,
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer,
	informerTweakListOptions dynamicinformer.TweakListOptionsFunc,
) InformerManager {
	manager := &informerManager{
		lock:                      sync.RWMutex{},
		started:                   false,
		client:                    client,
		informerTweakListOptions:  informerTweakListOptions,
		ftcInformer:               ftcInformer,
		eventHandlerGenerators:    []*EventHandlerGenerator{},
		gvkMapping:                bijection.NewBijection[string, schema.GroupVersionKind](),
		lastObservedFTCs:          map[string]*fedcorev1a1.FederatedTypeConfig{},
		informers:                 map[string]informers.GenericInformer{},
		informerCancelFuncs:       map[string]context.CancelFunc{},
		eventHandlerRegistrations: map[string]map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration{},
		lastAppliedFTCsCache:      map[string]map[*EventHandlerGenerator]*fedcorev1a1.FederatedTypeConfig{},
		queue:                     workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
	}

	ftcInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { manager.enqueue(obj) },
		UpdateFunc: func(_ interface{}, obj interface{}) { manager.enqueue(obj) },
		DeleteFunc: func(obj interface{}) { manager.enqueue(obj) },
	})

	return manager
}

func (m *informerManager) enqueue(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.Error(err, "informer-manager: Failed to enqueue FederatedTypeConfig")
		return
	}
	m.queue.Add(key)
}

func (m *informerManager) worker(ctx context.Context) {
	key, shutdown := m.queue.Get()
	if shutdown {
		return
	}
	defer m.queue.Done(key)

	logger := klog.FromContext(ctx)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		logger.Error(err, "Failed to process FederatedTypeConfig")
		return
	}

	ctx, logger = logging.InjectLoggerValues(ctx, "ftc", name)

	ftc, err := m.ftcInformer.Lister().Get(name)
	if apierrors.IsNotFound(err) {
		if err := m.processFTCDeletion(ctx, name); err != nil {
			logger.Error(err, "Failed to process FederatedTypeConfig, will retry")
			m.queue.AddRateLimited(key)
		} else {
			m.queue.Forget(key)
		}
		return
	}
	if err != nil {
		logger.Error(err, "Failed to get FederatedTypeConfig from lister, will retry")
		m.queue.AddRateLimited(key)
		return
	}

	err, needReenqueue, delay := m.processFTC(ctx, ftc)
	if err != nil {
		if needReenqueue {
			logger.Error(err, "Failed to process FederatedTypeConfig, will retry")
			m.queue.AddRateLimited(key)
		} else {
			logger.Error(err, "Failed to process FederatedTypeConfig")
			m.queue.Forget(key)
		}
		return
	}

	m.queue.Forget(key)
	if needReenqueue {
		m.queue.AddAfter(key, delay)
	}
}

func (m *informerManager) processFTC(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
) (err error, needReenqueue bool, reenqueueDelay time.Duration) {
	m.lock.Lock()
	defer m.lock.Unlock()

	ftc = ftc.DeepCopy()
	ftcName := ftc.Name
	gvk := ftc.GetSourceTypeGVK()
	gvr := ftc.GetSourceTypeGVR()

	ctx, logger := logging.InjectLoggerValues(ctx, "gvk", gvk.String())

	var informer informers.GenericInformer

	if oldGVK, exists := m.gvkMapping.LookupByT1(ftcName); exists {
		ctx, _ := logging.InjectLoggerValues(ctx, "old-gvk", oldGVK.String())

		if oldGVK != gvk {
			// This might occur if a ftc was deleted and recreated with a different source type within a short period of
			// time and we missed processing the deletion. We simply process the ftc deletion and reenqueue. Note:
			// updating of ftc source types, however, is still not a supported use case.
			err := m.processFTCDeletionUnlocked(ctx, ftcName)
			return err, true, 0
		}

		informer = m.informers[ftcName]
	} else {
		if err := m.gvkMapping.Add(ftcName, gvk); err != nil {
			// There must be another ftc with the same source type GVK.
			return fmt.Errorf("source type is already referenced by another FederatedTypeConfig: %w", err), false, 0
		}

		logger.V(2).Info("Starting new informer for FederatedTypeConfig")

		informer = dynamicinformer.NewFilteredDynamicInformer(
			m.client,
			gvr,
			metav1.NamespaceAll,
			0,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			m.informerTweakListOptions,
		)
		ctx, cancel := context.WithCancel(ctx)
		go informer.Informer().Run(ctx.Done())

		m.lastObservedFTCs[ftcName] = ftc
		m.informers[ftcName] = informer
		m.informerCancelFuncs[ftcName] = cancel
		m.eventHandlerRegistrations[ftcName] = map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration{}
		m.lastAppliedFTCsCache[ftcName] = map[*EventHandlerGenerator]*fedcorev1a1.FederatedTypeConfig{}
	}

	if !informer.Informer().HasSynced() {
		logger.V(3).Info("Informer for FederatedTypeConfig not synced, will not register event handlers yet")
		return nil, true, 100 * time.Millisecond
	}

	registrations := m.eventHandlerRegistrations[ftcName]
	lastAppliedFTCs := m.lastAppliedFTCsCache[ftcName]

	for _, generator := range m.eventHandlerGenerators {
		lastApplied := lastAppliedFTCs[generator]
		if !generator.Predicate(lastApplied, ftc) {
			lastAppliedFTCs[generator] = ftc
			continue
		}

		if oldRegistration := registrations[generator]; oldRegistration != nil {
			if err := informer.Informer().RemoveEventHandler(oldRegistration); err != nil {
				return fmt.Errorf("failed to unregister event handler: %w", err), true, 0
			}
			delete(registrations, generator)
		}
		delete(lastAppliedFTCs, generator)

		if handler := generator.Generator(ftc); handler != nil {
			newRegistration, err := informer.Informer().AddEventHandler(handler)
			if err != nil {
				return fmt.Errorf("failed to register event handler: %w", err), true, 0
			}
			registrations[generator] = newRegistration
		}

		lastAppliedFTCs[generator] = ftc
	}

	return nil, false, 0
}

func (m *informerManager) processFTCDeletion(ctx context.Context, ftcName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if gvk, exists := m.gvkMapping.LookupByT1(ftcName); exists {
		ctx, _ = logging.InjectLoggerValues(ctx, "gvk", gvk.String())
	}

	return m.processFTCDeletionUnlocked(ctx, ftcName)
}

func (m *informerManager) processFTCDeletionUnlocked(ctx context.Context, ftcName string) error {
	if cancel, ok := m.informerCancelFuncs[ftcName]; ok {
		klog.FromContext(ctx).V(2).Info("Stopping informer for FederatedTypeConfig")
		cancel()
	}

	m.gvkMapping.DeleteT1(ftcName)

	delete(m.lastObservedFTCs, ftcName)
	delete(m.informers, ftcName)
	delete(m.informerCancelFuncs, ftcName)
	delete(m.eventHandlerRegistrations, ftcName)

	return nil
}

func (m *informerManager) AddEventHandlerGenerator(generator *EventHandlerGenerator) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		return fmt.Errorf("failed to add EventHandlerGenerator: InformerManager is already started")
	}

	m.eventHandlerGenerators = append(m.eventHandlerGenerators, generator)
	return nil
}

func (m *informerManager) GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister {
	return m.ftcInformer.Lister()
}

func (m *informerManager) GetResourceLister(
	gvk schema.GroupVersionKind,
) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	ftc, ok := m.gvkMapping.LookupByT2(gvk)
	if !ok {
		return nil, nil, false
	}

	informer, ok := m.informers[ftc]
	if !ok {
		return nil, nil, false
	}

	return informer.Lister(), informer.Informer().HasSynced, true
}

func (m *informerManager) GetResourceFTC(gvk schema.GroupVersionKind) (*fedcorev1a1.FederatedTypeConfig, bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	ftcName, ok := m.gvkMapping.LookupByT2(gvk)
	if !ok {
		return nil, false
	}

	ftc := m.lastObservedFTCs[ftcName]
	if ftc == nil {
		return nil, false
	}

	return ftc, true
}

func (m *informerManager) HasSynced() bool {
	return m.ftcInformer.Informer().HasSynced()
}

func (m *informerManager) Start(ctx context.Context) {
	m.lock.Lock()
	defer m.lock.Unlock()

	ctx, logger := logging.InjectLoggerName(ctx, "informer-manager")

	if m.started {
		logger.Error(nil, "InformerManager cannot be started more than once")
		return
	}

	logger.V(2).Info("Starting InformerManager")

	m.started = true

	if !cache.WaitForCacheSync(ctx.Done(), m.HasSynced) {
		logger.Error(nil, "Failed to wait for InformerManager cache sync")
		return
	}

	go wait.UntilWithContext(ctx, m.worker, 0)

	go func() {
		<-ctx.Done()

		m.lock.Lock()
		defer m.lock.Unlock()

		logger.V(2).Info("Stopping InformerManager")
		m.queue.ShutDown()
		m.shutdown = true
	}()
}

func (m *informerManager) IsShutdown() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.shutdown
}

var _ InformerManager = &informerManager{}
