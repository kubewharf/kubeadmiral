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
	m.Lock()
	defer m.Unlock()

	if m.stopped {
		return fmt.Errorf("informer manager is shut down")
	}

	if _, ok := m.informers[gvr]; !ok {
		m.informers[gvr] = dynamicinformer.NewFilteredDynamicInformer(
			m.client,
			gvr,
			metav1.NamespaceAll,
			0,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			nil,
		)
		m.stopChs[gvr] = make(chan struct{})
		m.references[gvr] = 1

		m.informers[gvr].Informer().Run(m.stopChs[gvr])
	} else {
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
		if m.references[gvr] == 0 {
			close(m.stopChs[gvr])
			delete(m.informers, gvr)
			delete(m.stopChs, gvr)
			delete(m.references, gvr)
		}
	}()

	return nil
}

func (m *singleClusterInformerManager) ForResourceWithEventHandler(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	eventHandler cache.ResourceEventHandler,
) error {
	m.Lock()
	defer m.Unlock()

	if m.stopped {
		return fmt.Errorf("informer manager is shut down")
	}

	var registration cache.ResourceEventHandlerRegistration
	var err error

	if informer, ok := m.informers[gvr]; !ok {
		informer := dynamicinformer.NewFilteredDynamicInformer(
			m.client,
			gvr,
			metav1.NamespaceAll,
			0,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			nil,
		)
		if registration, err = informer.Informer().AddEventHandler(eventHandler); err != nil {
			return err
		}

		// Only start and cache if event handler added successfully to make method atomic
		m.informers[gvr] = informer
		m.stopChs[gvr] = make(chan struct{})
		m.references[gvr] = 1

		m.informers[gvr].Informer().Run(m.stopChs[gvr])
	} else {
		if registration, err = informer.Informer().AddEventHandler(eventHandler); err != nil {
			return err
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
		m.informers[gvr].Informer().RemoveEventHandler(registration)

		if m.references[gvr] == 0 {
			close(m.stopChs[gvr])
			delete(m.informers, gvr)
			delete(m.stopChs, gvr)
			delete(m.references, gvr)
		}
	}()

	return nil
}

func (m *singleClusterInformerManager) GetLister(
	gvr schema.GroupVersionResource,
) (cache.GenericLister, cache.InformerSynced) {
	m.Lock()
	defer m.Unlock()

	informer, ok := m.informers[gvr]
	if !ok {
		return nil, nil
	}

	return informer.Lister(), informer.Informer().HasSynced
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

var _ SingleClusterInformerManager = &singleClusterInformerManager{}
