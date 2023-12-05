package fake

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

var _ informermanager.FederatedInformerManager = &FakeFederatedInformerManager{}

// FakeFederatedInformerManager implements informermanager.FederatedInformerManager and should only be used in tests
type FakeFederatedInformerManager struct {
	HasSync  bool
	Shutdown bool

	ReadyClusters              []*fedcorev1a1.FederatedCluster
	ReadyClusterDynamicClients map[string]dynamic.Interface
	NodeListers                map[string]FakeLister
	PodListers                 map[string]FakeLister
}

type FakeLister struct {
	PodLister  corev1listers.PodLister
	NodeLister corev1listers.NodeLister
	Synced     bool
}

func (f *FakeFederatedInformerManager) AddEventHandlerGenerator(generator *informermanager.EventHandlerGenerator) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetResourceLister(gvk schema.GroupVersionKind, cluster string) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetClusterDynamicClient(cluster string) (client dynamic.Interface, exists bool) {
	client, exists = f.ReadyClusterDynamicClients[cluster]
	return client, exists
}

func (f *FakeFederatedInformerManager) GetClusterKubeClient(cluster string) (client kubernetes.Interface, exists bool) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetClusterDiscoveryClient(cluster string) (client discovery.DiscoveryInterface, exists bool) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetClusterRestConfig(cluster string) (config *rest.Config, exists bool) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) AddPodEventHandler(handler *informermanager.ResourceEventHandlerWithClusterFuncs) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetPodLister(cluster string) (lister corev1listers.PodLister, informerSynced cache.InformerSynced, exists bool) {
	l, exists := f.PodListers[cluster]
	return l.PodLister, func() bool { return l.Synced }, exists
}

func (f *FakeFederatedInformerManager) GetNodeLister(cluster string) (lister corev1listers.NodeLister, informerSynced cache.InformerSynced, exists bool) {
	l, exists := f.NodeListers[cluster]
	return l.NodeLister, func() bool { return l.Synced }, exists
}

func (f *FakeFederatedInformerManager) GetFederatedTypeConfigLister() v1alpha1.FederatedTypeConfigLister {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetFederatedClusterLister() v1alpha1.FederatedClusterLister {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetReadyClusters() ([]*fedcorev1a1.FederatedCluster, error) {
	return f.ReadyClusters, nil
}

func (f *FakeFederatedInformerManager) GetJoinedClusters() ([]*fedcorev1a1.FederatedCluster, error) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) HasSynced() bool {
	return f.HasSync
}

func (f *FakeFederatedInformerManager) AddClusterEventHandlers(handlers ...*informermanager.ClusterEventHandler) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) Start(ctx context.Context) {
}

func (f *FakeFederatedInformerManager) IsShutdown() bool {
	return f.Shutdown
}
