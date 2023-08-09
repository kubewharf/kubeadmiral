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
	var totalFraction float64

	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	resources := getRelevantResources(su)
	allocatable, requested := getAllocatableAndRequested(su, cluster, resources)

	fractions := make([]float64, 0, len(requested))
	for i := range requested {
		fraction := fractionOfCapacity(requested[i], allocatable[i])
		// This to find a cluster which has most balanced resource usage.
		if fraction >= 1 {
			// if requested >= capacity, the corresponding host should never be preferred.
			return 0, framework.NewResult(framework.Success)
		}
		totalFraction += fraction
		fractions = append(fractions, fraction)
	}

	std := 0.0

	// For most cases, resources are limited to cpu and memory, the std could be simplified to std := (fraction1-fraction2)/2
	// len(fractions) > 2: calculate std based on the well-known formula - root square of Σ((fraction(i)-mean)^2)/len(fractions)
	// Otherwise, set the std to zero is enough.
	if len(fractions) == 2 {
		std = math.Abs((fractions[0] - fractions[1]) / 2)
	} else if len(fractions) > 2 {
		mean := totalFraction / float64(len(fractions))
		var sum float64
		for _, fraction := range fractions {
			sum += (fraction - mean) * (fraction - mean)
		}
		std = math.Sqrt(sum / float64(len(fractions)))
	}

	// STD (standard deviation) is always a positive value. 1-deviation lets the score to be higher for cluster which has least deviation and
	// multiplying it with `MaxClusterScore` provides the scaling factor needed.
	return int64((1 - std) * float64(framework.MaxClusterScore)), framework.NewResult(framework.Success)
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
