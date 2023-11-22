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
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
)

type federatedInformerManager struct {
	lock sync.RWMutex

	started                         bool
	shutdown                        bool
	clusterEventHandlerRegistration cache.ResourceEventHandlerRegistration

	clientHelper          ClusterClientHelper
	kubeClientGetter      func(*fedcorev1a1.FederatedCluster, *rest.Config) (kubernetes.Interface, error)
	dynamicClientGetter   func(*fedcorev1a1.FederatedCluster, *rest.Config) (dynamic.Interface, error)
	discoveryClientGetter func(*fedcorev1a1.FederatedCluster, *rest.Config) (discovery.DiscoveryInterface, error)

	ftcInformer     fedcorev1a1informers.FederatedTypeConfigInformer
	clusterInformer fedcorev1a1informers.FederatedClusterInformer

	eventHandlerGenerators []*EventHandlerGenerator
	clusterEventHandlers   []*ClusterEventHandler

	kubeClients        map[string]kubernetes.Interface
	dynamicClients     map[string]dynamic.Interface
	discoveryClients   map[string]discovery.DiscoveryInterface
	restConfigs        map[string]*rest.Config
	connectionMap      map[string][]byte
	clusterCancelFuncs map[string]context.CancelFunc
	informerManagers   map[string]InformerManager
	informerFactories  map[string]informers.SharedInformerFactory

	queue              workqueue.RateLimitingInterface
	podListerSemaphore *semaphore.Weighted
	initialClusters    sets.Set[string]

	podEventHandlers      []*ResourceEventHandlerWithClusterFuncs
	podEventRegistrations map[string]map[*ResourceEventHandlerWithClusterFuncs]cache.ResourceEventHandlerRegistration
}

func NewFederatedInformerManager(
	clientHelper ClusterClientHelper,
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer,
	clusterInformer fedcorev1a1informers.FederatedClusterInformer,
) FederatedInformerManager {
	manager := &federatedInformerManager{
		lock:                   sync.RWMutex{},
		started:                false,
		shutdown:               false,
		clientHelper:           clientHelper,
		ftcInformer:            ftcInformer,
		clusterInformer:        clusterInformer,
		eventHandlerGenerators: []*EventHandlerGenerator{},
		clusterEventHandlers:   []*ClusterEventHandler{},
		kubeClients:            map[string]kubernetes.Interface{},
		dynamicClients:         map[string]dynamic.Interface{},
		discoveryClients:       map[string]discovery.DiscoveryInterface{},
		restConfigs:            map[string]*rest.Config{},
		connectionMap:          map[string][]byte{},
		clusterCancelFuncs:     map[string]context.CancelFunc{},
		informerManagers:       map[string]InformerManager{},
		informerFactories:      map[string]informers.SharedInformerFactory{},
		queue: workqueue.NewRateLimitingQueue(
			workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 10*time.Second),
		),
		podListerSemaphore:    semaphore.NewWeighted(3), // TODO: make this configurable
		initialClusters:       sets.New[string](),
		podEventHandlers:      []*ResourceEventHandlerWithClusterFuncs{},
		podEventRegistrations: map[string]map[*ResourceEventHandlerWithClusterFuncs]cache.ResourceEventHandlerRegistration{},
	}

	var err error
	if manager.clusterEventHandlerRegistration, err = clusterInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			cluster := obj.(*fedcorev1a1.FederatedCluster)
			return clusterutil.IsClusterJoined(&cluster.Status)
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { manager.enqueue(obj) },
			UpdateFunc: func(_ interface{}, obj interface{}) { manager.enqueue(obj) },
			DeleteFunc: func(obj interface{}) { manager.enqueue(obj) },
		},
	}); err != nil {
		klog.Error(err, "Failed to register event handler for FederatedCluster")
	}

	ftcInformer.Informer()

	manager.dynamicClientGetter = func(_ *fedcorev1a1.FederatedCluster, config *rest.Config) (dynamic.Interface, error) {
		return dynamic.NewForConfig(config)
	}
	manager.kubeClientGetter = func(_ *fedcorev1a1.FederatedCluster, config *rest.Config) (kubernetes.Interface, error) {
		return kubernetes.NewForConfig(config)
	}
	manager.discoveryClientGetter = func(_ *fedcorev1a1.FederatedCluster, config *rest.Config) (discovery.DiscoveryInterface, error) {
		return discovery.NewDiscoveryClientForConfig(config)
	}

	return manager
}

