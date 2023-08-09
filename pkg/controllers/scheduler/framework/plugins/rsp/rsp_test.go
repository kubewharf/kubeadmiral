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

package rsp

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func NewFederatedCluster(name string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func TestExtractClusterNames(t *testing.T) {
	clusters := []*fedcorev1a1.FederatedCluster{}
	names := []string{"foo", "bar"}
	for _, name := range names {
		clusters = append(clusters, NewFederatedCluster(name))
	}
	ret := ExtractClusterNames(clusters)
	assert.Equal(t, len(ret), len(names), "the length should be the same.")
	for i := range ret {
		assert.Equal(t, ret[i], names[i], "the name should be the same.")
	}
}

func makeClusterWithCPU(name string, allocatable, available int) *fedcorev1a1.FederatedCluster {
	cluster := &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if allocatable >= 0 && available >= 0 {
		cluster.Status.Resources = fedcorev1a1.Resources{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(strconv.Itoa(allocatable)),
			},
			Available: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(strconv.Itoa(available)),
			},
		}
	}
	return cluster
}

func TestCalcWeightLimit(t *testing.T) {
	type args struct {
		clusters         []*fedcorev1a1.FederatedCluster
		supplyLimitRatio float64
	}
	tests := []struct {
		name            string
		args            args
		wantWeightLimit map[string]int64
		wantErr         assert.ErrorAssertionFunc
	}{
		{
			name: "two clusters have the same resource",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					makeClusterWithCPU("cluster1", 100, 0),
					makeClusterWithCPU("cluster2", 100, 0),
				},
				supplyLimitRatio: 1.0,
			},
			wantWeightLimit: map[string]int64{
				"cluster1": int64(500),
				"cluster2": int64(500),
			},
			wantErr: assert.NoError,
		},
		{
			name: "3 clusters have different resource amount",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					makeClusterWithCPU("cluster1", 3000, 0),
					makeClusterWithCPU("cluster2", 4000, 0),
					makeClusterWithCPU("cluster3", 3000, 0),
				},
				supplyLimitRatio: 1.0,
			},
			wantWeightLimit: map[string]int64{
				"cluster1": int64(300),
				"cluster2": int64(400),
				"cluster3": int64(300),
			},
			wantErr: assert.NoError,
		},
		{
			name: "1 cluster node level info missing",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					makeClusterWithCPU("cluster1", 3000, -1),
					makeClusterWithCPU("cluster2", 7000, 0),
					makeClusterWithCPU("cluster3", 3000, 0),
				},
				supplyLimitRatio: 1.0,
			},
			wantWeightLimit: map[string]int64{
				"cluster1": int64(0),
				"cluster2": int64(700),
				"cluster3": int64(300),
			},
			wantErr: assert.NoError,
		},
		{
			name: "all clusters node level info missing",
			args: args{
				clusters: []*fedcorev1a1.FederatedCluster{
					makeClusterWithCPU("cluster1", 3000, -1),
					makeClusterWithCPU("cluster2", 7000, -1),
					makeClusterWithCPU("cluster3", 3000, -1),
				},
				supplyLimitRatio: 1.0,
			},
			wantWeightLimit: map[string]int64{
				"cluster1": int64(333),
				"cluster2": int64(333),
				"cluster3": int64(333),
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWeightLimit, err := CalcWeightLimit(tt.args.clusters, corev1.ResourceCPU, tt.args.supplyLimitRatio)
			if !tt.wantErr(t, err, fmt.Sprintf("CalcWeightLimit(%v)", tt.args.clusters)) {
				return
			}
			assert.Equalf(t, tt.wantWeightLimit, gotWeightLimit, "CalcWeightLimit(%v)", tt.args.clusters)
		})
	}
}

