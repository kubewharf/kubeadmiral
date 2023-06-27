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
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

type SingleClusterInformerManager interface {
	// Starts an informer for the given GroupResourceVersion if there isn't already one running. The new/existing
	// informer is guaranteed to run as long as the given context remains uncancelled.
	ForResource(ctx context.Context, gvr schema.GroupVersionResource) error
	// Same as ForResource, but registers the given event handler to the new/existing informer. The event handler is
	// additionally unregistered when the given context expires.
	ForResourceWithEventHandler(ctx context.Context, gvr schema.GroupVersionResource, eventHandler cache.ResourceEventHandler) error

	// Returns the lister for the given GroupResourceVersion's informer. The informer must have been started with
	// ForResource or FourResourceWithEventHandler and still be running.
	GetLister(gvr schema.GroupVersionResource) (cache.GenericLister, cache.InformerSynced)

	// Forcibly stops all running informers and prevents any new informers from being started.
	Shutdown()
}

type MultiClusterInformerManager interface {
	// Starts aninformer manager for the given cluster if there isn't already one running. The new/existing informer
	// manager is guaranteed to run as long as the given context remains uncancelled.
	ForCluster(ctx context.Context, cluster string, client dynamic.Interface) error

	// Returns a cluster's SingleClusterInformerManager. The informer manager must have been started with ForCluster
	// previously and still be running.
	GetManager(cluster string) SingleClusterInformerManager
}
