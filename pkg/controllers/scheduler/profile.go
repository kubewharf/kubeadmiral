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

package scheduler

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/apiresources"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusteraffinity"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusterresources"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/maxcluster"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/placement"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/rsp"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/tainttoleration"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/runtime"
)

func getDefaultEnabledPlugins() *runtime.EnabledPlugins {
	filterPlugins := []string{
		apiresources.APIResourcesName,
		tainttoleration.TaintTolerationName,
		clusterresources.ClusterResourcesFitName,
		placement.PlacementFilterName,
		clusteraffinity.ClusterAffinityName,
	}

	scorePlugins := []string{
		tainttoleration.TaintTolerationName,
		clusterresources.ClusterResourcesBalancedAllocationName,
		clusterresources.ClusterResourcesLeastAllocatedName,
		clusteraffinity.ClusterAffinityName,
	}

	selectPlugins := []string{maxcluster.MaxClusterName}
	replicasPlugins := []string{rsp.ClusterCapacityWeightName}

	return &runtime.EnabledPlugins{
		FilterPlugins:   filterPlugins,
		ScorePlugins:    scorePlugins,
		SelectPlugins:   selectPlugins,
		ReplicasPlugins: replicasPlugins,
	}
}

func applyProfile(enabledPlugins *runtime.EnabledPlugins, profile *fedcorev1a1.SchedulingProfile) {
	if profile.Spec.Plugins == nil {
		return
	}

	reconcileExtPoint := func(enabled []string, pluginSet fedcorev1a1.PluginSet) []string {
		disabledSet := sets.New[string]()
		for _, p := range pluginSet.Disabled {
			disabledSet.Insert(p.Name)
		}

		result := []string{}
		if !disabledSet.Has("*") {
			for _, e := range enabled {
				if !disabledSet.Has(e) {
					result = append(result, e)
				}
			}
		}

		for _, p := range pluginSet.Enabled {
			result = append(result, p.Name)
		}

		return result
	}

	enabledPlugins.FilterPlugins = reconcileExtPoint(enabledPlugins.FilterPlugins, profile.Spec.Plugins.Filter)
	enabledPlugins.ScorePlugins = reconcileExtPoint(enabledPlugins.ScorePlugins, profile.Spec.Plugins.Score)
	enabledPlugins.SelectPlugins = reconcileExtPoint(enabledPlugins.SelectPlugins, profile.Spec.Plugins.Select)
}

func (s *Scheduler) profileForFedObject(_ *unstructured.Unstructured, profile *fedcorev1a1.SchedulingProfile) (framework.Framework, error) {
	// inTreeRegistry should contain all known in-tree plugins
	inTreeRegistry := runtime.Registry{
		apiresources.APIResourcesName:                           apiresources.NewAPIResources,
		clusteraffinity.ClusterAffinityName:                     clusteraffinity.NewClusterAffinity,
		clusterresources.ClusterResourcesFitName:                clusterresources.NewClusterResourcesFit,
		placement.PlacementFilterName:                           placement.NewPlacementFilter,
		tainttoleration.TaintTolerationName:                     tainttoleration.NewTaintToleration,
		clusterresources.ClusterResourcesBalancedAllocationName: clusterresources.NewClusterResourcesBalancedAllocation,
		clusterresources.ClusterResourcesLeastAllocatedName:     clusterresources.NewClusterResourcesLeastAllocated,
		clusterresources.ClusterResourcesMostAllocatedName:      clusterresources.NewClusterResourcesMostAllocated,
		maxcluster.MaxClusterName:                               maxcluster.NewMaxCluster,
		rsp.ClusterCapacityWeightName:                           rsp.NewClusterCapacityWeight,
	}

	enabledPlugins := getDefaultEnabledPlugins()
	applyProfile(enabledPlugins, profile)

	return runtime.NewFramework(
		inTreeRegistry,
		enabledPlugins,
		runtime.WithDynamicClient(s.dynamicClient),
	)
}