func TestAvailableToPercentage(t *testing.T) {
	type args struct {
		clusterAvailables map[string]corev1.ResourceList
		weightLimit       map[string]int64
	}
	makeArgs := func(clusters ...*fedcorev1a1.FederatedCluster) args {
		return args{
			clusterAvailables: QueryAvailable(clusters),
			weightLimit: func() map[string]int64 {
				weightLimit, _ := CalcWeightLimit(clusters, corev1.ResourceCPU, 1.0)
				return weightLimit
			}(),
		}
	}
	tests := []struct {
		name               string
		args               args
		wantClusterWeights map[string]int64
		wantErr            assert.ErrorAssertionFunc
	}{
		{
			name: "test#1",
			args: makeArgs(
				makeClusterWithCPU("cluster1", 100, 50),
				makeClusterWithCPU("cluster2", 100, 50),
			),
			wantClusterWeights: map[string]int64{
				"cluster1": int64(500),
				"cluster2": int64(500),
			},
			wantErr: assert.NoError,
		},
		{
			name: "test#2",
			args: makeArgs(
				makeClusterWithCPU("cluster1", 100, 40),
				makeClusterWithCPU("cluster2", 100, 10),
			),
			// limit: 500:500, tmpWeight 500:200, cluster1: 500/(500+200)=0.714 cluster2: 200/(500+200)=0.286
			wantClusterWeights: map[string]int64{
				"cluster1": int64(714),
				"cluster2": int64(286),
			},
			wantErr: assert.NoError,
		},
		{
			name: "empty node level info",
			args: makeArgs(
				makeClusterWithCPU("cluster1", -1, -1),
			),
			wantClusterWeights: map[string]int64{
				"cluster1": int64(1000),
			},
			wantErr: assert.NoError,
		},
		{
			name: "1 cluster node level info missing",
			args: makeArgs(
				makeClusterWithCPU("cluster1", -1, -1),
				makeClusterWithCPU("cluster2", 400, 100),
				makeClusterWithCPU("cluster3", 200, 100),
			),
			wantClusterWeights: map[string]int64{
				"cluster1": int64(0),
				"cluster2": int64(600),
				"cluster3": int64(400),
			},
			wantErr: assert.NoError,
		},
		{
			name: "all clusters node level info missing",
			args: makeArgs(
				makeClusterWithCPU("cluster1", -1, -1),
				makeClusterWithCPU("cluster2", -1, 100),
				makeClusterWithCPU("cluster3", -1, 100),
			),
			wantClusterWeights: map[string]int64{
				"cluster1": int64(333),
				"cluster2": int64(333),
				"cluster3": int64(333),
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotClusterWeights, err := AvailableToPercentage(tt.args.clusterAvailables, corev1.ResourceCPU, tt.args.weightLimit)
			if !tt.wantErr(
				t,
				err,
				fmt.Sprintf("AvailableToPercentage(%v, %v)", tt.args.clusterAvailables, tt.args.weightLimit),
			) {
				return
			}
			assert.Equalf(
				t,
				tt.wantClusterWeights,
				gotClusterWeights,
				"AvailableToPercentage(%v, %v)",
				tt.args.clusterAvailables,
				tt.args.weightLimit,
			)
		})
	}
}

