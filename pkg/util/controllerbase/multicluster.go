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

package controllerbase

import (
	"context"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type MultiClusterEventHandlerGenerator func(ftc *fedcorev1a1.FederatedTypeConfig, cluster *fedcorev1a1.FederatedCluster) cache.ResourceEventHandler

// MultiClusterFTCControllerBase provides an interface for controllers that need to dynamically register event handlers
// and perform reconciliation for objects in member clusters based on FederatedTypeConfigs. MultiClusterFTCControllerBase
// will listen to FTC events and maintain informers for the source type of each FTC for all joined member clusters. It
// will also handle the registration and unregistration of generated event handlers in accordance with the lifecycle of
// each FTC.
type MultiClusterFTCControllerBase struct {
	fedSystemNamespace string
	baseClientConfig   *rest.Config
	kubeClient         kubernetes.Interface
	ftcInformer        fedcorev1a1informers.FederatedTypeConfigInformer
	clusterInformer    fedcorev1a1informers.FederatedClusterInformer

	informerManager informermanager.MultiClusterInformerManager

	lock                   *sync.Mutex
	started                bool
	eventHandlerGenerators []MultiClusterEventHandlerGenerator
	ftcContexts            map[string]context.Context
	ftcCancelFuncs         map[string]context.CancelFunc
	clusterCancelFuncs     map[string]context.CancelFunc
	clusterClients         map[string]dynamic.Interface

	ftcQueue     workqueue.Interface
	clusterQueue workqueue.Interface
	logger       klog.Logger
}

func NewMultiClusterFTCControllerBase(
	fedSystemNamespace string,
	baseConfig *rest.Config,
	kubeClient kubernetes.Interface,
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer,
	clusterInformer fedcorev1a1informers.FederatedClusterInformer,
) *MultiClusterFTCControllerBase {
	base := &MultiClusterFTCControllerBase{
		fedSystemNamespace:     fedSystemNamespace,
		baseClientConfig:       baseConfig,
		kubeClient:             kubeClient,
		ftcInformer:            ftcInformer,
		clusterInformer:        clusterInformer,
		informerManager:        informermanager.NewMultiClusterInformerManager(),
		lock:                   &sync.Mutex{},
		started:                false,
		eventHandlerGenerators: []MultiClusterEventHandlerGenerator{},
		ftcContexts:            map[string]context.Context{},
		ftcCancelFuncs:         map[string]context.CancelFunc{},
		clusterCancelFuncs:     map[string]context.CancelFunc{},
		clusterClients:         map[string]dynamic.Interface{},
		ftcQueue:               workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
		clusterQueue:           workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
		logger:                 klog.LoggerWithName(klog.Background(), "multi-cluster-ftc-controller-base"),
	}

	enqueueObj := func(o interface{}, queue workqueue.Interface) {
		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(o)
		if err != nil {
			base.logger.Error(err, "Failed to get meta namespace key")
			return
		}
		queue.Add(key)
	}

	ftcInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { enqueueObj(obj, base.ftcQueue) },
		UpdateFunc: func(_, obj interface{}) { enqueueObj(obj, base.ftcQueue) },
		DeleteFunc: func(obj interface{}) { enqueueObj(obj, base.ftcQueue) },
	})
	clusterInformer.Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				cluster := obj.(*fedcorev1a1.FederatedCluster)
				return util.IsClusterJoined(&cluster.Status)
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					enqueueObj(obj, base.clusterQueue)
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
					oldCluster := oldObj.(*fedcorev1a1.FederatedCluster)
					newCluster := newObj.(*fedcorev1a1.FederatedCluster)

					if oldCluster.GetGeneration() != newCluster.GetGeneration() {
						enqueueObj(newObj, base.clusterQueue)
					}
				},
				DeleteFunc: func(obj interface{}) {
					enqueueObj(obj, base.clusterQueue)
				},
			},
		},
	)

	return base
}

