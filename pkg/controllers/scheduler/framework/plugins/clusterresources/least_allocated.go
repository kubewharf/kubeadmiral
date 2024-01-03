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

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type ClusterResourcesLeastAllocated struct{}

func NewClusterResourcesLeastAllocated(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterResourcesLeastAllocated{}, nil
}

func (pl *ClusterResourcesLeastAllocated) Name() string {
	return names.ClusterResourcesLeastAllocated
}

// Score invoked at the score extension point.
func (pl *ClusterResourcesLeastAllocated) Score(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (int64, *framework.Result) {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	resources := getRelevantResources(su)
	allocatable, requested := getAllocatableAndRequested(su, cluster, resources)

	var score, weightSum int64
	// least allocated score favors cluster with fewer requested resources.
	// It calculates the percentage of memory, CPU, GPU or other non-build-in
	// requested resource by pods scheduled on the cluster, and
	// prioritizes based on the minimum of the average of the fraction of requested to capacity.
	//
	// Details:
	// (cpu((capacity-sum(requested))*100/capacity) * cpu_weight +
	// memory((capacity-sum(requested))*100/capacity) * memory_weight) / (cpu_weight + memory_weight)
	// Or with gpu
	// (cpu((capacity-sum(requested))*100/capacity) * cpu_weight +
	// memory((capacity-sum(requested))*100/capacity) * memory_weight +
	// gpu((capacity-sum(requested))*100/capacity) * gpu_weight) / (cpu_weight + memory_weight + gpu_weight)
	//
	// For any other non-build-in resource, we provided a default weight, eg:
	// a pod requests 'foo.com/bar1' and 'foo.com/bar2', the result will be:
	// (cpu((capacity-sum(requested))*100/capacity) * cpu_weight +
	// memory((capacity-sum(requested))*100/capacity) * memory_weight +
	// bar1((capacity-sum(requested))*100/capacity) * DefaultRatio +
	// bar2((capacity-sum(requested))*100/capacity) * DefaultRatio) / (cpu_weight + memory_weight + DefaultRatio * 2)
	for _, resource := range resources {
		resourceScore := leastRequestedScore(requested[resource], allocatable[resource])
		if weight, ok := framework.DefaultRequestedRatioResources[resource]; ok {
			score += resourceScore * weight
			weightSum += weight
		} else {
			score += resourceScore * framework.DefaultRatio
			weightSum += framework.DefaultRatio
		}
	}

	if weightSum == 0 {
		return 0, framework.NewResult(framework.Success)
	}

	return score / weightSum, framework.NewResult(framework.Success)
}

// ScoreExtensions of the Score plugin.
func (pl *ClusterResourcesLeastAllocated) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// The unused capacity is calculated on a scale of 0-100
// 0 being the lowest priority and 100 being the highest.
// The more unused resources the higher the score is.
func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}
	return ((capacity - requested) * int64(framework.MaxClusterScore)) / capacity
}
