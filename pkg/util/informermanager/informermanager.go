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
)

type informerManager struct {
	lock sync.RWMutex

	started bool

	client      dynamic.Interface
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer

	eventHandlerGenerators []*EventHandlerGenerator

	gvrMapping map[schema.GroupVersionResource]string

	informers                 map[string]informers.GenericInformer
	informerStopChs           map[string]chan struct{}
	eventHandlerRegistrations map[string]map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration

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
		gvrMapping:                map[schema.GroupVersionResource]string{},
		informers:                 map[string]informers.GenericInformer{},
		informerStopChs:           map[string]chan struct{}{},
		eventHandlerRegistrations: map[string]map[*EventHandlerGenerator]cache.ResourceEventHandlerRegistration{},
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

	if err := m.processFTC(ftc); err != nil {
		m.logger.Error(err, "Failed to process FederatedTypeConfig, will retry")
		m.queue.Add(key)
	}
}

func (m *informerManager) processFTC(ftc *fedcorev1a1.FederatedTypeConfig) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	ftcName := ftc.Name
	apiResource := ftc.GetSourceType()
	gvr := schemautil.APIResourceToGVR(&apiResource)

	m.gvrMapping[gvr] = ftcName

	informer, ok := m.informers[ftcName]
	if !ok {
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
	}

	registrations := m.eventHandlerRegistrations[ftcName]

	for _, generator := range m.eventHandlerGenerators {
		shouldRegister := generator.Predicate(ftc)
		oldRegistration, oldRegistrationExists := registrations[generator]

		switch {
		case !shouldRegister && oldRegistrationExists:
			if err := informer.Informer().RemoveEventHandler(oldRegistration); err != nil {
				return fmt.Errorf("failed to unregister event handler: %w", err)
			}
			delete(registrations, generator)

		case shouldRegister && !oldRegistrationExists:
			handler := generator.Generator(ftc)
			newRegistration, err := informer.Informer().AddEventHandler(handler)
			if err != nil {
				return fmt.Errorf("failed to register event handler: %w", err)
			}
			registrations[generator] = newRegistration
		}
	}

	return nil
}

func (m *informerManager) processFTCDeletion(ftcName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	stopCh, ok := m.informerStopChs[ftcName]
	if !ok {
		return nil
	}

	close(stopCh)
	delete(m.informers, ftcName)
	delete(m.informerStopChs, ftcName)
	delete(m.eventHandlerRegistrations, ftcName)

	for gvr, ftc := range m.gvrMapping {
		if ftc == ftcName {
			delete(m.gvrMapping, gvr)
		}
	}

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

	ftc, ok := m.gvrMapping[gvr]
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
