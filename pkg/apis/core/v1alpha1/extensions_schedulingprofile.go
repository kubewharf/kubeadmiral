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

package v1alpha1

import (
	fedcore "github.com/kubewharf/kubeadmiral/pkg/apis/core"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

func GetDefaultEnabledPlugins() *fedcore.EnabledPlugins {
	filterPlugins := []string{
		names.APIResources,
		names.TaintToleration,
		names.ClusterResourcesFit,
		names.PlacementFilter,
		names.ClusterAffinity,
	}

	scorePlugins := []string{
		names.TaintToleration,
		names.ClusterResourcesBalancedAllocation,
		names.ClusterResourcesLeastAllocated,
		names.ClusterAffinity,
	}

	selectPlugins := []string{names.MaxCluster}
	replicasPlugins := []string{names.ClusterCapacityWeight}

	return &fedcore.EnabledPlugins{
		FilterPlugins:   filterPlugins,
		ScorePlugins:    scorePlugins,
		SelectPlugins:   selectPlugins,
		ReplicasPlugins: replicasPlugins,
	}
}
