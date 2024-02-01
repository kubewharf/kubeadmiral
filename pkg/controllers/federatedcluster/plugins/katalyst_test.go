/*
Copyright 2024 The KubeAdmiral Authors.

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

package plugins

import (
	"context"
	"errors"
	"reflect"
	"testing"

	katalystv1a1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	katalystconsts "github.com/kubewharf/katalyst-api/pkg/consts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

func newNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func newCNR(name string, allocatable corev1.ResourceList) *katalystv1a1.CustomNodeResource {
	return &katalystv1a1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: katalystv1a1.CustomNodeResourceStatus{
			Resources: katalystv1a1.Resources{Allocatable: &allocatable},
		},
	}
}

func newUnsCNR(name string, allocatable corev1.ResourceList) *unstructured.Unstructured {
	uns, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(newCNR(name, allocatable))
	return &unstructured.Unstructured{Object: uns}
}

func newPod(name string, requests corev1.ResourceList, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: requests,
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase: phase,
		},
	}
}

type fakeUnstructuredLister struct {
	data []*unstructured.Unstructured
	err  error
}

func (pl fakeUnstructuredLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	if pl.err != nil {
		return nil, pl.err
	}
	res := []runtime.Object{}
	for _, uns := range pl.data {
		if selector.Matches(labels.Set(uns.GetLabels())) {
			res = append(res, uns)
		}
	}
	return res, nil
}

func (pl fakeUnstructuredLister) Get(name string) (runtime.Object, error) {
	if pl.err != nil {
		return nil, pl.err
	}
	for _, uns := range pl.data {
		if uns.GetName() == name {
			return uns, nil
		}
	}
	return nil, nil
}

func (pl fakeUnstructuredLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return pl
}

func Test_katalystPlugin_CollectClusterResources(t *testing.T) {
	type args struct {
		nodes  []*corev1.Node
		pods   []*corev1.Pod
		handle ClusterHandle
	}
	tests := []struct {
		name            string
		args            args
		wantAllocatable corev1.ResourceList
		wantAvailable   corev1.ResourceList
		wantErr         bool
	}{
		{
			name: "0 cnr",
			args: args{
				nodes: []*corev1.Node{newNode("node1")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodRunning,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{
						katalystCNR: fakeUnstructuredLister{},
					},
				},
			},
			wantAllocatable: nil,
			wantAvailable:   nil,
			wantErr:         false,
		},
		{
			name: "2 nodes, 1 pod, 2 cnr",
			args: args{
				nodes: []*corev1.Node{newNode("node1"), newNode("node2")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodRunning,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{
						katalystCNR: fakeUnstructuredLister{
							data: []*unstructured.Unstructured{
								newUnsCNR("node1", corev1.ResourceList{
									katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
									katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
								}),
								newUnsCNR("node2", corev1.ResourceList{
									katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("2k"),
									katalystconsts.ReclaimedResourceMemory:   resource.MustParse("2Mi"),
								}),
							},
						},
					},
				},
			},
			wantAllocatable: corev1.ResourceList{
				katalystconsts.ReclaimedResourceMilliCPU: *resource.NewScaledQuantity(3, 3),                 // calculated result
				katalystconsts.ReclaimedResourceMemory:   *resource.NewQuantity(3145728, resource.BinarySI), // calculated result
			},
			wantAvailable: corev1.ResourceList{
				katalystconsts.ReclaimedResourceMilliCPU: *resource.NewScaledQuantity(2, 3),                 // calculated result
				katalystconsts.ReclaimedResourceMemory:   *resource.NewQuantity(2097152, resource.BinarySI), // calculated result
			},
			wantErr: false,
		},
		{
			name: "1 nodes, 1 pod, 2 cnr",
			args: args{
				nodes: []*corev1.Node{newNode("node1")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodRunning,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{
						katalystCNR: fakeUnstructuredLister{
							data: []*unstructured.Unstructured{
								newUnsCNR("node1", corev1.ResourceList{
									katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
									katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
								}),
								newUnsCNR("node2", corev1.ResourceList{
									katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("2k"),
									katalystconsts.ReclaimedResourceMemory:   resource.MustParse("2Mi"),
								}),
							},
						},
					},
				},
			},
			wantAllocatable: corev1.ResourceList{
				katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
				katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
			},
			wantAvailable: corev1.ResourceList{
				katalystconsts.ReclaimedResourceMilliCPU: *resource.NewScaledQuantity(0, 3),           // calculated result
				katalystconsts.ReclaimedResourceMemory:   *resource.NewQuantity(0, resource.BinarySI), // calculated result
			},
			wantErr: false,
		},
		{
			name: "1 nodes, 1 pod failed, 1 pod succeeded, 1 cnr",
			args: args{
				nodes: []*corev1.Node{newNode("node1")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodFailed,
					),
					newPod(
						"pod2",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodSucceeded,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{
						katalystCNR: fakeUnstructuredLister{
							data: []*unstructured.Unstructured{
								newUnsCNR("node1", corev1.ResourceList{
									katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
									katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
								}),
							},
						},
					},
				},
			},
			wantAllocatable: corev1.ResourceList{
				katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
				katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
			},
			wantAvailable: corev1.ResourceList{
				katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
				katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
			},
			wantErr: false,
		},
		{
			name: "1 nodes, 1 pod, 1 cnr without resources",
			args: args{
				nodes: []*corev1.Node{newNode("node1")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodRunning,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{
						katalystCNR: fakeUnstructuredLister{
							data: []*unstructured.Unstructured{
								newUnsCNR("node1", nil),
							},
						},
					},
				},
			},
			wantAllocatable: corev1.ResourceList{},
			wantAvailable:   corev1.ResourceList{},
			wantErr:         false,
		},
		{
			name: "handle not exist",
			args: args{
				nodes: []*corev1.Node{newNode("node1")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodRunning,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{},
				},
			},
			wantAllocatable: nil,
			wantAvailable:   nil,
			wantErr:         false,
		},
		{
			name: "lister failed",
			args: args{
				nodes: []*corev1.Node{newNode("node1")},
				pods: []*corev1.Pod{
					newPod(
						"pod1",
						corev1.ResourceList{
							katalystconsts.ReclaimedResourceMilliCPU: resource.MustParse("1k"),
							katalystconsts.ReclaimedResourceMemory:   resource.MustParse("1Mi"),
						},
						corev1.PodRunning,
					),
				},
				handle: ClusterHandle{
					DynamicLister: map[schema.GroupVersionResource]cache.GenericLister{
						katalystCNR: fakeUnstructuredLister{err: errors.New("lister failed")},
					},
				},
			},
			wantAllocatable: nil,
			wantAvailable:   nil,
			wantErr:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &katalystPlugin{}
			gotAllocatable, gotAvailable, err := k.CollectClusterResources(
				context.Background(),
				tt.args.nodes,
				tt.args.pods,
				tt.args.handle,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("CollectClusterResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotAllocatable, tt.wantAllocatable) {
				t.Errorf("CollectClusterResources() gotAllocatable = %v, want %v", gotAllocatable, tt.wantAllocatable)
			}
			if !reflect.DeepEqual(gotAvailable, tt.wantAvailable) {
				t.Errorf("CollectClusterResources() gotAvailable = %v, want %v", gotAvailable, tt.wantAvailable)
			}
		})
	}
}
