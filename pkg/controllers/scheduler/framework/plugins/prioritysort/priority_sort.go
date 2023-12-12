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

package prioritysort

import (
	"context"
	"sort"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type PrioritySort struct{}

func NewPrioritySort(_ framework.Handle) (framework.Plugin, error) {
	return &PrioritySort{}, nil
}

func (pl *PrioritySort) Name() string {
	return names.PrioritySort
}

func (pl *PrioritySort) SelectClusters(
	ctx context.Context,
	su *framework.SchedulingUnit,
	clusterScoreList framework.ClusterScoreList,
) (framework.ClusterScoreList, *framework.Result) {
	var ret framework.ClusterScoreList

	// For preferenceBinpack, using scores as priorities is not stable.
	// For example, scores are now mainly from resource usage and utilization.
	// Through the binpack process, priorities of different clusters may change which may bring some unexpected results.
	if len(su.Priorities) > 0 || (su.SchedulingMode == fedcorev1a1.SchedulingModeDivide &&
		su.ReplicasStrategy == fedcorev1a1.ReplicasStrategyBinpack) {
		for _, clusterScore := range clusterScoreList {
			ret = append(ret, framework.ClusterScore{
				Cluster: clusterScore.Cluster,
				Score:   su.Priorities[clusterScore.Cluster.Name],
			})
		}
	} else {
		ret = clusterScoreList
	}

	sort.Slice(ret, func(i, j int) bool {
		if ret[i].Score != ret[j].Score {
			return ret[i].Score > ret[j].Score
		}
		return ret[i].Cluster.Name < ret[j].Cluster.Name
	})

	return ret, framework.NewResult(framework.Success)
}
