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
	"sync"

	"k8s.io/client-go/dynamic"
)

type multiClusterInformerManager struct {
	sync.Mutex

	managers   map[string]SingleClusterInformerManager
	references map[string]int64
}

func NewMultiClusterInformerManager() MultiClusterInformerManager {
	return &multiClusterInformerManager{
		Mutex:      sync.Mutex{},
		managers:   map[string]SingleClusterInformerManager{},
		references: map[string]int64{},
	}
}

func (m *multiClusterInformerManager) ForCluster(
	ctx context.Context,
	cluster string,
	client dynamic.Interface,
) error {
	m.Lock()
	defer m.Unlock()

	if _, ok := m.managers[cluster]; !ok {
		m.managers[cluster] = NewSingleClusterInformerManager(client)
		m.references[cluster] = 0
	}

	m.references[cluster]++

	go func() {
		m.Lock()
		defer m.Unlock()

		m.references[cluster]--
		if m.references[cluster] == 0 {
			m.managers[cluster].Shutdown()
			delete(m.managers, cluster)
			delete(m.references, cluster)
		}
	}()

	return nil
}

func (m *multiClusterInformerManager) GetManager(cluster string) SingleClusterInformerManager {
	m.Lock()
	defer m.Unlock()

	manager, ok := m.managers[cluster]
	if !ok {
		return nil
	}

	return manager
}

var _ MultiClusterInformerManager = &multiClusterInformerManager{}
