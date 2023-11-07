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

package adoption

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func newFederatedObject(annotations map[string]string) *fedcorev1a1.FederatedObject {
	return &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: annotations,
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Placements: []fedcorev1a1.PlacementWithController{
				{
					Controller: "test-controller",
					Placement: []fedcorev1a1.ClusterReference{
						{Cluster: "kubeadmiral-member-1"},
						{Cluster: "kubeadmiral-member-2"},
						{Cluster: "kubeadmiral-member-3"},
					},
				},
			},
		},
	}
}

func TestFilterToAdoptCluster(t *testing.T) {
	type args struct {
		obj         metav1.Object
		clusterName string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "propagate resource without adopting",
			args: args{
				obj:         newFederatedObject(nil),
				clusterName: "kubeadmiral-member-1",
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "invalid annotation for clusters to adopt",
			args: args{
				obj:         newFederatedObject(map[string]string{ClustersToAdoptAnnotation: "annotation"}),
				clusterName: "kubeadmiral-member-1",
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "cluster not matched",
			args: args{
				obj:         newFederatedObject(map[string]string{ClustersToAdoptAnnotation: `{"clusters": ["kubeadmiral-member-1"]}`}),
				clusterName: "kubeadmiral-member-2",
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "cluster name matched",
			args: args{
				obj:         newFederatedObject(map[string]string{ClustersToAdoptAnnotation: `{"clusters": ["kubeadmiral-member-1"]}`}),
				clusterName: "kubeadmiral-member-1",
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "invalid regexp",
			args: args{
				obj:         newFederatedObject(map[string]string{ClustersToAdoptAnnotation: `{"regexp": "*"}`}),
				clusterName: "kubeadmiral-member-1",
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "regexp matched",
			args: args{
				obj:         newFederatedObject(map[string]string{ClustersToAdoptAnnotation: `{"regexp": ".*"}`}),
				clusterName: "kubeadmiral-member-1",
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "name matched and regexp doesn't match",
			args: args{
				obj: newFederatedObject(map[string]string{
					ClustersToAdoptAnnotation: `{"clusters": ["kubeadmiral-member-1"], "regexp": "test*"}`,
				}),
				clusterName: "kubeadmiral-member-1",
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "regexp matched and name doesn't match",
			args: args{
				obj: newFederatedObject(map[string]string{
					ClustersToAdoptAnnotation: `{"clusters": ["kubeadmiral-member-2"], "regexp": "kubeadmiral*"}`,
				}),
				clusterName: "kubeadmiral-member-1",
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilterToAdoptCluster(tt.args.obj, tt.args.clusterName)
			if (err != nil) != tt.wantErr {
				t.Errorf("FilterToAdoptCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FilterToAdoptCluster() got = %v, want %v", got, tt.want)
			}
		})
	}
}
