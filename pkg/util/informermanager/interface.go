package informermanager

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
)

// EventHandlerGenerator is used by InformerManger and FederatedInformerManager to generate and register
// ResourceEventHandlers for each FTC's source type informer.
type EventHandlerGenerator struct {
	// Predicate is called for each FTC add or update event. Predicate should return True only if a new
	// ResoureEventHandler should be generated and registered for the given FTC. Previous event handlers registered for
	// this EventHandlerGenerator will also be removed.
	//
	// Note: we should be cautious about registering new ResourceEventHandler as it will receive synthetic add events
	// for every object in the informer's cache.
	Predicate func(oldFTC, newFTC *fedcorev1a1.FederatedTypeConfig) bool
	// Generator is used to generate a ResourceEventHandler for the given FTC. If nil is returned, no
	// ResourceEventHandler will be registered.
	Generator func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler
}

// InformerManager provides an interface for controllers that need to dynamically register event handlers and access
// objects based on FederatedTypeConfigs. InformerManager will listen to FTC events and maintain informers for the
// source type of each FTC.
type InformerManager interface {
	// Adds an EventHandler used to generate and register ResourceEventHandlers for each FTC's source type informer.
	AddEventHandlerGenerator(generator EventHandlerGenerator) error
	// Returns a lister for the given GroupResourceVersion if it exists. The lister for each FTC's source type will
	// eventually exist.
	GetResourceLister(gvr schema.GroupVersionResource) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool)

	// Returns the FederatedTypeConfig lister used by the InformerManager.
	GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister
	// Returns true if the InformerManager's view of FederatedTypeConfigs is synced.
	HasSynced() bool

	// Starts processing FederatedTypeConfig events.
	Start(ctx context.Context)
}

// ClusterEventHandler can be registered by controllers to hook into the cluster events received by the
// FederatedInformerManager.
type ClusterEventHandler struct {
	// ClusterEventPredicate is called for each FederatedCluster event and determines if the callback of this
	// ClusterEventHandler should be called for the the given event.
	Predicate ClusterEventPredicate
	// Callback is a function that accepts a FederatedCluster object.
	Callback  func(cluster *fedcorev1a1.FederatedCluster)
}

// ClusterEventPredicate determines if a callback should be called for a given cluster event.
type ClusterEventPredicate func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool

// FederatedInformerManager provides an interface for controllers that need to dynamically register event handlers and
// access objects in member clusters based on FederatedTypeConfigs. FederatedInformerManager will listen to FTC events
// and maintian informers for each FTC's source type and joined member cluster.
type FederatedInformerManager interface {
	// Adds an EventHandler used to generate and register ResourceEventHandlers for each FTC's source type informer.
	AddEventHandlerGenerator(generator EventHandlerGenerator) error
	// Returns a lister for the given GroupResourceVersion and cluster if it exists. The lister for each FTC's source
	// type and cluster will eventually exist.
	GetResourceLister(
		gvr schema.GroupVersionResource,
		cluster string,
	) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool)
	// Returns a client for the given cluster if it exists. The client for each cluster will eventually exist.
	GetClusterClient(cluster string) (dynamic.Interface, bool)

	// Returns the FederatedTypeConfig lister used by the FederatedInformerManager.
	GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister
	// Returns the FederatedCluster lister used by the FederatedInformerManager.
	GetFederatedClusterLister() fedcorev1a1listers.FederatedClusterLister
	// Returns true if the FederatedInformerManager's view of FederatedTypeConfigs and FederatedClusters is synced.
	HasSynced() bool

	// Adds a ClusterEventHandler that can be used by controllers to hook into the cluster events received by the
	// FederatedInformerManager.
	AddClusterEventHandler(handler ClusterEventHandler) error

	// Starts processing FederatedTypeConfig and FederatedCluster events.
	Start(ctx context.Context)
}