func TestClusterWeights(t *testing.T) {
	tests := []struct {
		name                 string
		schedulingUnit       framework.SchedulingUnit
		clusters             []*fedcorev1a1.FederatedCluster
		expectedReplicasList framework.ClusterReplicasList
		expectedResult       *framework.Result
	}{
		{
			name: "Dynamic scheduling with no weights specified",
			schedulingUnit: framework.SchedulingUnit{
				DesiredReplicas: pointer.Int64(10),
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				makeClusterWithCPU("cluster1", 200, 200),
				makeClusterWithCPU("cluster2", 300, 300),
				makeClusterWithCPU("cluster3", 500, 500),
			},
			expectedReplicasList: framework.ClusterReplicasList{
				{
					Cluster:  makeClusterWithCPU("cluster1", 200, 200),
					Replicas: 2,
				},
				{
					Cluster:  makeClusterWithCPU("cluster2", 300, 300),
					Replicas: 3,
				},
				{
					Cluster:  makeClusterWithCPU("cluster3", 500, 500),
					Replicas: 5,
				},
			},
			expectedResult: framework.NewResult(framework.Success),
		},
		{
			name: "Static scheduling with weights specified",
			schedulingUnit: framework.SchedulingUnit{
				DesiredReplicas: pointer.Int64(10),
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
				Weights: map[string]int64{
					"cluster1": 2,
					"cluster2": 3,
					"cluster3": 5,
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				NewFederatedCluster("cluster1"),
				NewFederatedCluster("cluster2"),
				NewFederatedCluster("cluster3"),
			},
			expectedReplicasList: framework.ClusterReplicasList{
				{
					Cluster:  NewFederatedCluster("cluster1"),
					Replicas: 2,
				},
				{
					Cluster:  NewFederatedCluster("cluster2"),
					Replicas: 3,
				},
				{
					Cluster:  NewFederatedCluster("cluster3"),
					Replicas: 5,
				},
			},
			expectedResult: framework.NewResult(framework.Success),
		},
		{
			name: "Static scheduling with some weights specified",
			schedulingUnit: framework.SchedulingUnit{
				DesiredReplicas: pointer.Int64(10),
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
				Weights: map[string]int64{
					"cluster1": 2,
					"cluster2": 3,
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				NewFederatedCluster("cluster1"),
				NewFederatedCluster("cluster2"),
				NewFederatedCluster("cluster3"),
			},
			expectedReplicasList: framework.ClusterReplicasList{
				{
					Cluster:  NewFederatedCluster("cluster1"),
					Replicas: 4,
				},
				{
					Cluster:  NewFederatedCluster("cluster2"),
					Replicas: 6,
				},
			},
			expectedResult: framework.NewResult(framework.Success),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rspPlugin := &ClusterCapacityWeight{}

			replicasList, res := rspPlugin.ReplicaScheduling(context.Background(), &tt.schedulingUnit, tt.clusters)
			assert.Equalf(
				t,
				tt.expectedReplicasList,
				replicasList,
				"unexpected replicas list, want: %v got %v",
				tt.expectedReplicasList,
				replicasList,
			)
			assert.Equalf(t, tt.expectedResult, res, "unexpected result, want: %v got %v", tt.expectedResult, res)
		})
	}
}

func TestMinReplicas(t *testing.T) {
	tests := []struct {
		name                 string
		schedulingUnit       framework.SchedulingUnit
		clusters             []*fedcorev1a1.FederatedCluster
		expectedReplicasList framework.ClusterReplicasList
		expectedResult       *framework.Result
	}{
		{
			name: "MinReplicas should be respected",
			schedulingUnit: framework.SchedulingUnit{
				DesiredReplicas: pointer.Int64(10),
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
				Weights: map[string]int64{
					"cluster1": 2,
					"cluster2": 3,
					"cluster3": 5,
				},
				MinReplicas: map[string]int64{
					"cluster1": 3,
					"cluster2": 3,
					"cluster3": 3,
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				NewFederatedCluster("cluster1"),
				NewFederatedCluster("cluster2"),
				NewFederatedCluster("cluster3"),
			},
			expectedReplicasList: framework.ClusterReplicasList{
				{
					Cluster:  NewFederatedCluster("cluster1"),
					Replicas: 3,
				},
				{
					Cluster:  NewFederatedCluster("cluster2"),
					Replicas: 3,
				},
				{
					Cluster:  NewFederatedCluster("cluster3"),
					Replicas: 4,
				},
			},
			expectedResult: framework.NewResult(framework.Success),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rspPlugin := &ClusterCapacityWeight{}

			replicasList, res := rspPlugin.ReplicaScheduling(context.Background(), &tt.schedulingUnit, tt.clusters)
			assert.Equalf(
				t,
				tt.expectedReplicasList,
				replicasList,
				"unexpected replicas list, want: %v got %v",
				tt.expectedReplicasList,
				replicasList,
			)
			assert.Equalf(t, tt.expectedResult, res, "unexpected result, want: %v got %v", tt.expectedResult, res)
		})
	}
}

func TestMaxReplicas(t *testing.T) {
	tests := []struct {
		name                 string
		schedulingUnit       framework.SchedulingUnit
		clusters             []*fedcorev1a1.FederatedCluster
		expectedReplicasList framework.ClusterReplicasList
		expectedResult       *framework.Result
	}{
		{
			name: "MaxReplicas is hard constraint",
			schedulingUnit: framework.SchedulingUnit{
				DesiredReplicas: pointer.Int64(10),
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
				Weights: map[string]int64{
					"cluster1": 2,
					"cluster2": 3,
					"cluster3": 5,
				},
				MaxReplicas: map[string]int64{
					"cluster1": 1,
					"cluster2": 1,
					"cluster3": 1,
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				NewFederatedCluster("cluster1"),
				NewFederatedCluster("cluster2"),
				NewFederatedCluster("cluster3"),
			},
			expectedReplicasList: framework.ClusterReplicasList{
				{
					Cluster:  NewFederatedCluster("cluster1"),
					Replicas: 1,
				},
				{
					Cluster:  NewFederatedCluster("cluster2"),
					Replicas: 1,
				},
				{
					Cluster:  NewFederatedCluster("cluster3"),
					Replicas: 1,
				},
			},
			expectedResult: framework.NewResult(framework.Success),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rspPlugin := &ClusterCapacityWeight{}

			replicasList, res := rspPlugin.ReplicaScheduling(context.Background(), &tt.schedulingUnit, tt.clusters)
			assert.Equalf(
				t,
				tt.expectedReplicasList,
				replicasList,
				"unexpected replicas list, want: %v got %v",
				tt.expectedReplicasList,
				replicasList,
			)
			assert.Equalf(t, tt.expectedResult, res, "unexpected result, want: %v got %v", tt.expectedResult, res)
		})
	}
}
