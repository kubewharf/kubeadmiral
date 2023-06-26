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
