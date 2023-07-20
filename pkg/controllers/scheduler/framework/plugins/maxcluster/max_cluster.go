//go:build exclude
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
	"sort"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

const (
	MaxClusterErrReason = "max cluster is less than 0"
)

type MaxCluster struct{}

func NewMaxCluster(_ framework.Handle) (framework.Plugin, error) {
	return &MaxCluster{}, nil
}

func (pl *MaxCluster) Name() string {
	return names.MaxCluster
}

func (pl *MaxCluster) SelectClusters(
	ctx context.Context,
	su *framework.SchedulingUnit,
	clusterScoreList framework.ClusterScoreList,
) ([]*fedcorev1a1.FederatedCluster, *framework.Result) {
	clusters := make([]*fedcorev1a1.FederatedCluster, 0)
	if su.MaxClusters != nil && *su.MaxClusters < 0 {
		return clusters, framework.NewResult(framework.Unschedulable, MaxClusterErrReason)
	}

	sort.Slice(clusterScoreList, func(i, j int) bool {
		return clusterScoreList[i].Score > clusterScoreList[j].Score
	})

	length := len(clusterScoreList)
	if su.MaxClusters != nil && int(*su.MaxClusters) < length {
		length = int(*su.MaxClusters)
	}

	for i := 0; i < length; i++ {
		clusters = append(clusters, clusterScoreList[i].Cluster)
	}

	return clusters, framework.NewResult(framework.Success)
}
