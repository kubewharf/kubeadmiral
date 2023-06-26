package informermanager

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

type SingleClusterInformerManager interface {
	// Starts an informer for the given GroupResourceVersion. The informer is guaranteed to run for as long as the given
	// context remains uncancelled. If there is already a running informer for the GroupVersionResource, the existing
	// informer is also guarnateed to run for as long as the given context remains uncancelled.
	ForResource(ctx context.Context, gvr schema.GroupVersionResource) error
	// Same as ForResource but registers the given event handler to the new or existing informer. Additionally, the
	// event handler is automatically unregisterd when the given context is cancelled.
	ForResourceWithEventHandler(ctx context.Context, gvr schema.GroupVersionResource, eventHandler cache.ResourceEventHandler) error

	// Same as ForResource, but also returns a lister that is backed by the new or existing informer.
	ListerForResource(ctx context.Context, gvr schema.GroupVersionResource) (cache.GenericLister, cache.InformerSynced, error)

	// Stops all running informers.
	Shutdown()
}

type MultiClusterInformerManager interface {
	// Starts and returns a SingleClusterInformerManager for the given cluster. The informer manager is guaranteed to
	// run for as long as the context remains uncancelled. If there is already a running informer manager for the given
	// cluster, the old informer manager is returned and the old infomer is also guaranteed to run for as long as the
	// given context remains uncancelled.
	ForCluster(ctx context.Context, cluster string, client dynamic.Interface) SingleClusterInformerManager

	// Returns a cluster's SingleClusterInformerManager. The informer manager must have been started with ForCluster
	// previously and still be running. The informer is guaranteed to run for as long as the given context remains
	// uncancelled. If there is no running informer manager for the given cluster, nil is returned.
	GetCluster(ctx context.Context, cluster string) SingleClusterInformerManager
}
