package informermanager

import (
	"bytes"
	"context"
	"encoding/gob"
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
	connectionMap               map[string][]byte
	informerManagers            map[string]InformerManager
	informerManagersCancelFuncs map[string]context.CancelFunc

	queue  workqueue.RateLimitingInterface
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
		connectionMap:               map[string][]byte{},
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

	logger := m.logger.WithValues("key", key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		logger.Error(err, "Failed to process FederatedCluster")
		return
	}

	cluster, err := m.clusterInformer.Lister().Get(name)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get FederatedCluster from lister, will retry")
		m.queue.AddRateLimited(key)
		return
	}
	if apierrors.IsNotFound(err) || !util.IsClusterJoined(&cluster.Status) {
		if err := m.processClusterDeletion(name); err != nil {
			logger.Error(err, "Failed to process FederatedCluster, will retry")
			m.queue.AddRateLimited(key)
			return
		}
		return
	}

	err, needReenqueue := m.processCluster(cluster)
	if err != nil {
		if needReenqueue {
			logger.Error(err, "Failed to process FederatedCluster, will retry")
		} else {
			logger.Error(err, "Failed to process FederatedCluster")
		}
	}
	if needReenqueue {
		m.queue.AddRateLimited(key)
	}
}

func (m *federatedInformerManager) processCluster(cluster *fedcorev1a1.FederatedCluster) (err error, needReenqueue bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	clusterName := cluster.Name

	connectionHash, err := m.clientGetter.ConnectionHash(cluster)
	if err != nil {
		return fmt.Errorf("failed to get connection hash for cluster %s: %w", clusterName, err), true
	}
	if oldConnectionHash, exists := m.connectionMap[clusterName]; exists {
		if !bytes.Equal(oldConnectionHash, connectionHash) {
			// This might occur if a cluster was deleted and recreated with different connection details within a short
			// period of time and we missed processing the deletion. We simply process the cluster deletion and
			// reenqueue.
			// Note: updating of cluster connetion details, however, is still not a supported use case.
			err := m.processClusterDeletionUnlocked(clusterName)
			return err, true
		}
	} else {
		clusterClient, err := m.clientGetter.ClientGetter(cluster)
		if err != nil {
			return fmt.Errorf("failed to get client for cluster %s: %w", clusterName, err), true
		}

		manager := NewInformerManager(clusterClient, m.ftcInformer)
		ctx, cancel := context.WithCancel(context.Background())

		for _, generator := range m.eventHandlerGenerators {
			if err := manager.AddEventHandlerGenerator(generator); err != nil {
				cancel()
				return fmt.Errorf("failed to initialized InformerManager for cluster %s: %w", clusterName, err), true
			}
		}

		manager.Start(ctx)

		m.connectionMap[clusterName] = connectionHash
		m.clients[clusterName] = clusterClient
		m.informerManagers[clusterName] = manager
		m.informerManagersCancelFuncs[clusterName] = cancel
	}

	return nil, false
}

func (m *federatedInformerManager) processClusterDeletion(clusterName string) error {
	m.lock.Lock()
	m.lock.Unlock()
	return m.processClusterDeletionUnlocked(clusterName)
}

func (m *federatedInformerManager) processClusterDeletionUnlocked(clusterName string) error {
	delete(m.connectionMap, clusterName)
	delete(m.clients, clusterName)

	if cancel, ok := m.informerManagersCancelFuncs[clusterName]; ok {
		cancel()
	}
	delete(m.informerManagers, clusterName)
	delete(m.informerManagersCancelFuncs, clusterName)

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
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		m.logger.Error(nil, "FederatedInformerManager cannot be started more than once")
		return
	}

	m.started = true

	if !cache.WaitForNamedCacheSync("federated-informer-manager", ctx.Done(), m.HasSynced) {
		return
	}

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

func DefaultClusterConnectionHash(cluster *fedcorev1a1.FederatedCluster) ([]byte, error) {
	hashObj := struct {
		ApiEndpoint            string
		SecretName             string
		UseServiceAccountToken bool
	}{
		ApiEndpoint:            cluster.Spec.APIEndpoint,
		SecretName:             cluster.Spec.SecretRef.Name,
		UseServiceAccountToken: cluster.Spec.UseServiceAccountToken,
	}

	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(hashObj); err != nil {
		return nil, err
	}
    return b.Bytes(), nil
}
