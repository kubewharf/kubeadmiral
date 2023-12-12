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

package aggregatedlister

import (
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type NodeLister struct {
	federatedInformerManager informermanager.FederatedInformerManager
}

var _ corev1listers.NodeLister = &NodeLister{}

func NewNodeLister(informer informermanager.FederatedInformerManager) *NodeLister {
	return &NodeLister{federatedInformerManager: informer}
}

func (n *NodeLister) List(selector labels.Selector) (ret []*corev1.Node, err error) {
	clusters, err := n.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	for _, cluster := range clusters {
		nodeLister, nodesSynced, exists := n.federatedInformerManager.GetNodeLister(cluster.Name)
		if !exists || !nodesSynced() {
			continue
		}
		nodes, err := nodeLister.List(selector)
		if err != nil {
			continue
		}
		for i := range nodes {
			node := nodes[i].DeepCopy()
			clusterobject.MakeObjectUnique(node, cluster.Name)
			ret = append(ret, node)
		}
	}
	return ret, nil
}

func (n *NodeLister) Get(name string) (*corev1.Node, error) {
	clusters, err := n.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusterobject.GetPossibleClusters(clusters, name) {
		nodeLister, nodesSynced, exists := n.federatedInformerManager.GetNodeLister(cluster)
		if !exists || !nodesSynced() {
			continue
		}
		nodes, err := nodeLister.List(labels.Everything())
		if err != nil {
			continue
		}
		for i := range nodes {
			if name == clusterobject.GenUniqueName(cluster, nodes[i].Name) {
				node := nodes[i].DeepCopy()
				clusterobject.MakeObjectUnique(node, cluster)
				return node, nil
			}
		}
	}
	return nil, apierrors.NewNotFound(corev1.Resource("node"), name)
}
