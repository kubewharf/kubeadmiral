/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package clusterevicted

import (
	"context"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

func makeCluster(clusterName string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
	}
}

func TestName(t *testing.T) {
	p, _ := NewClusterEvicted(nil)
	if name := p.Name(); !reflect.DeepEqual(name, names.ClusterEvicted) {
		t.Errorf("Expected name %s, but got %s", names.ClusterEvicted, name)
	}
}

func TestClusterEvictedPlugin(t *testing.T) {
	tests := []struct {
		name           string
		su             *framework.SchedulingUnit
		cluster        *fedcorev1a1.FederatedCluster
		expectedResult *framework.Result
	}{
		{
			name:           "su is nil",
			su:             nil,
			cluster:        makeCluster("cluster1"),
			expectedResult: framework.NewResult(framework.Error),
		},
		{
			"cluster should not be filtered when custom migration info is empty",
			&framework.SchedulingUnit{
				CustomMigration: framework.CustomMigrationSpec{
					Info: nil,
				},
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Success),
		},
		{
			"cluster should not be filtered when name not in custom migration info",
			&framework.SchedulingUnit{
				CustomMigration: framework.CustomMigrationSpec{
					Info: &framework.CustomMigrationInfo{
						UnavailableClusters: []framework.UnavailableCluster{
							{
								Cluster: "cluster2",
							},
						},
					},
				},
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Success),
		},
		{
			"cluster should not be filtered when replica migration in custom migration info",
			&framework.SchedulingUnit{
				CustomMigration: framework.CustomMigrationSpec{
					Info: &framework.CustomMigrationInfo{
						LimitedCapacity: map[string]int64{
							"cluster1": 1,
						},
					},
				},
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Success),
		},
		{
			name: "cluster should not be filtered when time has expired",
			su: &framework.SchedulingUnit{
				CustomMigration: framework.CustomMigrationSpec{
					Info: &framework.CustomMigrationInfo{
						UnavailableClusters: []framework.UnavailableCluster{
							{
								Cluster: "cluster1",
								ValidUntil: metav1.Time{
									Time: time.Now().Add(-1 * time.Hour),
								},
							},
						},
					},
				},
			},
			cluster:        makeCluster("cluster1"),
			expectedResult: framework.NewResult(framework.Success),
		},
		{
			name: "cluster should be filtered",
			su: &framework.SchedulingUnit{
				CustomMigration: framework.CustomMigrationSpec{
					Info: &framework.CustomMigrationInfo{
						UnavailableClusters: []framework.UnavailableCluster{
							{
								Cluster: "cluster1",
								ValidUntil: metav1.Time{
									Time: time.Now().Add(1 * time.Hour),
								},
							},
						},
					},
				},
			},
			cluster:        makeCluster("cluster1"),
			expectedResult: framework.NewResult(framework.Unschedulable),
		},
	}

	p, _ := NewClusterEvicted(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := p.(framework.FilterPlugin).Filter(context.TODO(), test.su, test.cluster)
			if result.IsSuccess() != test.expectedResult.IsSuccess() {
				t.Errorf("result does not match: %v, want %v", result, test.expectedResult)
			}
		})
	}
}
