//go:build exclude
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
	"math"

	corev1 "k8s.io/api/core/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type ClusterResourcesBalancedAllocation struct{}

func NewClusterResourcesBalancedAllocation(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterResourcesBalancedAllocation{}, nil
}

func (pl *ClusterResourcesBalancedAllocation) Name() string {
	return names.ClusterResourcesBalancedAllocation
}

// Score invoked at the score extension point.
func (pl *ClusterResourcesBalancedAllocation) Score(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (int64, *framework.Result) {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	requested := make(framework.ResourceToValueMap, len(framework.DefaultRequestedRatioResources))
	allocatable := make(framework.ResourceToValueMap, len(framework.DefaultRequestedRatioResources))
	for resource := range framework.DefaultRequestedRatioResources {
		allocatable[resource], requested[resource] = calculateResourceAllocatableRequest(su, cluster, resource)
	}

	cpuFraction := fractionOfCapacity(requested[corev1.ResourceCPU], allocatable[corev1.ResourceCPU])
	memoryFraction := fractionOfCapacity(requested[corev1.ResourceMemory], allocatable[corev1.ResourceMemory])
	// This to find a node which has most balanced CPU, memory and volume usage.
	if cpuFraction >= 1 || memoryFraction >= 1 {
		// if requested >= capacity, the corresponding host should never be preferred.
		return 0, framework.NewResult(framework.Success)
	}

	// Upper and lower boundary of difference between cpuFraction and memoryFraction are -1 and 1
	// respectively. Multiplying the absolute value of the difference by 10 scales the value to
	// 0-10 with 0 representing well balanced allocation and 10 poorly balanced. Subtracting it from
	// 10 leads to the score which also scales from 0 to 10 while 10 representing well balanced.
	diff := math.Abs(cpuFraction - memoryFraction)
	score := int64((1 - diff) * float64(framework.MaxClusterScore))
	return score, framework.NewResult(framework.Success)
}

// ScoreExtensions of the Score plugin.
func (pl *ClusterResourcesBalancedAllocation) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func fractionOfCapacity(requested, capacity int64) float64 {
	if capacity == 0 {
		return 1
	}
	return float64(requested) / float64(capacity)
}
