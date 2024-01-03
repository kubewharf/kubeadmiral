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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	fakeinformermanager "github.com/kubewharf/kubeadmiral/pkg/util/informermanager/fake"
)

func TestNodeLister_Get(t *testing.T) {
	clusters, listers := newClustersWithNodeListers(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters: clusters,
		NodeListers:   listers,
	}
	nodes := make([]*corev1.Node, 3)
	for i := range nodes {
		name := fmt.Sprintf("cluster-%d", i)
		node := newNode(name)
		clusterobject.MakeObjectUnique(node, name)
		nodes[i] = node
	}

	type fields struct {
		federatedInformerManager informermanager.FederatedInformerManager
	}
	type args struct {
		name string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *corev1.Node
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "get node",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				name: nodes[1].Name,
			},
			want:    nodes[1],
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "node not found",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				name: "fake",
			},
			want:    nil,
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return apierrors.IsNotFound(err) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewNodeLister(tt.fields.federatedInformerManager)
			got, err := n.Get(tt.args.name)
			if !tt.wantErr(t, err, fmt.Sprintf("Get(%v)", tt.args.name)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Get(%v)", tt.args.name)
		})
	}
}

func TestNodeLister_List(t *testing.T) {
	clusters, listers := newClustersWithNodeListers(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters: clusters,
		NodeListers:   listers,
	}
	nodes := make([]*corev1.Node, 3)
	for i := range nodes {
		name := fmt.Sprintf("cluster-%d", i)
		node := newNode(name)
		clusterobject.MakeObjectUnique(node, name)
		nodes[i] = node
	}

	type fields struct {
		federatedInformerManager informermanager.FederatedInformerManager
	}
	type args struct {
		selector labels.Selector
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantRet []*corev1.Node
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "list all",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.Everything(),
			},
			wantRet: []*corev1.Node{nodes[0], nodes[1], nodes[2]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list fake node",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"fake": "node"}),
			},
			wantRet: []*corev1.Node{nodes[0], nodes[1], nodes[2]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list one node",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"node": "cluster-0"}),
			},
			wantRet: []*corev1.Node{nodes[0]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list not found",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"foo": "bar"}),
			},
			wantRet: nil,
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &NodeLister{
				federatedInformerManager: tt.fields.federatedInformerManager,
			}
			gotRet, err := n.List(tt.args.selector)
			if !tt.wantErr(t, err, fmt.Sprintf("List(%v)", tt.args.selector)) {
				return
			}
			assert.Equalf(t, tt.wantRet, gotRet, "List(%v)", tt.args.selector)
		})
	}
}

func newNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"fake": "node",
				"node": name,
			},
		},
	}
}

func newClustersWithNodeListers(num int) ([]*fedcorev1a1.FederatedCluster, map[string]fakeinformermanager.FakeLister) {
	clusters := make([]*fedcorev1a1.FederatedCluster, num)
	listers := make(map[string]fakeinformermanager.FakeLister)

	for i := range clusters {
		name := fmt.Sprintf("cluster-%d", i)
		clusters[i] = &fedcorev1a1.FederatedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}

		listers[name] = fakeinformermanager.FakeLister{
			NodeLister: fakeNodeLister{data: []*corev1.Node{newNode(name)}},
			Synced:     true,
		}
	}
	return clusters, listers
}

// fakes corev1listers.NodeLister at once
type fakeNodeLister struct {
	data []*corev1.Node
	err  error
}

func (nl fakeNodeLister) List(selector labels.Selector) (ret []*corev1.Node, err error) {
	if nl.err != nil {
		return nil, nl.err
	}
	var res []*corev1.Node
	for _, node := range nl.data {
		if selector.Matches(labels.Set(node.Labels)) {
			res = append(res, node)
		}
	}
	return res, nil
}

func (nl fakeNodeLister) Get(name string) (*corev1.Node, error) {
	if nl.err != nil {
		return nil, nl.err
	}
	for _, node := range nl.data {
		if node.Name == name {
			return node, nil
		}
	}
	return nil, nil
}
