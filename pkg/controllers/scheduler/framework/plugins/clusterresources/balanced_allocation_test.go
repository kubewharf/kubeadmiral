/*
Copyright 2019 The Kubernetes Authors.

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

package clusterresources

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func makeCluster(
	clusterName string,
	allocatableMilliCPU, allocatableMemory, availableMilliCPU, availableMemory int64,
) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Status: fedcorev1a1.FederatedClusterStatus{
			Resources: fedcorev1a1.Resources{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(allocatableMilliCPU, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(allocatableMemory, resource.BinarySI),
				},
				Available: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(availableMilliCPU, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(availableMemory, resource.BinarySI),
				},
			},
		},
	}
}

func makeSchedulingUnit(suName string, requestMilliCPU, requestMemory int64) *framework.SchedulingUnit {
	return &framework.SchedulingUnit{
		Name: suName,
		ResourceRequest: framework.Resource{
			MilliCPU: requestMilliCPU,
			Memory:   requestMemory,
		},
	}
}

func TestClusterResourcesBalancedAllocation(t *testing.T) {
	tests := []struct {
		name         string
		su           *framework.SchedulingUnit
		clusters     []*fedcorev1a1.FederatedCluster
		expectedList framework.ClusterScoreList
	}{
		{
			// Cluster1 scores (remaining resources) on 0-10 scale
			// CPU Fraction: 0 / 4000 = 0%
			// Memory Fraction: 0 / 10000 = 0%
			// Cluster1 Score: 10 - (0-0)*100 = 100
			// Cluster2 scores (remaining resources) on 0-10 scale
			// CPU Fraction: 0 / 4000 = 0 %
			// Memory Fraction: 0 / 10000 = 0%
			// Cluster2 Score: 10 - (0-0)*100 = 100
			su: makeSchedulingUnit("su1", 0, 0),
			clusters: []*fedcorev1a1.FederatedCluster{
				makeCluster("cluster1", 4000, 10000, 4000, 10000),
				makeCluster("cluster2", 4000, 10000, 4000, 10000),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: makeCluster("cluster1", 4000, 10000, 4000, 10000), Score: framework.MaxClusterScore},
				{Cluster: makeCluster("cluster2", 4000, 10000, 4000, 10000), Score: framework.MaxClusterScore},
			},
			name: "nothing scheduled, nothing requested",
		},
		{
			// Cluster1 scores on 0-10 scale
			// CPU Fraction: 3000 / 4000= 75%
			// Memory Fraction: 5000 / 10000 = 50%
			// Cluster1 Score: 10 - (0.75-0.5)*100 = 75
			// Cluster2 scores on 0-10 scale
			// CPU Fraction: 3000 / 6000= 50%
			// Memory Fraction: 5000/10000 = 50%
			// Cluster2 Score: 10 - (0.5-0.5)*100 = 100
			su: makeSchedulingUnit("su2", 3000, 5000),
			clusters: []*fedcorev1a1.FederatedCluster{
				makeCluster("cluster1", 4000, 10000, 4000, 10000),
				makeCluster("cluster2", 6000, 10000, 6000, 10000),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: makeCluster("cluster1", 4000, 10000, 4000, 10000), Score: 75},
				{Cluster: makeCluster("cluster2", 6000, 10000, 6000, 10000), Score: framework.MaxClusterScore},
			},
			name: "nothing scheduled, resources requested, differently sized machines",
		},
	}

	p, _ := NewClusterResourcesBalancedAllocation(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotList := framework.ClusterScoreList{}
			for _, cluster := range test.clusters {
				score, result := p.(framework.ScorePlugin).Score(context.TODO(), test.su, cluster)
				if !result.IsSuccess() {
					t.Errorf("unexpected error: %v", result.AsError())
				}
				gotList = append(gotList, framework.ClusterScore{
					Cluster: cluster,
					Score:   score,
				})
			}

			for i := 0; i < len(test.expectedList); i++ {
				if test.expectedList[i].Cluster.Name != gotList[i].Cluster.Name {
					t.Errorf("expected:\n\t%s,\ngot:\n\t%s",
						test.expectedList[i].Cluster.Name, gotList[i].Cluster.Name)
				}
				if test.expectedList[i].Score != gotList[i].Score {
					t.Errorf("expected:\n\t%d,\ngot:\n\t%d",
						test.expectedList[i].Score, gotList[i].Score)
				}
			}
		})
	}
}
