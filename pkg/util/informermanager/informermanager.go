package informermanager

import (
	"context"
	"fmt"
	"sync"

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
	"github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/util/tools"
)

type informerManager struct {
	lock sync.RWMutex

	started bool

	client      dynamic.Interface
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer

	eventHandlerGenerators []*EventHandlerGenerator

	gvrMapping *tools.BijectionMap[string, schema.GroupVersionResource]

	informers                 map[string]informers.GenericInformer
	informerStopChs           map[string]chan struct{}
	eventHandlerRegistrations map[string]map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration
	lastAppliedFTCsCache      map[string]map[*EventHandlerGenerator]*fedcorev1a1.FederatedTypeConfig

	queue  workqueue.Interface
	logger klog.Logger
}

func NewInformerManager(client dynamic.Interface, ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer) InformerManager {
	manager := &informerManager{
		lock:                      sync.RWMutex{},
		started:                   false,
		client:                    client,
		ftcInformer:               ftcInformer,
		eventHandlerGenerators:    []*EventHandlerGenerator{},
		gvrMapping:                tools.NewBijectionMap[string, schema.GroupVersionResource](),
		informers:                 map[string]informers.GenericInformer{},
		informerStopChs:           map[string]chan struct{}{},
		eventHandlerRegistrations: map[string]map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration{},
		lastAppliedFTCsCache:      map[string]map[*EventHandlerGenerator]*fedcorev1a1.FederatedTypeConfig{},
		queue:                     workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
		logger:                    klog.LoggerWithName(klog.Background(), "informer-manager"),
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
		m.logger.Error(err, "Failed to enqueue FederatedTypeConfig")
		return
	}
	m.queue.Add(key)
}

func (m *informerManager) worker() {
	key, shutdown := m.queue.Get()
	if shutdown {
		return
	}
	defer m.queue.Done(key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		m.logger.Error(err, "Failed to process FederatedTypeConfig")
		return
	}

	ftc, err := m.ftcInformer.Lister().Get(name)
	if apierrors.IsNotFound(err) {
		if err := m.processFTCDeletion(name); err != nil {
			m.logger.Error(err, "Failed to process FederatedTypeConfig, will retry")
			m.queue.Add(key)
			return
		}
		return
	}
	if err != nil {
		m.logger.Error(err, "Failed to process FederatedTypeConfig, will retry")
		m.queue.Add(key)
		return
	}

	err, needReenqueue := m.processFTC(ftc)
	if err != nil {
		if needReenqueue {
			m.logger.Error(err, "Failed to process FederatedTypeConfig, will retry")
		} else {
			m.logger.Error(err, "Failed to process FederatedTypeConfig")
		}
	}
	if needReenqueue {
		m.queue.Add(key)
	}
}

func (m *informerManager) processFTC(ftc *fedcorev1a1.FederatedTypeConfig) (err error, needReenqueue bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	ftcName := ftc.Name
	apiResource := ftc.GetSourceType()
	gvr := schemautil.APIResourceToGVR(&apiResource)

	var informer informers.GenericInformer

	if oldGVR, exists := m.gvrMapping.Lookup(ftcName); exists {
		if oldGVR != gvr {
			// This might occur if a ftc was deleted and recreated with a different source type within a short period of
			// time and we missed processing the deletion. We simply process the ftc deletion and reenqueue. Note:
			// updating of ftc source types, however, is still not a supported use case.
			err := m.processFTCDeletionUnlocked(ftcName)
			return err, true
		}

		informer = m.informers[ftcName]
	} else {
		if err := m.gvrMapping.Add(ftcName, gvr); err != nil {
			// There must be another ftc with the same source type GVR.
			return fmt.Errorf("source type is already referenced by another FederatedTypeConfig: %w", err), false
		}

		informer = dynamicinformer.NewFilteredDynamicInformer(
			m.client,
			gvr,
			metav1.NamespaceAll,
			0,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			nil,
		)
		stopCh := make(chan struct{})
		go informer.Informer().Run(stopCh)

		m.informers[ftcName] = informer
		m.informerStopChs[ftcName] = stopCh
		m.eventHandlerRegistrations[ftcName] = map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration{}
		m.lastAppliedFTCsCache[ftcName] = map[*EventHandlerGenerator]*fedcorev1a1.FederatedTypeConfig{}
	}

	registrations := m.eventHandlerRegistrations[ftcName]
	lastAppliedFTCs := m.lastAppliedFTCsCache[ftcName]

	ftc = ftc.DeepCopy()

	for _, generator := range m.eventHandlerGenerators {
		lastApplied := lastAppliedFTCs[generator]
		if !generator.Predicate(lastApplied, ftc) {
			lastAppliedFTCs[generator] = ftc
			continue
		}

		if oldRegistration := registrations[generator]; oldRegistration != nil {
			if err := informer.Informer().RemoveEventHandler(oldRegistration); err != nil {
				return fmt.Errorf("failed to unregister event handler: %w", err), true
			}
			delete(registrations, generator)
		}
		delete(lastAppliedFTCs, generator)

		if handler := generator.Generator(ftc); handler != nil {
			newRegistration, err := informer.Informer().AddEventHandler(handler)
			if err != nil {
				return fmt.Errorf("failed to register event handler: %w", err), true
			}
			registrations[generator] = newRegistration
		}

		lastAppliedFTCs[generator] = ftc
	}

	return nil, false
}

func (m *informerManager) processFTCDeletion(ftcName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.processFTCDeletionUnlocked(ftcName)
}

func (m *informerManager) processFTCDeletionUnlocked(ftcName string) error {
	if stopCh, ok := m.informerStopChs[ftcName]; ok {
		close(stopCh)
	}

	m.gvrMapping.Delete(ftcName)

	delete(m.informers, ftcName)
	delete(m.informerStopChs, ftcName)
	delete(m.eventHandlerRegistrations, ftcName)

	return nil
}

func (m *informerManager) AddEventHandlerGenerator(generator *EventHandlerGenerator) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		return fmt.Errorf("InformerManager is already started.")
	}

	m.eventHandlerGenerators = append(m.eventHandlerGenerators, generator)
	return nil
}

func (m *informerManager) GetFederatedTypeConfigLister() v1alpha1.FederatedTypeConfigLister {
	return m.ftcInformer.Lister()
}

func (m *informerManager) GetResourceLister(
	gvr schema.GroupVersionResource,
) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	ftc, ok := m.gvrMapping.ReverseLookup(gvr)
	if !ok {
		return nil, nil, false
	}

	informer, ok := m.informers[ftc]
	if !ok {
		return nil, nil, false
	}

	return informer.Lister(), informer.Informer().HasSynced, true
}

func (m *informerManager) HasSynced() bool {
	return m.ftcInformer.Informer().HasSynced()
}

func (m *informerManager) Start(ctx context.Context) {
	if !cache.WaitForNamedCacheSync("informer-manager", ctx.Done(), m.HasSynced) {
		return
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		m.logger.Error(nil, "InformerManager cannot be started more than once")
		return
	}

	m.started = true

	go wait.Until(m.worker, 0, ctx.Done())
	go func() {
		<-ctx.Done()
		m.queue.ShutDown()

		m.lock.Lock()
		defer m.lock.Unlock()
		for _, stopCh := range m.informerStopChs {
			close(stopCh)
		}
	}()
}

var _ InformerManager = &informerManager{}
