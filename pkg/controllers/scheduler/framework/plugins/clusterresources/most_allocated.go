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

type ClusterResourcesMostAllocated struct{}

func NewClusterResourcesMostAllocated(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterResourcesMostAllocated{}, nil
}

func (pl *ClusterResourcesMostAllocated) Name() string {
	return names.ClusterResourcesMostAllocated
}

// Score invoked at the score extension point.
func (pl *ClusterResourcesMostAllocated) Score(
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
	// most allocated score favors nodes with most requested resources.
	// It calculates the percentage of memory and CPU requested by pods scheduled on the node, and prioritizes
	// based on the maximum of the average of the fraction of requested to capacity.
	// Details:
	// (cpu((capacity-sum(requested))*100/capacity) * cpu_weight +
	// memory((capacity-sum(requested))*100/capacity) * memory_weight) / (cpu_weight + memory_weight)
	// Or with gpu
	// (cpu((capacity-sum(requested))*100/capacity) * cpu_weight +
	// memory((capacity-sum(requested))*100/capacity) * memory_weight +
	// gpu((capacity-sum(requested))*100/capacity) * gpu_weight) / (cpu_weight + memory_weight + gpu_weight)
	for _, resource := range resources {
		resourceScore := mostRequestedScore(requested[resource], allocatable[resource])
		weight := framework.DefaultRequestedRatioResources[resource]
		score += resourceScore * weight
		weightSum += weight
	}

	if weightSum == 0 {
		return 0, framework.NewResult(framework.Success)
	}

	return score / weightSum, framework.NewResult(framework.Success)
}

// ScoreExtensions of the Score plugin.
func (pl *ClusterResourcesMostAllocated) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// The used capacity is calculated on a scale of 0-100
// 0 being the lowest priority and 100 being the highest.
// The more resources are used the higher the score is. This function
// is almost a reversed version of least_requested_priority.calculateUnusedScore
// (100 - calculateUnusedScore). The main difference is in rounding. It was added to
// keep the final formula clean and not to modify the widely used (by users
// in their default scheduling policies) calculateUsedScore.
func mostRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return (requested * framework.MaxClusterScore) / capacity
}
