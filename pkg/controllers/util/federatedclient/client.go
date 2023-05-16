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

package federatedclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	dynamicclient "k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformer "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

var _ FederatedClientFactory = &federatedClientFactory{}

type federatedClientFactory struct {
	mu sync.RWMutex

	client     fedclient.Interface
	kubeClient kubeclient.Interface
	informer   fedcorev1a1informers.FederatedClusterInformer
	handle     cache.ResourceEventHandlerRegistration

	fedSystemNamespace  string
	memberClientBuilder MemberClientBuilder

	clientUpdateHandlers []ClientUpdateHandler
	queue                workqueue.Interface

	clusterErrors         map[string]error
	kubeClientsetCache    map[string]kubeclient.Interface
	dynamicClientsetCache map[string]dynamicclient.Interface
	kubeInformerCache     map[string]kubeinformer.SharedInformerFactory
	dynamicInformerCache  map[string]dynamicinformer.DynamicSharedInformerFactory

	availablePodListers *semaphore.Weighted
	enablePodPruning    bool
}

type MemberClientBuilder interface {
	Build(
		ctx context.Context,
		kubeClient kubeclient.Interface,
		cluster *fedcorev1a1.FederatedCluster,
		fedSystemNamespace string,
	) (kubeclient.Interface, dynamicclient.Interface, error)
}

type BaseRestMemberClientBuilder struct {
	Base *rest.Config
}

func (b *BaseRestMemberClientBuilder) Build(
	ctx context.Context,
	kubeClient kubeclient.Interface,
	cluster *fedcorev1a1.FederatedCluster,
	fedSystemNamespace string,
) (
	kubeClientset kubeclient.Interface,
	dynamicClientset dynamicclient.Interface,
	err error,
) {
	restConfig := copyRestConfig(b.Base)
	restConfig.Host = cluster.Spec.APIEndpoint

	clusterSecretRef, err := kubeClient.CoreV1().
		Secrets(fedSystemNamespace).
		Get(ctx, cluster.Spec.SecretRef.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to retrieve cluster secret: %w", err)
	}

	if err := util.PopulateAuthDetailsFromSecret(
		restConfig,
		cluster.Spec.Insecure,
		clusterSecretRef,
		cluster.Spec.UseServiceAccountToken,
	); err != nil {
		return nil, nil, fmt.Errorf("failed to bulid rest config from cluster secret: %w", err)
	}

	if kubeClientset, err = kubeclient.NewForConfig(restConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to create kube clientset: %w", err)
	}

	if dynamicClientset, err = dynamicclient.NewForConfig(restConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to create dynamic clientset: %w", err)
	}

	return kubeClientset, dynamicClientset, nil
}

func NewFederatedClientsetFactory(
	fedClient fedclient.Interface,
	kubeClient kubeclient.Interface,
	informer fedcorev1a1informers.FederatedClusterInformer,
	fedSystemNamespace string,
	baseRestConfig *rest.Config,
	maxPodListers int64,
	enablePodPruning bool,
) FederatedClientFactory {
	return NewFederatedClientsetFactoryWithBuilder(
		fedClient,
		kubeClient,
		informer,
		fedSystemNamespace,
		maxPodListers,
		enablePodPruning,
		&BaseRestMemberClientBuilder{Base: baseRestConfig},
	)
}

func NewFederatedClientsetFactoryWithBuilder(
	fedClient fedclient.Interface,
	kubeClient kubeclient.Interface,
	informer fedcorev1a1informers.FederatedClusterInformer,
	fedSystemNamespace string,
	maxPodListers int64,
	enablePodPruning bool,
	memberClientBuilder MemberClientBuilder,
) FederatedClientFactory {
	factory := &federatedClientFactory{
		mu:                    sync.RWMutex{},
		client:                fedClient,
		kubeClient:            kubeClient,
		informer:              informer,
		fedSystemNamespace:    fedSystemNamespace,
		memberClientBuilder:   memberClientBuilder,
		queue:                 workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
		clientUpdateHandlers:  []ClientUpdateHandler{},
		clusterErrors:         map[string]error{},
		kubeClientsetCache:    map[string]kubeclient.Interface{},
		dynamicClientsetCache: map[string]dynamicclient.Interface{},
		kubeInformerCache:     map[string]kubeinformer.SharedInformerFactory{},
		dynamicInformerCache:  map[string]dynamicinformer.DynamicSharedInformerFactory{},
		enablePodPruning:      enablePodPruning,
	}
	if maxPodListers > 0 {
		factory.availablePodListers = semaphore.NewWeighted(maxPodListers)
	}
	factory.handle, _ = informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c := obj.(*fedcorev1a1.FederatedCluster)
			factory.enqueueCluster(c)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldCluster := oldObj.(*fedcorev1a1.FederatedCluster)
			newCluster := newObj.(*fedcorev1a1.FederatedCluster)

			// only enqueue on generation change or cluster join condition changes
			if oldCluster.Generation != newCluster.Generation ||
				util.IsClusterJoined(&oldCluster.Status) != util.IsClusterJoined(&newCluster.Status) {
				factory.enqueueCluster(newCluster)
			}
		},
		DeleteFunc: func(obj interface{}) {
			c := obj.(*fedcorev1a1.FederatedCluster)
			factory.enqueueCluster(c)
		},
	})
	return factory
}

