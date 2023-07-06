package informermanager

import (
	"context"
	"github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

type federatedInformerManager struct{}

// AddClusterEventHandler implements FederatedInformerManager.
func (*federatedInformerManager) AddClusterEventHandler(handler ClusterEventHandler) error {
	panic("unimplemented")
}

// AddEventHandlerGenerator implements FederatedInformerManager.
func (*federatedInformerManager) AddEventHandlerGenerator(generator *EventHandlerGenerator) error {
	panic("unimplemented")
}

// GetClusterClient implements FederatedInformerManager.
func (*federatedInformerManager) GetClusterClient(cluster string) (dynamic.Interface, bool) {
	panic("unimplemented")
}

// GetFederatedClusterLister implements FederatedInformerManager.
func (*federatedInformerManager) GetFederatedClusterLister() v1alpha1.FederatedClusterLister {
	panic("unimplemented")
}

// GetFederatedTypeConfigLister implements FederatedInformerManager.
func (*federatedInformerManager) GetFederatedTypeConfigLister() v1alpha1.FederatedTypeConfigLister {
	panic("unimplemented")
}

// GetResourceLister implements FederatedInformerManager.
func (*federatedInformerManager) GetResourceLister(gvr schema.GroupVersionResource, cluster string) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	panic("unimplemented")
}

// HasSynced implements FederatedInformerManager.
func (*federatedInformerManager) HasSynced() bool {
	panic("unimplemented")
}

// Start implements FederatedInformerManager.
func (*federatedInformerManager) Start(ctx context.Context) {
	panic("unimplemented")
}

var _ FederatedInformerManager = &federatedInformerManager{}
