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

package clusterterminating

import (
	"context"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type ClusterTerminating struct{}

func NewClusterTerminating(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterTerminating{}, nil
}

func (pl *ClusterTerminating) Name() string {
	return names.ClusterTerminating
}

func (pl *ClusterTerminating) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	// Prevent scheduling to terminating cluster unless it is already scheduled to.
	replicas, alreadyScheduled := su.CurrentClusters[cluster.Name]
	if !alreadyScheduled && !cluster.DeletionTimestamp.IsZero() {
		return framework.NewResult(framework.Unschedulable, "cluster(s) were terminating")
	}
	// Prevent scheduling new replicas to terminating cluster
	if alreadyScheduled && su.MaxReplicas[cluster.Name] > *replicas {
		su.MaxReplicas[cluster.Name] = *replicas
	}

	return framework.NewResult(framework.Success)
}

// updateMaxReplicasForTerminatingCluster prevents scheduling new replicas to terminating clusters
func updateMaxReplicasForTerminatingCluster(
	su *framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) {
	for _, cluster := range clusters {
		if !cluster.DeletionTimestamp.IsZero() {
			replicas := su.CurrentClusters[cluster.Name]
			if replicas == nil {
				replicas = new(int64)
			}
			if su.MaxReplicas == nil {
				su.MaxReplicas = map[string]int64{cluster.Name: *replicas}
			} else if max, exist := su.MaxReplicas[cluster.Name]; !exist || max > *replicas {
				su.MaxReplicas[cluster.Name] = *replicas
			}
		}
	}
}
