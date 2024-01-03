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

package fake

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
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
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetResourceLister(
	gvk schema.GroupVersionKind,
	cluster string,
) (lister cache.GenericLister, informerSynced cache.InformerSynced, exists bool) {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetClusterDynamicClient(cluster string) (client dynamic.Interface, exists bool) {
	client, exists = f.ReadyClusterDynamicClients[cluster]
	return client, exists
}

func (f *FakeFederatedInformerManager) GetClusterKubeClient(cluster string) (client kubernetes.Interface, exists bool) {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetClusterRestConfig(cluster string) (config *rest.Config, exists bool) {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) AddPodEventHandler(handler *informermanager.ResourceEventHandlerWithClusterFuncs) {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetPodLister(
	cluster string,
) (lister corev1listers.PodLister, informerSynced cache.InformerSynced, exists bool) {
	l, exists := f.PodListers[cluster]
	return l.PodLister, func() bool { return l.Synced }, exists
}

func (f *FakeFederatedInformerManager) GetNodeLister(
	cluster string,
) (lister corev1listers.NodeLister, informerSynced cache.InformerSynced, exists bool) {
	l, exists := f.NodeListers[cluster]
	return l.NodeLister, func() bool { return l.Synced }, exists
}

func (f *FakeFederatedInformerManager) GetFederatedTypeConfigLister() fedcorev1a1listers.FederatedTypeConfigLister {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetFederatedClusterLister() fedcorev1a1listers.FederatedClusterLister {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) GetReadyClusters() ([]*fedcorev1a1.FederatedCluster, error) {
	return f.ReadyClusters, nil
}

func (f *FakeFederatedInformerManager) GetJoinedClusters() ([]*fedcorev1a1.FederatedCluster, error) {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) HasSynced() bool {
	return f.HasSync
}

func (f *FakeFederatedInformerManager) AddClusterEventHandlers(handlers ...*informermanager.ClusterEventHandler) error {
	// TODO implement me
	panic("implement me")
}

func (f *FakeFederatedInformerManager) Start(ctx context.Context) {
}

func (f *FakeFederatedInformerManager) IsShutdown() bool {
	return f.Shutdown
}