func (m *federatedInformerManager) enqueue(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.Error(err, "federated-informer-manager: Failed to enqueue FederatedCluster")
		return
	}
	m.queue.Add(key)
}

func (m *federatedInformerManager) worker(ctx context.Context) {
	key, shutdown := m.queue.Get()
	if shutdown {
		return
	}
	defer m.queue.Done(key)

	logger := klog.FromContext(ctx)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		logger.Error(err, "Failed to process FederatedCluster")
		return
	}

	ctx, logger = logging.InjectLoggerValues(ctx, "cluster", name)

	cluster, err := m.clusterInformer.Lister().Get(name)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get FederatedCluster from lister, will retry")
		m.queue.AddRateLimited(key)
		return
	}
	if apierrors.IsNotFound(err) || !clusterutil.IsClusterJoined(&cluster.Status) {
		if err := m.processClusterDeletion(ctx, name); err != nil {
			logger.Error(err, "Failed to process FederatedCluster, will retry")
			m.queue.AddRateLimited(key)
		} else {
			m.queue.Forget(key)
		}
		return
	}

	needReenqueue, err := m.processCluster(ctx, cluster)
	if err != nil {
		if needReenqueue {
			logger.Error(err, "Failed to process FederatedCluster, will retry")
			m.queue.AddRateLimited(key)
		} else {
			logger.Error(err, "Failed to process FederatedCluster")
			m.queue.Forget(key)
		}
		return
	}

	if needReenqueue {
		m.queue.AddRateLimited(key)
	} else {
		m.queue.Forget(key)
	}
}

