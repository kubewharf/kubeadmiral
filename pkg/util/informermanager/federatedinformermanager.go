package informermanager

import (
	"context"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

type federatedInformerManager struct {
	lock sync.RWMutex

	started bool

	clientGetter    ClusterClientGetter
	ftcInformer     fedcorev1a1informers.FederatedTypeConfigInformer
	clusterInformer fedcorev1a1informers.FederatedClusterInformer

	eventHandlerGenerators []*EventHandlerGenerator
	clusterEventHandler    []*ClusterEventHandler

	clients                     map[string]dynamic.Interface
	informerManagers            map[string]InformerManager
	informerManagersCancelFuncs map[string]context.CancelFunc

	queue  workqueue.Interface
	logger klog.Logger
}

func NewFederatedInformerManager(
	clientGetter ClusterClientGetter,
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer,
	clusterInformer fedcorev1a1informers.FederatedClusterInformer,
) FederatedInformerManager {
	manager := &federatedInformerManager{
		lock:                        sync.RWMutex{},
		started:                     false,
		clientGetter:                clientGetter,
		ftcInformer:                 ftcInformer,
		clusterInformer:             clusterInformer,
		eventHandlerGenerators:      []*EventHandlerGenerator{},
		clusterEventHandler:         []*ClusterEventHandler{},
		clients:                     map[string]dynamic.Interface{},
		informerManagers:            map[string]InformerManager{},
		informerManagersCancelFuncs: map[string]context.CancelFunc{},
		queue:                       workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
		logger:                      klog.LoggerWithName(klog.Background(), "federated-informer-manager"),
	}

	clusterInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			cluster := obj.(*fedcorev1a1.FederatedCluster)
			return util.IsClusterJoined(&cluster.Status)
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { manager.enqueue(obj) },
			UpdateFunc: func(_ interface{}, obj interface{}) { manager.enqueue(obj) },
			DeleteFunc: func(obj interface{}) { manager.enqueue(obj) },
		},
	})

	return manager
}

func (m *federatedInformerManager) enqueue(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		m.logger.Error(err, "Failed to enqueue FederatedCluster")
		return
	}
	m.queue.Add(key)
}

func (m *federatedInformerManager) worker() {
	key, shutdown := m.queue.Get()
	if shutdown {
		return
	}
	defer m.queue.Done(key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		m.logger.Error(err, "Failed to process FederatedCluster")
		return
	}

	cluster, err := m.clusterInformer.Lister().Get(name)
	if err != nil && !apierrors.IsNotFound(err) {
		m.logger.Error(err, "Failed to process FederatedCluster, will retry")
		m.queue.Add(key)
		return
	}
	if apierrors.IsNotFound(err) || !util.IsClusterJoined(&cluster.Status) {
		if err := m.processClusterUnjoin(name); err != nil {
			m.logger.Error(err, "Failed to process FederatedCluster, will retry")
			m.queue.Add(key)
			return
		}
		return
	}

	if err := m.processCluster(cluster); err != nil {
		m.logger.Error(err, "Failed to process FederatedCluster, will retry")
		m.queue.Add(key)
	}
}

func (m *federatedInformerManager) processCluster(cluster *fedcorev1a1.FederatedCluster) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	clusterName := cluster.Name

	clusterClient, ok := m.clients[clusterName]
	if !ok {
		var err error
		if clusterClient, err = m.clientGetter(cluster); err != nil {
			return fmt.Errorf("failed to get client for cluster %s: %s", clusterName, err)
		}
		m.clients[clusterName] = clusterClient
	}

	manager, ok := m.informerManagers[clusterName]
	if !ok {
		manager = NewInformerManager(clusterClient, m.ftcInformer)
		ctx, cancel := context.WithCancel(context.Background())

		for _, generator := range m.eventHandlerGenerators {
			if err := manager.AddEventHandlerGenerator(generator); err != nil {
				return fmt.Errorf("failed to initialized InformerManager for cluster %s: %w", clusterName, err)
			}
		}

		manager.Start(ctx)
		m.informerManagers[clusterName] = manager
		m.informerManagersCancelFuncs[clusterName] = cancel
	}

	return nil
}

func (m *federatedInformerManager) processClusterUnjoin(clusterName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	delete(m.clients, clusterName)

	if cancel, ok := m.informerManagersCancelFuncs[clusterName]; ok {
		cancel()
		delete(m.informerManagers, clusterName)
		delete(m.informerManagersCancelFuncs, clusterName)
	}

	return nil
}

func (m *federatedInformerManager) AddClusterEventHandler(handler *ClusterEventHandler) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		return fmt.Errorf("FederatedInformerManager is already started.")
	}

	m.clusterEventHandler = append(m.clusterEventHandler, handler)
	return nil

}

func (m *federatedInformerManager) AddEventHandlerGenerator(generator *EventHandlerGenerator) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		return fmt.Errorf("FederatedInformerManager is already started.")
	}

	m.eventHandlerGenerators = append(m.eventHandlerGenerators, generator)
	return nil
}

func (m *federatedInformerManager) GetClusterClient(cluster string) (client dynamic.Interface, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	client, ok := m.clients[cluster]
	return client, ok
}

func (m *federatedInformerManager) GetFederatedClusterLister() fedcorev1a1listers.FederatedClusterLister {
	return m.clusterInformer.Lister()
}

func (m *federatedInformerManager) GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister {
	return m.ftcInformer.Lister()
}

func (m *federatedInformerManager) GetResourceLister(
	gvr schema.GroupVersionResource,
	cluster string,
) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	manager, ok := m.informerManagers[cluster]
	if !ok {
		return nil, nil, false
	}

	return manager.GetResourceLister(gvr)
}

func (m *federatedInformerManager) HasSynced() bool {
	return m.ftcInformer.Informer().HasSynced() && m.clusterInformer.Informer().HasSynced()
}

func (m *federatedInformerManager) Start(ctx context.Context) {
	if !cache.WaitForNamedCacheSync("federated-informer-manager", ctx.Done(), m.HasSynced) {
		return
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		m.logger.Error(nil, "FederatedInformerManager cannot be started more than once")
		return
	}

	m.started = true

	for _, handler := range m.clusterEventHandler {
		predicate := handler.Predicate
		callback := handler.Callback

		m.clusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				cluster := obj.(*fedcorev1a1.FederatedCluster)
				if predicate(nil, cluster) {
					callback(cluster)
				}
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				oldCluster := oldObj.(*fedcorev1a1.FederatedCluster)
				newCluster := newObj.(*fedcorev1a1.FederatedCluster)
				if predicate(oldCluster, newCluster) {
					callback(newCluster)
				}
			},
			DeleteFunc: func(obj interface{}) {
				cluster := obj.(*fedcorev1a1.FederatedCluster)
				if predicate(cluster, nil) {
					callback(cluster)
				}
			},
		})
	}

	go wait.Until(m.worker, 0, ctx.Done())
	go func() {
		<-ctx.Done()
		m.queue.ShutDown()

		m.lock.Lock()
		defer m.lock.Unlock()
		for _, cancelFunc := range m.informerManagersCancelFuncs {
			cancelFunc()
		}
	}()
}

var _ FederatedInformerManager = &federatedInformerManager{}