// Adds an EventHandlerGenerator that will be used to generate event handlers to be added to each FTC's source type
// informer. Event handlers generated for a FTC will also be unregistered when the FTC is deleted.
// EventHandlerGenerators cannot be added after the MultiClusterFTCControllerBase is started.
//
// NOTE: event handlers generated for a FTC may be temporarily registered more than once, so it is important to ensure
// that the generated event handlers are idempotent.
func (b *MultiClusterFTCControllerBase) AddEventHandlerGenerator(generator MultiClusterEventHandlerGenerator) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.started {
		return fmt.Errorf("controller is already started")
	}

	b.eventHandlerGenerators = append(b.eventHandlerGenerators, generator)
	return nil
}

// Returns a lister for the given GVR if it exists. The lister for a FTC's source type and the given cluster will
// eventually exist as long as the FTC and cluster exists in the view of the MutliClusterControllerBase.
func (b *MultiClusterFTCControllerBase) GetResourceLister(
	cluster string,
	gvr schema.GroupVersionResource,
) (cache.GenericLister, cache.InformerSynced) {
	manager := b.informerManager.GetManager(cluster)
	if manager == nil {
		return nil, nil
	}
	return manager.GetLister(gvr)
}

// Returns a client for the given cluster if it exists. The client will eventually exist as long as the cluster exists
// in the view of the MultiClusterFTCControllerBase.
func (b *MultiClusterFTCControllerBase) GetClusterClient(cluster string) dynamic.Interface {
	client, ok := b.clusterClients[cluster]
	if !ok {
		return nil
	}
	return client
}

// Returns the FTC lister used by the MultiClusterFTCControllerBase.
func (b *MultiClusterFTCControllerBase) GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister {
	return b.ftcInformer.Lister()
}

// Returns the federated cluster lister used by the MultiClusterFTCControllerBase.
func (b *MultiClusterFTCControllerBase) GetFederatedClusterLister() fedcorev1a1listers.FederatedClusterLister {
	return b.clusterInformer.Lister()
}

// Returns true if the FTCControllerBase's view of FederatedTypeConfigs and FederatedClusters is synced.
func (b *MultiClusterFTCControllerBase) IsSynced() bool {
	return b.ftcInformer.Informer().HasSynced() && b.clusterInformer.Informer().HasSynced()
}

// Starts processing FederatedTypeConfig and FederatedCluster events.
func (b *MultiClusterFTCControllerBase) Start(ctx context.Context) {
	if !cache.WaitForNamedCacheSync(
		"multi-cluster-ftc-controller-base",
		ctx.Done(),
		b.ftcInformer.Informer().HasSynced,
		b.clusterInformer.Informer().HasSynced,
	) {
		return
	}

	b.lock.Lock()
	defer b.lock.Unlock()
	b.started = true

	go wait.UntilWithContext(ctx, b.processNextFTC, 0)
	go wait.UntilWithContext(ctx, b.processNextCluster, 0)

	go func() {
		<-ctx.Done()
		b.ftcQueue.ShutDown()
		b.clusterQueue.ShutDown()
	}()
}

func (b *MultiClusterFTCControllerBase) processNextFTC(ctx context.Context) {
	key, quit := b.ftcQueue.Get()
	if quit {
		return
	}

	defer b.ftcQueue.Done(key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		b.logger.Error(err, "Failed to split meta namespace key")
		return
	}

	ftc, err := b.ftcInformer.Lister().Get(name)

	if apierrors.IsNotFound(err) {
		if err := b.handleFTCDeletion(name); err != nil {
			b.logger.Error(err, "Failed to handle FTC deletion")
			b.ftcQueue.Add(key)
			return
		}
	}

	if err != nil {
		b.logger.Error(err, "Failed to get ftc from store")
		b.ftcQueue.Add(key)
		return
	}

	if b.handleFTCUpdate(name, ftc); err != nil {
		b.logger.Error(err, "Failed to handle FTC update")
		b.ftcQueue.Add(key)
	}
}

func (b *MultiClusterFTCControllerBase) processNextCluster(ctx context.Context) {
	key, quit := b.clusterQueue.Get()
	if quit {
		return
	}

	defer b.clusterQueue.Done(key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		b.logger.Error(err, "Failed to split meta namespace key")
		return
	}

	cluster, err := b.clusterInformer.Lister().Get(name)

	if apierrors.IsNotFound(err) || util.IsClusterJoined(&cluster.Status) {
		if err := b.handleClusterUnjoin(name); err != nil {
			b.logger.Error(err, "Failed to handle cluster unjoin")
			b.clusterQueue.Add(key)
			return
		}
	}

	if err != nil {
		b.logger.Error(err, "Failed to get cluster from store")
		b.clusterQueue.Add(key)
		return
	}

	if b.handleClusterJoin(name, cluster); err != nil {
		b.logger.Error(err, "Failed to handle cluster update")
		b.clusterQueue.Add(key)
	}
}

