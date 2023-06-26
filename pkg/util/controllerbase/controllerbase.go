package controllerbase

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
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type EventHandlerGenerator func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler

// FTCControllerBase provides an interface for controllers that need to dynamically register event handlers and perform
// reconciliation based on FederatedTypeConfigs. FTCControllerBase will listen to FTC events and maintain informers for
// the source type of each FTC. It will also handle the registration and unregistration of generated event handlers in
// accordance to the lifecycle of each FTC.
type FTCControllerBase struct {
	ftcInformer     fedcorev1a1informers.FederatedTypeConfigInformer
	informerManager informermanager.SingleClusterInformerManager

	lock                   sync.Mutex
	started                bool
	eventHandlerGenerators []EventHandlerGenerator

	cancelFuncs map[string]context.CancelFunc
	queue       workqueue.Interface

	logger klog.Logger
}

func NewFTCControllerBase(
	ftcInformer fedcorev1a1informers.FederatedTypeConfigInformer,
	dynamicClient dynamic.Interface,
) *FTCControllerBase {
	base := &FTCControllerBase{
		ftcInformer:            ftcInformer,
		informerManager:        informermanager.NewSingleClusterInformerManager(dynamicClient),
		lock:                   sync.Mutex{},
		started:                false,
		eventHandlerGenerators: []EventHandlerGenerator{},
		cancelFuncs:            map[string]context.CancelFunc{},
		queue:                  workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
		logger:                 klog.LoggerWithName(klog.Background(), "ftc-controller-base"),
	}

	enqueueObj := func(o interface{}) {
		key, err := cache.MetaNamespaceKeyFunc(o)
		if err != nil {
			base.logger.Error(err, "Failed to get meta namespace key")
			return
		}
		base.queue.Add(key)
	}

	ftcInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    enqueueObj,
		UpdateFunc: func(_, obj interface{}) { enqueueObj(obj) },
		DeleteFunc: enqueueObj,
	})

	return base
}

// Adds an EventHandlerGenerator that will be used to generate event handlers to be added to each FTC's source type
// informer. Event handlers generated for a FTC will also be unregistered when the FTC is deleted.
// EventHandlerGenerators cannot be added after the FTCControllerBase is started.
//
// NOTE: event handlers generated for a FTC may be temporarily registerd more than once, so it is important to ensure
// that the generated event handlers are idempotent.
func (b *FTCControllerBase) AddEventHandlerGenerator(generator EventHandlerGenerator) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.started {
		return fmt.Errorf("controller is already started")
	}

	b.eventHandlerGenerators = append(b.eventHandlerGenerators, generator)
	return nil
}

// Returns a lister for the given GVR if it exists. The lister for a FTC's source type will exist as long as the FTC
// exists in the view of the FTCControllerBase.
func (b *FTCControllerBase) GetResourceLister(gvr schema.GroupVersionResource) (cache.GenericLister, cache.InformerSynced) {
	return b.informerManager.GetLister(gvr)
}


// Returns the FTC lister used by the FTCControllerBase.
func (b *FTCControllerBase) GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister {
	return b.ftcInformer.Lister()
}

// Returns true if the FTCControllerBase's view of FederatedTypeConfigs is synced.
func (b *FTCControllerBase) IsSynced() bool {
	return b.ftcInformer.Informer().HasSynced()
}

// Starts processing FederatedTypeConfig events.
func (b *FTCControllerBase) Start(ctx context.Context) {
	if !cache.WaitForNamedCacheSync("ftc-controller-base", ctx.Done(), b.ftcInformer.Informer().HasSynced) {
		return
	}

	b.lock.Lock()
	defer b.lock.Unlock()
	b.started = true

	go wait.UntilWithContext(ctx, b.processNextQueueItem, 0)

	go func() {
		<-ctx.Done()
		b.queue.ShutDown()
		b.informerManager.Shutdown()
	}()
}

func (b *FTCControllerBase) processNextQueueItem(ctx context.Context) {
	key, quit := b.queue.Get()
	if quit {
		return
	}

	defer b.queue.Done(key)

	_, name, err := cache.SplitMetaNamespaceKey(key.(string))
	if err != nil {
		b.logger.Error(err, "Failed to split meta namespace key")
		return
	}

	ftc, err := b.ftcInformer.Lister().Get(name)

	if apierrors.IsNotFound(err) {
		if err := b.handleFTCDeletion(name); err != nil {
			b.logger.Error(err, "Failed to handle FTC deletion")
			b.queue.Add(key)
			return
		}
	}

	if err != nil {
		b.logger.Error(err, "Failed to get ftc from store")
		b.queue.Add(key)
		return
	}

	if b.handleFTCUpdate(name, ftc); err != nil {
		b.logger.Error(err, "Failed to handle FTC update")
		b.queue.Add(key)
	}
}

func (b *FTCControllerBase) handleFTCDeletion(name string) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	cancelFunc, ok := b.cancelFuncs[name]
	if !ok {
		return nil
	}

	cancelFunc()
	delete(b.cancelFuncs, name)
	return nil
}

func (b *FTCControllerBase) handleFTCUpdate(name string, ftc *fedcorev1a1.FederatedTypeConfig) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if cancelFunc, ok := b.cancelFuncs[name]; ok {
		cancelFunc()
		delete(b.cancelFuncs, name)
	}

	gvr := schemautil.APIResourceToGVR(ftc.GetSourceType())

	ctx, cancelFunc := context.WithCancel(context.Background())
	if err := b.informerManager.ForResource(ctx, gvr); err != nil {
		cancelFunc()
		return fmt.Errorf("failed to start informer for resource: %w", err)
	}

	for _, generator := range b.eventHandlerGenerators {
		handler := generator(ftc)
		if handler == nil {
			continue
		}
		if err := b.informerManager.ForResourceWithEventHandler(ctx, gvr, handler); err != nil {
			// If we fail to register an event handler, we trigger the previous handlers to be unregistered by
			// cancelling the context.
			//
			// NOTE: there is still a chance that each event handler will be temporarily registered more than once if we
			// reconcile again before the informermanager unregisters the previous handlers - users should be aware and
			// tolerant of this.
			cancelFunc()
			return fmt.Errorf("failed to add event handler: %w", err)
		}
	}

	b.cancelFuncs[name] = cancelFunc
	return nil
}

type MultiClusterFTCControllerBase struct {

}
