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

package placement

import (
	"context"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

const (
	PlacementFilterName = "PlacementFilter"
)

type PlacementFilter struct{}

func NewPlacementFilter(_ framework.FrameworkHandle) (framework.Plugin, error) {
	return &PlacementFilter{}, nil
}

func (pl *PlacementFilter) Name() string {
	return PlacementFilterName
}

func (pl *PlacementFilter) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	if len(su.ClusterNames) == 0 {
		// no filtering
		return framework.NewResult(framework.Success)
	}

	if _, exists := su.ClusterNames[cluster.Name]; !exists {
		return framework.NewResult(framework.Unschedulable, "cluster is not in placement list")
	}

	return framework.NewResult(framework.Success)
}
