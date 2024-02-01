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

package katalyst

import (
	"context"
	"reflect"
	"testing"

	katalystconsts "github.com/kubewharf/katalyst-api/pkg/consts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func TestKatalystResourcesExist_Filter(t *testing.T) {
	type args struct {
		su      *framework.SchedulingUnit
		cluster *fedcorev1a1.FederatedCluster
	}
	tests := []struct {
		name string
		args args
		want *framework.Result
	}{
		{
			name: "cluster scheduled",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{"cluster1": pointer.Int64(1)},
				},
				cluster: &fedcorev1a1.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster1"},
				},
			},
			want: framework.NewResult(framework.Success),
		},
		{
			name: "cluster not scheduled, su without katalyst resources",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{"cluster1": pointer.Int64(1)},
				},
				cluster: &fedcorev1a1.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster2",
					},
				},
			},
			want: framework.NewResult(framework.Success),
		},
		{
			name: "cluster not scheduled, su with katalyst, cluster with katalyst",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{"cluster1": pointer.Int64(1)},
					ResourceRequest: framework.Resource{
						ScalarResources: map[corev1.ResourceName]int64{katalystconsts.ReclaimedResourceMilliCPU: 1},
					},
				},
				cluster: &fedcorev1a1.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster2",
					},
					Status: fedcorev1a1.FederatedClusterStatus{
						Resources: fedcorev1a1.Resources{
							Allocatable: corev1.ResourceList{
								katalystconsts.ReclaimedResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
			want: framework.NewResult(framework.Success),
		},
		{
			name: "cluster not scheduled, su with katalyst, cluster without katalyst",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{"cluster1": pointer.Int64(1)},
					ResourceRequest: framework.Resource{
						ScalarResources: map[corev1.ResourceName]int64{katalystconsts.ReclaimedResourceMilliCPU: 1},
					},
				},
				cluster: &fedcorev1a1.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster2",
					},
					Status: fedcorev1a1.FederatedClusterStatus{
						Resources: fedcorev1a1.Resources{
							Allocatable: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
			want: framework.NewResult(framework.Unschedulable, "Katalyst resource does not exist"),
		},
		{
			name: "cluster is nil",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{"cluster1": pointer.Int64(1)},
				},
				cluster: nil,
			},
			want: framework.NewResult(framework.Error, "invalid federated cluster"),
		},
		{
			name: "su is nil",
			args: args{
				su:      nil,
				cluster: nil,
			},
			want: framework.NewResult(framework.Error, "invalid scheduling unit"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := &KatalystResourcesExist{}
			if got := pl.Filter(context.Background(), tt.args.su, tt.args.cluster); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}