func (m *federatedInformerManager) processCluster(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
) (needReenqueue bool, err error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	clusterName := cluster.Name

	connectionHash, err := m.clientHelper.ConnectionHash(cluster)
	if err != nil {
		return true, fmt.Errorf("failed to get connection hash for cluster %s: %w", clusterName, err)
	}
	if oldConnectionHash, exists := m.connectionMap[clusterName]; exists {
		if !bytes.Equal(oldConnectionHash, connectionHash) {
			// This might occur if a cluster was deleted and recreated with different connection details within a short
			// period of time and we missed processing the deletion. We simply process the cluster deletion and
			// reenqueue.
			// Note: updating of cluster connection details, however, is still not a supported use case.
			err := m.processClusterDeletionUnlocked(ctx, clusterName)
			return true, err
		}
	} else {
		clusterRestConfig, err := m.clientHelper.RestConfigGetter(cluster)
		if err != nil {
			return true, fmt.Errorf("failed to get rest config for cluster %s: %w", clusterName, err)
		}

		clusterDynamicClient, err := m.dynamicClientGetter(cluster, clusterRestConfig)
		if err != nil {
			return true, fmt.Errorf("failed to get dynamic client for cluster %s: %w", clusterName, err)
		}

		clusterKubeClient, err := m.kubeClientGetter(cluster, clusterRestConfig)
		if err != nil {
			return true, fmt.Errorf("failed to get kubernetes client for cluster %s: %w", clusterName, err)
		}

		clusterDiscoveryClient, err := m.discoveryClientGetter(cluster, clusterRestConfig)
		if err != nil {
			return true, fmt.Errorf("failed to get discovery client for cluster %s: %w", clusterName, err)
		}

		manager := NewInformerManager(
			clusterDynamicClient,
			m.ftcInformer,
			func(opts *metav1.ListOptions) {
				selector := &metav1.LabelSelector{}
				metav1.AddLabelToSelector(
					selector,
					managedlabel.ManagedByKubeAdmiralLabelKey,
					managedlabel.ManagedByKubeAdmiralLabelValue,
				)
				opts.LabelSelector = metav1.FormatLabelSelector(selector)
			},
		)

		ctx, cancel := context.WithCancel(ctx)
		for _, generator := range m.eventHandlerGenerators {
			if err := manager.AddEventHandlerGenerator(generator); err != nil {
				cancel()
				return true, fmt.Errorf("failed to initialized InformerManager for cluster %s: %w", clusterName, err)
			}
		}

		factory := informers.NewSharedInformerFactory(clusterKubeClient, 0)
		addPodInformer(ctx, factory, clusterKubeClient, m.podListerSemaphore, false)
		factory.Core().V1().Nodes().Informer()

		klog.FromContext(ctx).V(2).Info("Starting new InformerManager for FederatedCluster")
		manager.Start(ctx)

		klog.FromContext(ctx).V(2).Info("Starting new SharedInformerFactory for FederatedCluster")
		factory.Start(ctx.Done())

		m.connectionMap[clusterName] = connectionHash
		m.kubeClients[clusterName] = clusterKubeClient
		m.dynamicClients[clusterName] = clusterDynamicClient
		m.discoveryClients[clusterName] = clusterDiscoveryClient
		m.restConfigs[clusterName] = clusterRestConfig
		m.clusterCancelFuncs[clusterName] = cancel
		m.informerManagers[clusterName] = manager
		m.informerFactories[clusterName] = factory
		m.podEventRegistrations[clusterName] = map[*ResourceEventHandlerWithClusterFuncs]cache.ResourceEventHandlerRegistration{}
	}

	if m.initialClusters.Has(cluster.Name) {
		manager := m.informerManagers[cluster.Name]
		if manager != nil && manager.HasSynced() {
			m.initialClusters.Delete(cluster.Name)
		} else {
			klog.FromContext(ctx).V(3).Info("Waiting for InformerManager sync")
			return true, nil
		}
	}

	registrations := m.podEventRegistrations[clusterName]
	factory := m.informerFactories[clusterName]
	for _, handler := range m.podEventHandlers {
		if registrations[handler] == nil {
			copied := handler.copyWithClusterName(clusterName)
			if r, err := factory.Core().V1().Pods().Informer().AddEventHandler(copied); err == nil {
				registrations[handler] = r
			}
		}
	}

	return false, nil
}

func (m *federatedInformerManager) processClusterDeletion(ctx context.Context, clusterName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.processClusterDeletionUnlocked(ctx, clusterName)
}

func (m *federatedInformerManager) processClusterDeletionUnlocked(ctx context.Context, clusterName string) error {
	delete(m.connectionMap, clusterName)
	delete(m.kubeClients, clusterName)
	delete(m.dynamicClients, clusterName)
	delete(m.discoveryClients, clusterName)
	delete(m.restConfigs, clusterName)

	if cancel, ok := m.clusterCancelFuncs[clusterName]; ok {
		klog.FromContext(ctx).V(2).Info("Stopping InformerManager and SharedInformerFactory for FederatedCluster")
		cancel()
	}
	delete(m.informerManagers, clusterName)
	delete(m.informerFactories, clusterName)
	delete(m.clusterCancelFuncs, clusterName)
	delete(m.podEventRegistrations, clusterName)

	m.initialClusters.Delete(clusterName)

	return nil
}

func (m *federatedInformerManager) AddClusterEventHandlers(handlers ...*ClusterEventHandler) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		return fmt.Errorf("failed to add ClusterEventHandler: FederatedInformerManager is already started")
	}

	m.clusterEventHandlers = append(m.clusterEventHandlers, handlers...)
	return nil
}

