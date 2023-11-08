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

package scheduler

import (
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func Test_updateMaxReplicasForTerminatingCluster(t *testing.T) {
	type args struct {
		su       *framework.SchedulingUnit
		clusters []*fedcorev1a1.FederatedCluster
	}
	tests := []struct {
		name            string
		args            args
		wantMaxReplicas map[string]int64
	}{
		{
			name: "clusters is nil",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{
						"cluster1": pointer.Int64(10),
						"cluster2": pointer.Int64(5),
					},
				},
				clusters: nil,
			},
			wantMaxReplicas: nil,
		},
		{
			name: "no terminating cluster",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{
						"cluster1": pointer.Int64(10),
						"cluster2": pointer.Int64(5),
					},
					MaxReplicas: map[string]int64{
						"cluster1": 1,
						"cluster2": 2,
					},
				},
				clusters: []*fedcorev1a1.FederatedCluster{
					{ObjectMeta: metav1.ObjectMeta{Name: "cluster1"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "cluster2"}},
				},
			},
			wantMaxReplicas: map[string]int64{
				"cluster1": 1,
				"cluster2": 2,
			},
		},
		{
			name: "maxReplicas changed for terminating clusters",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{
						"cluster1": pointer.Int64(1),
						"cluster2": pointer.Int64(2),
					},
					MaxReplicas: map[string]int64{
						"cluster1": 10,
					},
				},
				clusters: []*fedcorev1a1.FederatedCluster{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "cluster1",
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "cluster2",
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
						},
					},
				},
			},
			wantMaxReplicas: map[string]int64{
				"cluster1": 1,
				"cluster2": 2,
			},
		},
		{
			name: "maxReplicas added for terminating clusters",
			args: args{
				su: &framework.SchedulingUnit{
					CurrentClusters: map[string]*int64{
						"cluster2": pointer.Int64(2),
						"cluster3": pointer.Int64(2),
					},
				},
				clusters: []*fedcorev1a1.FederatedCluster{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "cluster1",
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "cluster2",
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "cluster3",
						},
					},
				},
			},
			wantMaxReplicas: map[string]int64{
				"cluster1": 0,
				"cluster2": 2,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateMaxReplicasForTerminatingCluster(tt.args.su, tt.args.clusters)
			if !reflect.DeepEqual(tt.args.su.MaxReplicas, tt.wantMaxReplicas) {
				t.Errorf("updateMaxReplicasForTerminatingCluster() got = %v, want %v",
					tt.args.su.MaxReplicas, tt.wantMaxReplicas)
			}
		})
	}
}
