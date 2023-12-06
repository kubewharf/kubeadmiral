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
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func genFederatedCluster(name string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func TestGetPossibleClusters(t *testing.T) {
	longName := strings.Repeat("abcde12345", 24)
	type args struct {
		clusters   []*fedcorev1a1.FederatedCluster
		targetName string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "one cluster matched",
			args: args{
				clusters:   []*fedcorev1a1.FederatedCluster{genFederatedCluster("foo")},
				targetName: "foo-1",
			},
			want: []string{"foo"},
		},
		{
			name: "two clusters matched with long name",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					genFederatedCluster(longName + "1234567"),
					genFederatedCluster(longName + "123abcd"),
				},
				targetName: longName + "1234567890",
			},
			want: []string{longName + "1234567", longName + "123abcd"},
		},
		{
			name: "one cluster matched with long name",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					genFederatedCluster(longName + "1234567"),
					genFederatedCluster(longName + "1abcdef"),
				},
				targetName: longName + "1234567890",
			},
			want: []string{longName + "1234567"},
		},
		{
			name: "no cluster matched with long name",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					genFederatedCluster(longName + "1234567"),
				},
				targetName: longName + "123",
			},
			want: []string{},
		},
		{
			name: "no cluster matched",
			args: args{
				clusters:   []*fedcorev1a1.FederatedCluster{genFederatedCluster("foo")},
				targetName: "bar",
			},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetPossibleClusters(tt.args.clusters, tt.args.targetName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetPossibleClusters() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMakePodUniquea(t *testing.T) {
	clusterName := "cluster"
	podName := "pod"
	nodeName := "node"

	pod := &corev1.Pod{}
	pod.SetName(podName)
	MakePodUnique(pod, clusterName)
	assert.Equal(t, GenUniqueName(clusterName, podName), pod.GetName())
	assert.Equal(t, clusterName, pod.GetAnnotations()[ClusterNameAnnotationKey])
	assert.Equal(t, podName, pod.GetAnnotations()[RawNameAnnotationKey])
	assert.Equal(t, "", pod.Spec.NodeName)

	pod = &corev1.Pod{}
	pod.SetName(podName)
	pod.Spec.NodeName = nodeName
	MakePodUnique(pod, clusterName)
	assert.Equal(t, GenUniqueName(clusterName, podName), pod.GetName())
	assert.Equal(t, clusterName, pod.GetAnnotations()[ClusterNameAnnotationKey])
	assert.Equal(t, podName, pod.GetAnnotations()[RawNameAnnotationKey])
	assert.Equal(t, GenUniqueName(clusterName, nodeName), pod.Spec.NodeName)
}

func TestMakeObjectUniquea(t *testing.T) {
	clusterName := "cluster"
	obj := &corev1.ConfigMap{}
	obj.SetName("cm")

	MakeObjectUnique(obj, clusterName)
	assert.Equal(t, GenUniqueName(clusterName, "cm"), obj.GetName())
	assert.Equal(t, clusterName, obj.GetAnnotations()[ClusterNameAnnotationKey])
	assert.Equal(t, "cm", obj.GetAnnotations()[RawNameAnnotationKey])
}