func (m *federatedInformerManager) AddEventHandlerGenerator(generator *EventHandlerGenerator) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.started {
		return fmt.Errorf("failed to add EventHandlerGenerator: FederatedInformerManager is already started")
	}

	m.eventHandlerGenerators = append(m.eventHandlerGenerators, generator)
	return nil
}

func (m *federatedInformerManager) GetClusterDynamicClient(cluster string) (client dynamic.Interface, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	client, ok := m.dynamicClients[cluster]
	return client, ok
}

func (m *federatedInformerManager) GetClusterKubeClient(cluster string) (client kubernetes.Interface, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	client, ok := m.kubeClients[cluster]
	return client, ok
}

func (m *federatedInformerManager) GetClusterDiscoveryClient(cluster string) (client discovery.DiscoveryInterface, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	client, ok := m.discoveryClients[cluster]
	return client, ok
}

func (m *federatedInformerManager) GetClusterRestConfig(cluster string) (config *rest.Config, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	config, ok := m.restConfigs[cluster]
	return rest.CopyConfig(config), ok
}

func (m *federatedInformerManager) GetReadyClusters() ([]*fedcorev1a1.FederatedCluster, error) {
	var clusters []*fedcorev1a1.FederatedCluster

	allClusters, err := m.GetFederatedClusterLister().List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}
	for _, cluster := range allClusters {
		if clusterutil.IsClusterReady(&cluster.Status) {
			clusters = append(clusters, cluster)
		}
	}

	return clusters, nil
}

func (m *federatedInformerManager) GetFederatedClusterLister() fedcorev1a1listers.FederatedClusterLister {
	return m.clusterInformer.Lister()
}

func (m *federatedInformerManager) GetJoinedClusters() ([]*fedcorev1a1.FederatedCluster, error) {
	var clusters []*fedcorev1a1.FederatedCluster

	allClusters, err := m.GetFederatedClusterLister().List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}
	for _, cluster := range allClusters {
		if clusterutil.IsClusterJoined(&cluster.Status) {
			clusters = append(clusters, cluster)
		}
	}

	return clusters, nil
}

func (m *federatedInformerManager) GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister {
	return m.ftcInformer.Lister()
}

func (m *federatedInformerManager) GetNodeLister(
	cluster string,
) (lister corev1listers.NodeLister, informerSynced cache.InformerSynced, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	factory, ok := m.informerFactories[cluster]
	if !ok {
		return nil, nil, false
	}

	return factory.Core().V1().Nodes().Lister(), factory.Core().V1().Nodes().Informer().HasSynced, true
}

func (m *federatedInformerManager) AddPodEventHandler(handler *ResourceEventHandlerWithClusterFuncs) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.podEventHandlers = append(m.podEventHandlers, handler)
}

func (m *federatedInformerManager) GetPodLister(
	cluster string,
) (lister corev1listers.PodLister, informerSynced cache.InformerSynced, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	factory, ok := m.informerFactories[cluster]
	if !ok {
		return nil, nil, false
	}

	return factory.Core().V1().Pods().Lister(), factory.Core().V1().Pods().Informer().HasSynced, true
}

func (m *federatedInformerManager) GetResourceLister(
	gvk schema.GroupVersionKind,
	cluster string,
) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	manager, ok := m.informerManagers[cluster]
	if !ok {
		return nil, nil, false
	}

	return manager.GetResourceLister(gvk)
}

func (m *federatedInformerManager) HasSynced() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.ftcInformer.Informer().HasSynced() && m.clusterInformer.Informer().HasSynced() &&
		len(m.initialClusters) == 0
}

