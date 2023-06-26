package informermanager

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type singleClusterInformerManager struct {
	sync.Mutex
	stopped bool

	client dynamic.Interface

	informers  map[schema.GroupVersionResource]informers.GenericInformer
	stopChs    map[schema.GroupVersionResource]chan struct{}
	references map[schema.GroupVersionResource]int64
}

func NewSingleClusterInformerManager(client dynamic.Interface) SingleClusterInformerManager {
	return &singleClusterInformerManager{
		Mutex:      sync.Mutex{},
		stopped:    false,
		client:     client,
		informers:  map[schema.GroupVersionResource]informers.GenericInformer{},
		stopChs:    map[schema.GroupVersionResource]chan struct{}{},
		references: map[schema.GroupVersionResource]int64{},
	}
}

func (m *singleClusterInformerManager) ForResource(ctx context.Context, gvr schema.GroupVersionResource) error {
	_, _, err := m.addInformerReference(ctx, gvr, nil)
	return err
}

func (m *singleClusterInformerManager) ForResourceWithEventHandler(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	eventHandler cache.ResourceEventHandler,
) error {
	_, _, err := m.addInformerReference(ctx, gvr, eventHandler)
	return err
}

func (m *singleClusterInformerManager) ListerForResource(
	ctx context.Context,
	gvr schema.GroupVersionResource,
) (cache.GenericLister, cache.InformerSynced, error) {
	return m.addInformerReference(ctx, gvr, nil)
}

func (m *singleClusterInformerManager) Shutdown() {
	m.Lock()
	defer m.Unlock()

	m.stopped = true
	for gvr, stopCh := range m.stopChs {
		close(stopCh)
		delete(m.informers, gvr)
		delete(m.stopChs, gvr)
		delete(m.references, gvr)
	}
}

func (m *singleClusterInformerManager) addInformerReference(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	eventHandler cache.ResourceEventHandler,
) (cache.GenericLister, cache.InformerSynced, error) {
	m.Lock()
	defer m.Unlock()

	if m.stopped {
		return nil, nil, fmt.Errorf("informer manager is shut down")
	}

	var registration cache.ResourceEventHandlerRegistration
	var err error

	if _, ok := m.informers[gvr]; !ok {
		informer := dynamicinformer.NewFilteredDynamicInformer(
			m.client,
			gvr,
			metav1.NamespaceAll,
			0,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			nil,
		)
		if eventHandler != nil {
			if registration, err = m.informers[gvr].Informer().AddEventHandler(eventHandler); err != nil {
				return nil, nil, err
			}
		}

		m.informers[gvr] = informer
		m.stopChs[gvr] = make(chan struct{})
		m.references[gvr] = 1

		m.informers[gvr].Informer().Run(m.stopChs[gvr])
	} else {
		if eventHandler != nil {
			if registration, err = m.informers[gvr].Informer().AddEventHandler(eventHandler); err != nil {
				return nil, nil, err
			}
		}

		m.references[gvr]++
	}

	go func() {
		<-ctx.Done()

		m.Lock()
		defer m.Unlock()

		if m.stopped {
			return
		}

		m.references[gvr]--
		if registration != nil {
			m.informers[gvr].Informer().RemoveEventHandler(registration)
		}

		if m.references[gvr] == 0 {
			close(m.stopChs[gvr])
			delete(m.informers, gvr)
			delete(m.stopChs, gvr)
			delete(m.references, gvr)
		}
	}()

	return m.informers[gvr].Lister(), m.informers[gvr].Informer().HasSynced, nil
}

var _ SingleClusterInformerManager = &singleClusterInformerManager{}
