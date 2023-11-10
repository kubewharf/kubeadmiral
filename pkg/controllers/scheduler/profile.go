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
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	fedcore "github.com/kubewharf/kubeadmiral/pkg/apis/core"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/apiresources"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusteraffinity"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusterready"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusterresources"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusterterminating"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/maxcluster"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/placement"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/rsp"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/tainttoleration"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/runtime"
)

// inTreeRegistry should contain all known in-tree plugins
var inTreeRegistry = runtime.Registry{
	names.APIResources:                       apiresources.NewAPIResources,
	names.ClusterReady:                       clusterready.NewClusterReady,
	names.ClusterTerminating:                 clusterterminating.NewClusterTerminating,
	names.ClusterAffinity:                    clusteraffinity.NewClusterAffinity,
	names.ClusterResourcesFit:                clusterresources.NewClusterResourcesFit,
	names.PlacementFilter:                    placement.NewPlacementFilter,
	names.TaintToleration:                    tainttoleration.NewTaintToleration,
	names.ClusterResourcesBalancedAllocation: clusterresources.NewClusterResourcesBalancedAllocation,
	names.ClusterResourcesLeastAllocated:     clusterresources.NewClusterResourcesLeastAllocated,
	names.ClusterResourcesMostAllocated:      clusterresources.NewClusterResourcesMostAllocated,
	names.MaxCluster:                         maxcluster.NewMaxCluster,
	names.ClusterCapacityWeight:              rsp.NewClusterCapacityWeight,
}

func applyProfile(base *fedcore.EnabledPlugins, profile *fedcorev1a1.SchedulingProfile) {
	if profile.Spec.Plugins == nil {
		return
	}

	base.FilterPlugins = reconcileExtPoint(base.FilterPlugins, profile.Spec.Plugins.Filter)
	base.ScorePlugins = reconcileExtPoint(base.ScorePlugins, profile.Spec.Plugins.Score)
	base.SelectPlugins = reconcileExtPoint(base.SelectPlugins, profile.Spec.Plugins.Select)
}

func reconcileExtPoint(enabled []string, pluginSet fedcorev1a1.PluginSet) []string {
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

func (s *Scheduler) createFramework(
	profile *fedcorev1a1.SchedulingProfile,
	handle framework.Handle,
) (framework.Framework, error) {
	enabledPlugins := fedcorev1a1.GetDefaultEnabledPlugins()
	if profile != nil {
		applyProfile(enabledPlugins, profile)
	}

	registry := runtime.Registry{}

	if err := registry.Merge(inTreeRegistry); err != nil {
		// This should not happen
		err = fmt.Errorf("failed to merge in-tree plugin registry into empty registry: %w", err)
		return nil, err
	}
	webhookRegistry, err := s.webhookPluginRegistry()
	if err != nil {
		return nil, fmt.Errorf("failed to get webhook plugin registry: %w", err)
	}
	if err := registry.Merge(webhookRegistry); err != nil {
		return nil, fmt.Errorf("failed to merge webhook plugin registry: %w", err)
	}

	return runtime.NewFramework(
		registry,
		handle,
		enabledPlugins,
		profile.ProfileName(),
		s.metrics,
	)
}