func (m *federatedInformerManager) Start(ctx context.Context) {
	m.lock.Lock()
	defer m.lock.Unlock()

	ctx, logger := logging.InjectLoggerName(ctx, "federated-informer-manager")

	if m.started {
		logger.Error(nil, "FederatedInformerManager cannot be started more than once")
		return
	}

	m.started = true

	if !cache.WaitForCacheSync(ctx.Done(), m.ftcInformer.Informer().HasSynced, m.clusterInformer.Informer().HasSynced) {
		logger.Error(nil, "Failed to wait for FederatedInformerManager cache sync")
		return
	}

	// Populate the initial snapshot of clusters

	clusters := m.clusterInformer.Informer().GetStore().List()
	for _, clusterObj := range clusters {
		cluster := clusterObj.(*fedcorev1a1.FederatedCluster)
		if clusterutil.IsClusterJoined(&cluster.Status) {
			m.initialClusters.Insert(cluster.GetName())
		}
	}

	for _, handler := range m.clusterEventHandlers {
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
				if deleted, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					obj = deleted.Obj
					if obj == nil {
						return
					}
				}
				cluster := obj.(*fedcorev1a1.FederatedCluster)
				if predicate(cluster, nil) {
					callback(cluster)
				}
			},
		})
	}

	go wait.UntilWithContext(ctx, m.worker, 0)
	go func() {
		<-ctx.Done()

		m.lock.Lock()
		defer m.lock.Unlock()

		logger.V(2).Info("Stopping FederatedInformerManager")
		m.queue.ShutDown()

		// We remove the event handler for FederatedCluster to prevent goroutine leak
		if m.clusterEventHandlerRegistration != nil {
			logger.V(2).Info("Removing event handler for FederatedCluster")
			if err := m.clusterInformer.Informer().RemoveEventHandler(m.clusterEventHandlerRegistration); err != nil {
				logger.Error(err, "Failed to remove event handler for FederatedCluster")
			}
		}

		m.shutdown = true
	}()
}

func (m *federatedInformerManager) IsShutdown() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.shutdown
}

var _ FederatedInformerManager = &federatedInformerManager{}

func DefaultClusterConnectionHash(cluster *fedcorev1a1.FederatedCluster) ([]byte, error) {
	hashObj := struct {
		APIEndpoint            string
		SecretName             string
		UseServiceAccountToken bool
	}{
		APIEndpoint:            cluster.Spec.APIEndpoint,
		SecretName:             cluster.Spec.SecretRef.Name,
		UseServiceAccountToken: cluster.Spec.UseServiceAccountToken,
	}

	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(hashObj); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// GetClusterObject is a helper function to get a cluster object. GetClusterObject first attempts to get the object from
// the federated informer manager with the given key. However, if the cache for the cluster is not synced, it will send a GET
// request to the cluster's apiserver to retrieve the object directly.
func GetClusterObject(
	ctx context.Context,
	ftcManager FederatedTypeConfigManager,
	fedInformerManager FederatedInformerManager,
	clusterName string,
	qualifiedName common.QualifiedName,
	gvk schema.GroupVersionKind,
) (*unstructured.Unstructured, bool, error) {
	lister, hasSynced, exists := fedInformerManager.GetResourceLister(gvk, clusterName)
	if exists && hasSynced() {
		clusterObj, err := lister.Get(qualifiedName.String())
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, false, nil
			} else {
				return nil, false, err
			}
		}
		return clusterObj.(*unstructured.Unstructured), true, nil
	}

	client, exists := fedInformerManager.GetClusterDynamicClient(clusterName)
	if !exists {
		return nil, false, fmt.Errorf("cluster client does not exist for cluster %q", clusterName)
	}

	ftc, exists := ftcManager.GetResourceFTC(gvk)
	if !exists {
		return nil, false, fmt.Errorf("FTC does not exist for GVK %q", gvk)
	}

	clusterObj, err := client.Resource(ftc.GetSourceTypeGVR()).Namespace(qualifiedName.Namespace).Get(
		ctx, qualifiedName.Name, metav1.GetOptions{ResourceVersion: "0"},
	)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to get object %q with client: %w", qualifiedName.String(), err)
	}
	if !managedlabel.HasManagedLabel(clusterObj) {
		return nil, false, nil
	}

	return clusterObj, true, nil
}