func (f *federatedClientFactory) Start(ctx context.Context) {
	go func() {
		defer f.queue.ShutDown()
		defer f.informer.Informer().RemoveEventHandler(f.handle)

		<-ctx.Done()
	}()

	if !cache.WaitForNamedCacheSync("federated-client-factory", ctx.Done(), f.informer.Informer().HasSynced) {
		return
	}

	go wait.UntilWithContext(ctx, f.processQueueItem, 0)
	go wait.UntilWithContext(ctx, f.startInformerFactories, 500*time.Millisecond)
}

func (f *federatedClientFactory) AddClientUpdateHandler(handler ClientUpdateHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clientUpdateHandlers = append(f.clientUpdateHandlers, handler)
}

func (f *federatedClientFactory) KubeClientsetForCluster(cluster string) (kubeclient.Interface, bool, error) {
	if !f.informer.Informer().HasSynced() {
		return nil, false, errors.New("clusters not yet synced")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	err := f.clusterErrors[cluster]
	if err != nil {
		return nil, true, err
	}

	clientset := f.kubeClientsetCache[cluster]
	if clientset == nil {
		return nil, false, nil
	}

	return clientset, true, nil
}

func (f *federatedClientFactory) DynamicClientsetForCluster(cluster string) (dynamicclient.Interface, bool, error) {
	if !f.informer.Informer().HasSynced() {
		return nil, false, errors.New("clusters not yet synced")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	err := f.clusterErrors[cluster]
	if err != nil {
		return nil, true, err
	}

	clientset := f.dynamicClientsetCache[cluster]
	if clientset == nil {
		return nil, false, nil
	}

	return clientset, true, nil
}

func (f *federatedClientFactory) KubeSharedInformerFactoryForCluster(
	cluster string,
) (kubeinformer.SharedInformerFactory, bool, error) {
	if !f.informer.Informer().HasSynced() {
		return nil, false, errors.New("clusters not yet synced")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	err := f.clusterErrors[cluster]
	if err != nil {
		return nil, true, err
	}

	informerFactory := f.kubeInformerCache[cluster]
	if informerFactory == nil {
		return nil, false, nil
	}

	return informerFactory, true, nil
}

func (f *federatedClientFactory) DynamicSharedInformerFactoryForCluster(
	cluster string,
) (dynamicinformer.DynamicSharedInformerFactory, bool, error) {
	if !f.informer.Informer().HasSynced() {
		return nil, false, errors.New("clusters not yet synced")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	err := f.clusterErrors[cluster]
	if err != nil {
		return nil, true, err
	}

	informerFactory := f.dynamicInformerCache[cluster]
	if informerFactory == nil {
		return nil, false, nil
	}

	return informerFactory, true, nil
}

func (f *federatedClientFactory) enqueueCluster(obj runtime.Object) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to get key for object %+v: %v", obj, err))
	}
	f.queue.Add(key)
}

func (f *federatedClientFactory) processQueueItem(ctx context.Context) {
	key, quit := f.queue.Get()
	if quit {
		return
	}
	defer f.queue.Done(key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		utilruntime.HandleError(err)
		return
	}

	cluster, err := f.informer.Lister().Get(name)
	if err != nil && !apierrors.IsNotFound(err) {
		utilruntime.HandleError(err)
		f.queue.Add(key)
		return
	}

	defer f.sendClientUpdate(name)

	if err != nil && apierrors.IsNotFound(err) {
		// cluster was deleted so we clear the caches
		f.clearCaches(name)
		return
	}
	if !util.IsClusterJoined(&cluster.Status) {
		// we should not provide clients for unjoined clusters
		f.clearCaches(name)
		return
	}

	kubeClientset, dynamicClientset, err := f.memberClientBuilder.Build(ctx, f.kubeClient, cluster, f.fedSystemNamespace)
	if err != nil {
		f.updateCachesWithError(name, err)
		f.queue.Add(key)
		return
	}

	kubeInformerFactory := kubeinformer.NewSharedInformerFactory(kubeClientset, util.NoResyncPeriod)
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClientset, util.NoResyncPeriod)

	f.mu.Lock()
	f.clusterErrors[name] = nil
	f.kubeClientsetCache[name] = kubeClientset
	f.dynamicClientsetCache[name] = dynamicClientset
	f.kubeInformerCache[name] = kubeInformerFactory
	f.dynamicInformerCache[name] = dynamicInformerFactory
	addPodInformer(ctx, kubeInformerFactory, kubeClientset, f.availablePodListers, f.enablePodPruning)
	f.mu.Unlock()
}

func (f *federatedClientFactory) updateCachesWithError(cluster string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.clusterErrors[cluster] = err
	f.kubeClientsetCache[cluster] = nil
	f.dynamicClientsetCache[cluster] = nil
	f.kubeInformerCache[cluster] = nil
	f.dynamicInformerCache[cluster] = nil
}

func (f *federatedClientFactory) clearCaches(cluster string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.clusterErrors, cluster)
	delete(f.kubeClientsetCache, cluster)
	delete(f.dynamicClientsetCache, cluster)
	delete(f.kubeInformerCache, cluster)
	delete(f.dynamicInformerCache, cluster)
}

func (f *federatedClientFactory) sendClientUpdate(cluster string) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, handler := range f.clientUpdateHandlers {
		handler(cluster, f)
	}
}

func (f *federatedClientFactory) startInformerFactories(ctx context.Context) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, factory := range f.kubeInformerCache {
		if factory != nil {
			factory.Start(ctx.Done())
		}
	}
	for _, factory := range f.dynamicInformerCache {
		if factory != nil {
			factory.Start(ctx.Done())
		}
	}
}

func copyRestConfig(config *rest.Config) *rest.Config {
	return &rest.Config{
		QPS:       config.QPS,
		Burst:     config.Burst,
		UserAgent: config.UserAgent,
	}
}
