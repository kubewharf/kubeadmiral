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

package maxcluster

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func makeCluster(clusterName string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
	}
}

func newIntP64(in int64) *int64 {
	return &in
}

func TestMaxClusterSelectClusters(t *testing.T) {
	tests := []struct {
		name             string
		su               *framework.SchedulingUnit
		clusterScoreList framework.ClusterScoreList
		expectedCluster  []string
		expectedResult   *framework.Result
	}{
		{
			name: "1 cluster, duplicate scheduling mode, nil replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
			},
			expectedCluster: []string{"foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, duplicate scheduling mode, nil replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, duplicate scheduling mode, nil replicas, same score",
			su: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("abc"),
					Score:   2,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"abc", "fun"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "1 cluster, divide scheduling mode, 5 replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(5),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
			},
			expectedCluster: []string{"foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, 5 replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(5),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, nil replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDivide,
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, 10 replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(10),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, 11 replicas",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(11),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, 5 replicas, 3 max clusters",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(5),
				MaxClusters:     pointer.Int64(3),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, 11 replicas, 3 max clusters",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(11),
				MaxClusters:     pointer.Int64(3),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun", "foo"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "2 clusters, divide scheduling mode, 11 replicas, 1 max cluster",
			su: &framework.SchedulingUnit{
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(11),
				MaxClusters:     pointer.Int64(1),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
				{
					Cluster: makeCluster("fun"),
					Score:   2,
				},
			},
			expectedCluster: []string{"fun"},
			expectedResult:  framework.NewResult(framework.Success),
		},
		{
			name: "cluster with invalid max clusters",
			su: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDivide,
				MaxClusters:    newIntP64(int64(-1)),
			},
			expectedCluster: []string{},
			expectedResult:  framework.NewResult(framework.Unschedulable, MaxClusterErrReason),
		},
		{
			name: "1 cluster, 0 max clusters",
			su: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDivide,
				MaxClusters:    pointer.Int64(0),
			},
			clusterScoreList: framework.ClusterScoreList{
				{
					Cluster: makeCluster("foo"),
					Score:   1,
				},
			},
			expectedCluster: []string{},
			expectedResult:  framework.NewResult(framework.Success),
		},
	}

	p, _ := NewMaxCluster(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotList, result := p.(framework.SelectPlugin).SelectClusters(context.TODO(), test.su, test.clusterScoreList)
			if !reflect.DeepEqual(result, test.expectedResult) {
				t.Errorf("status does not match: %v, want: %v", result, test.expectedResult)
			}
			if len(gotList) != len(test.expectedCluster) {
				t.Errorf("expected select cluster length does not match: %d, want: %d", len(gotList), len(test.expectedCluster))
			}
			for i := range gotList {
				if gotList[i].Cluster.Name != test.expectedCluster[i] {
					t.Errorf("Unexpected select cluster: %s, want: %s", gotList[i].Cluster.Name, test.expectedCluster[i])
				}
			}
		})
	}
}
