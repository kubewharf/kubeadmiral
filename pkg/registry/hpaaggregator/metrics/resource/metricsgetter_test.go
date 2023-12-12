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

package resource

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/metrics"
	metricsinstall "k8s.io/metrics/pkg/apis/metrics/install"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	fakeinformermanager "github.com/kubewharf/kubeadmiral/pkg/util/informermanager/fake"
)

func Test_metricsGetter_GetNodeMetrics(t *testing.T) {
	clusters, clients := newClusters(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters:              clusters,
		ReadyClusterDynamicClients: clients,
	}
	nodes := make([]*corev1.Node, 3)
	nodeMetrics := make([]metrics.NodeMetrics, 3)
	for i := range nodes {
		cluster := fmt.Sprintf("cluster-%d", i)
		n, m := newNode(cluster)

		clusterobject.MakeObjectUnique(n, cluster)
		nodes[i] = n

		result := metrics.NodeMetrics{}
		clusterobject.MakeObjectUnique(m, cluster)
		_ = metricsv1beta1.Convert_v1beta1_NodeMetrics_To_metrics_NodeMetrics(m, &result, nil)
		nodeMetrics[i] = result
	}

	type fields struct {
		federatedInformerManager informermanager.FederatedInformerManager
		logger                   klog.Logger
	}
	type args struct {
		nodes []*corev1.Node
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []metrics.NodeMetrics
		wantErr bool
	}{
		{
			name: "3 clusters, 3 nodes",
			fields: fields{
				federatedInformerManager: informer,
				logger:                   klog.Background(),
			},
			args: args{
				nodes: nodes,
			},
			want:    nodeMetrics,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metricsGetter{
				federatedInformerManager: tt.fields.federatedInformerManager,
				logger:                   tt.fields.logger,
			}
			got, err := m.GetNodeMetrics(tt.args.nodes...)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetNodeMetrics() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			sort.Slice(got, func(i, j int) bool {
				return got[i].Name < got[j].Name
			})
			sort.Slice(tt.want, func(i, j int) bool {
				return tt.want[i].Name < tt.want[j].Name
			})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetNodeMetrics() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_metricsGetter_GetPodMetrics(t *testing.T) {
	clusters, clients := newClusters(3)
	informer := &fakeinformermanager.FakeFederatedInformerManager{
		ReadyClusters:              clusters,
		ReadyClusterDynamicClients: clients,
	}
	pods := make([]*metav1.PartialObjectMetadata, 3)
	podMetrics := make([]metrics.PodMetrics, 3)
	for i := range pods {
		cluster := fmt.Sprintf("cluster-%d", i)
		p, m := newPod("default", cluster)

		clusterobject.MakePodUnique(p, cluster)
		pods[i] = meta.AsPartialObjectMetadata(p)

		result := metrics.PodMetrics{}
		clusterobject.MakeObjectUnique(m, cluster)
		_ = metricsv1beta1.Convert_v1beta1_PodMetrics_To_metrics_PodMetrics(m, &result, nil)
		podMetrics[i] = result
	}

	type fields struct {
		federatedInformerManager informermanager.FederatedInformerManager
		logger                   klog.Logger
	}
	type args struct {
		pods []*metav1.PartialObjectMetadata
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []metrics.PodMetrics
		wantErr bool
	}{
		{
			name: "3 clusters, 3 pods",
			fields: fields{
				federatedInformerManager: informer,
				logger:                   klog.Background(),
			},
			args: args{
				pods: pods,
			},
			want:    podMetrics,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetricsGetter(tt.fields.federatedInformerManager, tt.fields.logger)
			got, err := m.GetPodMetrics(tt.args.pods...)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetPodMetrics() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			sort.Slice(got, func(i, j int) bool {
				return got[i].Name < got[j].Name
			})
			sort.Slice(tt.want, func(i, j int) bool {
				return tt.want[i].Name < tt.want[j].Name
			})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetPodMetrics() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_metricsConverter(t *testing.T) {
	type args struct {
		uns         *unstructured.Unstructured
		clusterName string
	}
	tests := []struct {
		name     string
		args     args
		wantNode metrics.NodeMetrics
		wantPod  metrics.PodMetrics
		wantErr  bool
	}{
		{
			name: "normal",
			args: args{
				uns:         &unstructured.Unstructured{Object: map[string]interface{}{"window": "10s"}},
				clusterName: "cluster1",
			},
			wantNode: metrics.NodeMetrics{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1-",
					Annotations: map[string]string{
						clusterobject.ClusterNameAnnotationKey: "cluster1",
						clusterobject.RawNameAnnotationKey:     "",
					},
				},
				Window: metav1.Duration{Duration: 10 * time.Second},
			},
			wantPod: metrics.PodMetrics{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1-",
					Annotations: map[string]string{
						clusterobject.ClusterNameAnnotationKey: "cluster1",
						clusterobject.RawNameAnnotationKey:     "",
					},
				},
				Window: metav1.Duration{Duration: 10 * time.Second},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := nodeMetricsConverter(tt.args.uns, tt.args.clusterName)
			if (err != nil) != tt.wantErr {
				t.Errorf("nodeMetricsConverter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(node, tt.wantNode) {
				t.Errorf("nodeMetricsConverter() got = %v, want %v", node, tt.wantNode)
			}

			pod, err := podMetricsConverter(tt.args.uns, tt.args.clusterName)
			if (err != nil) != tt.wantErr {
				t.Errorf("podMetricsConverter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(pod, tt.wantPod) {
				t.Errorf("podMetricsConverter() got = %v, want %v", pod, tt.wantPod)
			}
		})
	}
}

func newClusters(num int) ([]*fedcorev1a1.FederatedCluster, map[string]dynamic.Interface) {
	clusters := make([]*fedcorev1a1.FederatedCluster, 0, num)
	clients := make(map[string]dynamic.Interface)

	s := runtime.NewScheme()
	_ = k8sscheme.AddToScheme(s)
	metricsinstall.Install(s)

	for i := 0; i < num; i++ {
		name := fmt.Sprintf("cluster-%d", i)
		clusters = append(clusters, &fedcorev1a1.FederatedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		})

		pod, podM := newPod("default", name)
		node, nodeM := newNode(name)
		client := dynamicfake.NewSimpleDynamicClient(s, pod, node, podM, nodeM)
		clients[name] = client
	}

	return clusters, clients
}

func newPod(namespace, name string) (*corev1.Pod, *metricsv1beta1.PodMetrics) {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  name,
					Image: "nginx:alpine",
				},
			},
		},
	}

	podMetrics := &metricsv1beta1.PodMetrics{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: podMetricsGVR.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Containers: []metricsv1beta1.ContainerMetrics{
			{
				Name:  name,
				Usage: map[corev1.ResourceName]resource.Quantity{"cpu": resource.MustParse("1")},
			},
		},
	}

	return pod, podMetrics
}

func newNode(name string) (*corev1.Node, *metricsv1beta1.NodeMetrics) {
	node := &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	nodeMetrics := &metricsv1beta1.NodeMetrics{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: nodeMetricsGVR.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Usage: map[corev1.ResourceName]resource.Quantity{"cpu": resource.MustParse("1")},
	}

	return node, nodeMetrics
}
