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
	"k8s.io/apimachinery/pkg/runtime"
	corev1listers "k8s.io/client-go/listers/core/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	fakeinformermanager "github.com/kubewharf/kubeadmiral/pkg/util/informermanager/fake"
)

func TestPodLister_Get(t *testing.T) {
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
		want    runtime.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:   "bad request",
			fields: fields{},
			args: args{
				name: "aa",
			},
			want:    nil,
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return apierrors.IsBadRequest(err) },
		},
		{
			name: "normal",
			fields: fields{
				federatedInformerManager: &fakeinformermanager.FakeFederatedInformerManager{},
			},
			args: args{
				name: "a/b",
			},
			want:    nil,
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPodLister(tt.fields.federatedInformerManager)
			got, err := p.Get(tt.args.name)
			if !tt.wantErr(t, err, fmt.Sprintf("Get(%v)", tt.args.name)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Get(%v)", tt.args.name)
		})
	}
}

func TestPodLister_List(t *testing.T) {
	clusters, listers := newClustersWithPodListers(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters: clusters,
		PodListers:    listers,
	}
	pods := make([]*corev1.Pod, 3)
	for i := range pods {
		name := fmt.Sprintf("cluster-%d", i)
		pod := newPod("default", name)
		clusterobject.MakePodUnique(pod, name)
		pods[i] = pod
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
		wantRet []runtime.Object
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
			wantRet: []runtime.Object{pods[0], pods[1], pods[2]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list fake pod",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"fake": "pod"}),
			},
			wantRet: []runtime.Object{pods[0], pods[1], pods[2]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list one pod",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"pod": "cluster-0"}),
			},
			wantRet: []runtime.Object{pods[0]},
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
			p := &PodLister{
				federatedInformerManager: tt.fields.federatedInformerManager,
			}
			gotRet, err := p.List(tt.args.selector)
			if !tt.wantErr(t, err, fmt.Sprintf("List(%v)", tt.args.selector)) {
				return
			}
			assert.Equalf(t, tt.wantRet, gotRet, "List(%v)", tt.args.selector)
		})
	}
}

func TestPodNamespaceLister_Get(t *testing.T) {
	clusters, listers := newClustersWithPodListers(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters: clusters,
		PodListers:    listers,
	}
	pods := make([]*corev1.Pod, 3)
	for i := range pods {
		name := fmt.Sprintf("cluster-%d", i)
		pod := newPod("default", name)
		clusterobject.MakePodUnique(pod, name)
		pods[i] = pod
	}

	type fields struct {
		namespace                string
		federatedInformerManager informermanager.FederatedInformerManager
	}
	type args struct {
		name string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    runtime.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "get pod",
			fields: fields{
				federatedInformerManager: informer,
			},
			args: args{
				name: pods[1].Name,
			},
			want:    pods[1],
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "pod not found",
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
			p := &PodNamespaceLister{
				namespace:                tt.fields.namespace,
				federatedInformerManager: tt.fields.federatedInformerManager,
			}
			got, err := p.Get(tt.args.name)
			if !tt.wantErr(t, err, fmt.Sprintf("Get(%v)", tt.args.name)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Get(%v)", tt.args.name)
		})
	}
}

func TestPodNamespaceLister_List(t *testing.T) {
	clusters, listers := newClustersWithPodListers(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters: clusters,
		PodListers:    listers,
	}
	pods := make([]*corev1.Pod, 3)
	for i := range pods {
		name := fmt.Sprintf("cluster-%d", i)
		pod := newPod("default", name)
		clusterobject.MakePodUnique(pod, name)
		pods[i] = pod
	}

	type fields struct {
		namespace                string
		federatedInformerManager informermanager.FederatedInformerManager
	}
	type args struct {
		selector labels.Selector
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantRet []runtime.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "list all",
			fields: fields{
				namespace:                "default",
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.Everything(),
			},
			wantRet: []runtime.Object{pods[0], pods[1], pods[2]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list fake pod",
			fields: fields{
				namespace:                "default",
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"fake": "pod"}),
			},
			wantRet: []runtime.Object{pods[0], pods[1], pods[2]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list one pod",
			fields: fields{
				namespace:                "default",
				federatedInformerManager: informer,
			},
			args: args{
				selector: labels.SelectorFromSet(map[string]string{"pod": "cluster-0"}),
			},
			wantRet: []runtime.Object{pods[0]},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool { return err == nil },
		},
		{
			name: "list not found",
			fields: fields{
				namespace:                "default",
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
			p := &PodNamespaceLister{
				namespace:                tt.fields.namespace,
				federatedInformerManager: tt.fields.federatedInformerManager,
			}
			gotRet, err := p.List(tt.args.selector)
			if !tt.wantErr(t, err, fmt.Sprintf("List(%v)", tt.args.selector)) {
				return
			}
			assert.Equalf(t, tt.wantRet, gotRet, "List(%v)", tt.args.selector)
		})
	}
}

func newPod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"fake": "pod",
				"pod":  name,
			},
		},
	}
}

func newClustersWithPodListers(num int) ([]*fedcorev1a1.FederatedCluster, map[string]fakeinformermanager.FakeLister) {
	clusters := make([]*fedcorev1a1.FederatedCluster, num)
	listers := make(map[string]fakeinformermanager.FakeLister)
	ns := "default"

	for i := range clusters {
		name := fmt.Sprintf("cluster-%d", i)
		clusters[i] = &fedcorev1a1.FederatedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}

		listers[name] = fakeinformermanager.FakeLister{
			PodLister: fakePodLister{data: []*corev1.Pod{newPod(ns, name)}},
			Synced:    true,
		}
	}
	return clusters, listers
}

// fakes both PodLister and PodNamespaceLister at once
type fakePodLister struct {
	data []*corev1.Pod
	err  error
}

func (pl fakePodLister) List(selector labels.Selector) (ret []*corev1.Pod, err error) {
	if pl.err != nil {
		return nil, pl.err
	}
	res := []*corev1.Pod{}
	for _, pod := range pl.data {
		if selector.Matches(labels.Set(pod.Labels)) {
			res = append(res, pod)
		}
	}
	return res, nil
}

func (pl fakePodLister) Get(name string) (*corev1.Pod, error) {
	if pl.err != nil {
		return nil, pl.err
	}
	for _, pod := range pl.data {
		if pod.Name == name {
			return pod, nil
		}
	}
	return nil, nil
}

func (pl fakePodLister) Pods(namespace string) corev1listers.PodNamespaceLister {
	return pl
}