func (b *MultiClusterFTCControllerBase) handleFTCUpdate(name string, ftc *fedcorev1a1.FederatedTypeConfig) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if cancelFunc, ok := b.ftcCancelFuncs[name]; ok {
		cancelFunc()
		delete(b.ftcCancelFuncs, name)
		delete(b.ftcContexts, name)
	}

	gvr := schemautil.APIResourceToGVR(ftc.GetSourceType())

	ctx, cancelFunc := context.WithCancel(context.Background())
	for _, generator := range b.eventHandlerGenerators {
		for clusterName := range b.clusterCancelFuncs {
			cluster, err := b.clusterInformer.Lister().Get(clusterName)
			if err != nil {
				cancelFunc()
				return fmt.Errorf("failed to get cluster %s from store", clusterName)
			}

			handler := generator(ftc, cluster)
			if handler == nil {
				continue
			}

			manager := b.informerManager.GetManager(clusterName)
			if manager == nil {
				cancelFunc()
				return fmt.Errorf("failed to get SingleClusterInformerManager for cluster %s", clusterName)
			}

			if err := manager.ForResourceWithEventHandler(ctx, gvr, handler); err != nil {
				cancelFunc()
				return fmt.Errorf("failed to add event handler: %w", err)
			}
		}
	}

	b.ftcCancelFuncs[name] = cancelFunc
	b.ftcContexts[name] = ctx
	return nil
}

func (b *MultiClusterFTCControllerBase) handleFTCDeletion(name string) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if cancelFunc, ok := b.ftcCancelFuncs[name]; ok {
		cancelFunc()
		delete(b.ftcCancelFuncs, name)
	}

	return nil
}

func (b *MultiClusterFTCControllerBase) handleClusterJoin(name string, cluster *fedcorev1a1.FederatedCluster) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if cancelFunc, ok := b.clusterCancelFuncs[name]; ok {
		cancelFunc()
		delete(b.clusterCancelFuncs, name)
	}

	clusterConfig, err := util.BuildClusterConfig(cluster, b.kubeClient, b.baseClientConfig, b.fedSystemNamespace)
	if err != nil {
		return fmt.Errorf("failed to build cluster client config: %w", err)
	}
	clusterClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		return fmt.Errorf("failed to build cluster client: %w", err)
	}

	clusterCtx, cancelFunc := context.WithCancel(context.Background())
	if err := b.informerManager.ForCluster(clusterCtx, name, clusterClient); err != nil {
		cancelFunc()
		return fmt.Errorf("failed to start informer manager for cluster: %w", err)
	}
	manager := b.informerManager.GetManager(name)
	if manager == nil {
		cancelFunc()
		return fmt.Errorf("failed to get SingleClusterInformerManager for cluster %s", cluster)
	}

	for ftcName, ftcCtx := range b.ftcContexts {
		ftc, err := b.ftcInformer.Lister().Get(ftcName)
		if err != nil {
			cancelFunc()
			return fmt.Errorf("failed to get ftc: %w", err)
		}

		gvr := schemautil.APIResourceToGVR(ftc.GetSourceType())

		for _, generator := range b.eventHandlerGenerators {
			handler := generator(ftc, cluster)
			if handler == nil {
				continue
			}

			if err := manager.ForResourceWithEventHandler(ftcCtx, gvr, handler); err != nil {
				cancelFunc()
				return fmt.Errorf("failed to add event handler: %w", err)
			}
		}
	}

	b.clusterCancelFuncs[name] = cancelFunc
	b.clusterClients[name] = clusterClient
	return nil
}

func (b *MultiClusterFTCControllerBase) handleClusterUnjoin(name string) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if cancelFunc, ok := b.clusterCancelFuncs[name]; ok {
		cancelFunc()
		delete(b.clusterCancelFuncs, name)
		delete(b.clusterClients, name)
	}

	return nil
}
