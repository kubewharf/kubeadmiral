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

	"k8s.io/apimachinery/pkg/api/resource"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func schedulingUnitWithGPU(su *framework.SchedulingUnit, value int64) *framework.SchedulingUnit {
	su.ResourceRequest.SetScalar(framework.ResourceGPU, value)
	return su
}

func clusterWithGPU(fc *fedcorev1a1.FederatedCluster, allocatable, available int64) *fedcorev1a1.FederatedCluster {
	fc.Status.Resources.Allocatable[framework.ResourceGPU] = *resource.NewQuantity(allocatable, resource.BinarySI)
	fc.Status.Resources.Available[framework.ResourceGPU] = *resource.NewQuantity(available, resource.BinarySI)
	return fc
}

func TestClusterResourcesLeastAllocated(t *testing.T) {
	tests := []struct {
		name         string
		su           *framework.SchedulingUnit
		clusters     []*fedcorev1a1.FederatedCluster
		expectedList framework.ClusterScoreList
	}{
		{
			// Cluster1 scores (remaining resources) on 0-100 scale
			// CPU Fraction: 0 / 4000 = 0%
			// Memory Fraction: 0 / 10000 = 0%
			// Cluster1 Score: (100 + 100)/ 2 = 100
			// Cluster2 scores (remaining resources) on 0-100 scale
			// CPU Fraction: 0 / 4000 = 0 %
			// Memory Fraction: 0 / 10000 = 0%
			// Cluster2 Score: (100 + 100) / 2 = 100
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
			// Cluster1 scores on 0-100 scale
			// CPU Fraction: 3000 / 4000= 75%
			// Memory Fraction: 5000 / 10000 = 50%
			// Cluster1 Score: (25 + 50) / 2 = 37
			// Cluster2 scores on 0-100 scale
			// CPU Fraction: 3000 / 6000 = 50%
			// Memory Fraction: 5000 / 10000 = 50%
			// Cluster2 Score: (50 + 50) / 2  = 50
			su: makeSchedulingUnit("su2", 3000, 5000),
			clusters: []*fedcorev1a1.FederatedCluster{
				makeCluster("cluster1", 4000, 10000, 4000, 10000),
				makeCluster("cluster2", 6000, 10000, 6000, 10000),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: makeCluster("cluster1", 4000, 10000, 4000, 10000), Score: 37},
				{Cluster: makeCluster("cluster2", 6000, 10000, 6000, 10000), Score: 50},
			},
			name: "nothing scheduled, resources requested, differently sized machines",
		},
		{
			// Cluster1 scores on 0-100 scale
			// CPU Fraction: 3000 / 4000= 75%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 5000 / 10000 = 50%
			// Cluster1 Score: (25 + 50 + 50 * 4) / 6 = 45
			// Cluster2 scores on 0-100 scale
			// CPU Fraction: 3000 / 6000 = 50%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 5000 / 10000 = 50%
			// Cluster2 Score: (50 + 50 + 50 * 4) / 6 = 50
			su: schedulingUnitWithGPU(makeSchedulingUnit("su3", 3000, 5000), 5000),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithGPU(makeCluster("cluster1", 4000, 10000, 4000, 10000), 10000, 10000),
				clusterWithGPU(makeCluster("cluster2", 6000, 10000, 6000, 10000), 10000, 10000),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: clusterWithGPU(makeCluster("cluster1", 4000, 10000, 4000, 10000), 10000, 10000), Score: 45},
				{Cluster: clusterWithGPU(makeCluster("cluster2", 6000, 10000, 6000, 10000), 10000, 10000), Score: 50},
			},
			name: "nothing scheduled, resources requested with gpu, differently sized machines",
		},
		{
			// Cluster1 scores on 0-100 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 4000 / 10000 = 40%
			// Cluster1 Score: (40 + 50 + 60 * 4) / 6 = 55
			// Cluster2 scores on 0-100 scale
			// CPU Fraction: 6000 / 15000 = 40%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 4000 / 6700 = 60%
			// Cluster2 Score: (60 + 50 + 40 * 4) / 6 = 45
			su: schedulingUnitWithGPU(makeSchedulingUnit("su4", 6000, 5000), 4000),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithGPU(makeCluster("cluster1", 10000, 10000, 10000, 10000), 10000, 10000),
				clusterWithGPU(makeCluster("cluster2", 15000, 10000, 15000, 10000), 6700, 6700),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: clusterWithGPU(makeCluster("cluster1", 10000, 10000, 10000, 10000), 10000, 10000), Score: 55},
				{Cluster: clusterWithGPU(makeCluster("cluster2", 15000, 10000, 15000, 10000), 6700, 6700), Score: 45},
			},
			name: "nothing scheduled, resources requested with gpu, differently sized machines, gpu fraction differs",
		},
	}

	p, _ := NewClusterResourcesLeastAllocated(nil)
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
