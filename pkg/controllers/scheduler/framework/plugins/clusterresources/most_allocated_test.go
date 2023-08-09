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

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func TestClusterResourcesMostAllocated(t *testing.T) {
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
			// Cluster1 Score: (0 + 0) / 2 = 0
			// Cluster2 scores (remaining resources) on 0-100 scale
			// CPU Fraction: 0 / 4000 = 0 %
			// Memory Fraction: 0 / 10000 = 0%
			// Cluster2 Score: (0 + 0) / 2 = 0
			su: makeSchedulingUnit("su1", 0, 0),
			clusters: []*fedcorev1a1.FederatedCluster{
				makeCluster("cluster1", 4000, 10000, 4000, 10000),
				makeCluster("cluster2", 4000, 10000, 4000, 10000),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: makeCluster("cluster1", 4000, 10000, 4000, 10000), Score: 0},
				{Cluster: makeCluster("cluster2", 4000, 10000, 4000, 10000), Score: 0},
			},
			name: "nothing scheduled, nothing requested",
		},
		{
			// Cluster1 scores on 0-100 scale
			// CPU Fraction: 3000 / 4000= 75%
			// Memory Fraction: 5000 / 10000 = 50%
			// Cluster1 Score: (75 + 50) / 2 = 62
			// Cluster2 scores on 0-100 scale
			// CPU Fraction: 3000 / 6000 = 50%
			// Memory Fraction: 5000 / 10000 = 50%
			// Cluster2 Score: (50 + 50) / 2 = 50
			su: makeSchedulingUnit("su2", 3000, 5000),
			clusters: []*fedcorev1a1.FederatedCluster{
				makeCluster("cluster1", 4000, 10000, 4000, 10000),
				makeCluster("cluster2", 6000, 10000, 6000, 10000),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: makeCluster("cluster1", 4000, 10000, 4000, 10000), Score: 62},
				{Cluster: makeCluster("cluster2", 6000, 10000, 6000, 10000), Score: 50},
			},
			name: "nothing scheduled, resources requested, differently sized machines",
		},
		{
			// Cluster1 scores on 0-100 scale
			// CPU Fraction: 3000 / 4000= 75%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 5000 / 10000 = 50%
			// Cluster1 Score: (75 + 50 + 50 * 4) / 6 = 54
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
				{Cluster: clusterWithGPU(makeCluster("cluster1", 4000, 10000, 4000, 10000), 10000, 10000), Score: 54},
				{Cluster: clusterWithGPU(makeCluster("cluster2", 6000, 10000, 6000, 10000), 10000, 10000), Score: 50},
			},
			name: "nothing scheduled, resources requested with gpu, differently sized machines",
		},
		{
			// Cluster1 scores on 0-100 scale
			// CPU Fraction: 6000 / 10000 = 60%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 4000 / 10000 = 40%
			// Cluster1 Score: (60 + 50 + 40 * 4) / 6 = 45
			// Cluster2 scores on 0-100 scale
			// CPU Fraction: 6000 / 15000 = 40%
			// Memory Fraction: 5000 / 10000 = 50%
			// GPU Fraction: 4000 / 6600 = 60%
			// Cluster2 Score: (40 + 50 + 60 * 4) / 6 = 55
			su: schedulingUnitWithGPU(makeSchedulingUnit("su4", 6000, 5000), 4000),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithGPU(makeCluster("cluster1", 10000, 10000, 10000, 10000), 10000, 10000),
				clusterWithGPU(makeCluster("cluster2", 15000, 10000, 15000, 10000), 6600, 6600),
			},
			expectedList: []framework.ClusterScore{
				{Cluster: clusterWithGPU(makeCluster("cluster1", 10000, 10000, 10000, 10000), 10000, 10000), Score: 45},
				{Cluster: clusterWithGPU(makeCluster("cluster2", 15000, 10000, 15000, 10000), 6600, 6600), Score: 55},
			},
			name: "nothing scheduled, resources requested with gpu, differently sized machines, gpu fraction differs",
		},
	}

	p, _ := NewClusterResourcesMostAllocated(nil)
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
