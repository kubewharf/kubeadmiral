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
	// Predicate is called each time a FTC is reconciled to determine if a event handler needs to be registered for this
	// EventHandlerGenerator. If Predicate returns false, any previously registered event handler for this
	// EventHandlerGenerator will also be unregistered.
	//
	// Note: updating of event handlers is intentionally unsupported as registering a new event handler would cause all
	// existing objects in the cache to be sent to it as add events, potentially causing performance problems. In other
	// words, if Predicate returns true and there is already a registered event handler for this EventHandlerGenerator,
	// a new event handler will not be generated.
	Predicate func(ftc *fedcorev1a1.FederatedTypeConfig) bool
	// Generator is used to generate a ResourceEventHandler for the given FTC. Generator MUST not return nil.
	Generator func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler
}

// InformerManager provides an interface for controllers that need to dynamically register event handlers and access
// objects based on FederatedTypeConfigs. InformerManager will listen to FTC events and maintain informers for the
// source type of each FTC.
//
// Having multiple FTCs with the same source type is not supported and may cause InformerManager to behave incorrectly.
// Updating FTC source types is also not supported and may also cause InformerManager to behave incorrectly.
type InformerManager interface {
	// Adds an EventHandler used to generate and register ResourceEventHandlers for each FTC's source type informer.
	AddEventHandlerGenerator(generator *EventHandlerGenerator) error
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
	Callback func(cluster *fedcorev1a1.FederatedCluster)
}

// ClusterEventPredicate determines if a callback should be called for a given cluster event.
type ClusterEventPredicate func(oldCluster, newCluster *fedcorev1a1.FederatedCluster) bool

// FederatedInformerManager provides an interface for controllers that need to dynamically register event handlers and
// access objects in member clusters based on FederatedTypeConfigs. FederatedInformerManager will listen to FTC events
// and maintian informers for each FTC's source type and joined member cluster.
//
// Having multiple FTCs with the same source type is not supported and may cause FederatedInformerManager to behave
// incorrectly. Updating FTC source types is also not supported and may also cause FederatedInformerManager to behave
// incorrectly.
//
// Updating Cluster connection details is also not supported and may cause FederatedInformerManager to behave
// incorrectly.
type FederatedInformerManager interface {
	// Adds an EventHandler used to generate and register ResourceEventHandlers for each FTC's source type informer.
	AddEventHandlerGenerator(generator *EventHandlerGenerator) error
	// Returns a lister for the given GroupResourceVersion and cluster if it exists. The lister for each FTC's source
	// type and cluster will eventually exist.
	GetResourceLister(
		gvr schema.GroupVersionResource,
		cluster string,
	) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool)
	// Returns a client for the given cluster if it exists. The client for each cluster will eventually exist.
	GetClusterClient(cluster string) (client dynamic.Interface, exists bool)

	// Returns the FederatedTypeConfig lister used by the FederatedInformerManager.
	GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister
	// Returns the FederatedCluster lister used by the FederatedInformerManager.
	GetFederatedClusterLister() fedcorev1a1listers.FederatedClusterLister
	// Returns true if the FederatedInformerManager's view of FederatedTypeConfigs and FederatedClusters is synced.
	HasSynced() bool

	// Adds a ClusterEventHandler that can be used by controllers to hook into the cluster events received by the
	// FederatedInformerManager.
	AddClusterEventHandler(handler *ClusterEventHandler) error

	// Starts processing FederatedTypeConfig and FederatedCluster events.
	Start(ctx context.Context)
}

// ClusterClientGetter is used by the FederatedInformerManager to create clients for joined member clusters.
type ClusterClientGetter struct {
	// ConnectionHash should return a string that uniquely identifies the combination of parameters used to generate the
	// cluster client. A change in the connection hash indicates a need to create a new client for a given member
	// cluster.
	ConnectionHash func(cluster *fedcorev1a1.FederatedCluster) string
	// ClientGetter returns a dynamic client for the given member cluster.
	ClientGetter   func(cluster *fedcorev1a1.FederatedCluster) (dynamic.Interface, error)
}
