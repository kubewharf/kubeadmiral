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

package runtime

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	fedcore "github.com/kubewharf/kubeadmiral/pkg/apis/core"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
)

const (
	filter         = "Filter"
	score          = "Score"
	selectClusters = "SelectClusters"
	replicas       = "Replicas"
)

type frameworkImpl struct {
	filterPlugins   []framework.FilterPlugin
	scorePlugins    []framework.ScorePlugin
	selectPlugins   []framework.SelectPlugin
	replicasPlugins []framework.ReplicasPlugin

	profileName string
	metrics     stats.Metrics
}

var _ framework.Framework = &frameworkImpl{}

func NewFramework(registry Registry, handle framework.Handle, enabledPlugins *fedcore.EnabledPlugins,
	profileName string, metrics stats.Metrics,
) (framework.Framework, error) {
	fwk := &frameworkImpl{}

	pluginsMap := make(map[string]framework.Plugin)

	for name, factory := range registry {
		if !enabledPlugins.IsPluginEnabled(name) {
			continue
		}
		plugin, err := factory(handle)
		if err != nil {
			return nil, fmt.Errorf("error initializing plugin %q: %w", name, err)
		}
		pluginsMap[name] = plugin
	}

	for _, e := range fwk.getExtensionPoints(enabledPlugins) {
		if err := addPlugins(e.slicePtr, e.plugins, pluginsMap); err != nil {
			return nil, err
		}
	}

	fwk.profileName = profileName
	fwk.metrics = metrics
	return fwk, nil
}

func addPlugins(pluginList interface{}, enabledPluginNames []string, pluginsMap map[string]framework.Plugin) error {
	plugins := reflect.ValueOf(pluginList).Elem()
	pluginType := plugins.Type().Elem()

	registeredPlugins := sets.New[string]()
	for _, pluginName := range enabledPluginNames {
		pg, ok := pluginsMap[pluginName]
		if !ok {
			return fmt.Errorf("%s %s does not exist", pluginType.Name(), pluginName)
		}

		if !reflect.TypeOf(pg).Implements(pluginType) {
			return fmt.Errorf("plugin %s does not implement %s", pluginName, pluginType.Name())
		}

		if registeredPlugins.Has(pluginName) {
			return fmt.Errorf("plugin %s already registered as %s", pluginName, pluginType.Name())
		}

		newPlugins := reflect.Append(plugins, reflect.ValueOf(pg))
		plugins.Set(newPlugins)
		registeredPlugins.Insert(pluginName)
	}

	return nil
}

// extensionPoint encapsulates desired and applied set of plugins at a specific extension point.
type extensionPoint struct {
	// the set of plugins to be configured at this extension point.
	plugins []string
	// a pointer to the slice storing plugins implementations that will run at this extension point.
	slicePtr interface{}
}

func (f *frameworkImpl) getExtensionPoints(enabledPlugins *fedcore.EnabledPlugins) []extensionPoint {
	return []extensionPoint{
		{plugins: enabledPlugins.FilterPlugins, slicePtr: &f.filterPlugins},
		{plugins: enabledPlugins.ScorePlugins, slicePtr: &f.scorePlugins},
		{plugins: enabledPlugins.SelectPlugins, slicePtr: &f.selectPlugins},
		{plugins: enabledPlugins.ReplicasPlugins, slicePtr: &f.replicasPlugins},
	}
}

func (f *frameworkImpl) RunFilterPlugins(
	ctx context.Context,
	schedulingUnit *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (result *framework.Result) {
	startTime := time.Now()
	defer func() {
		f.metrics.Duration(metrics.SchedulerFrameworkExtensionPointDuration, startTime,
			stats.Tag{Name: "extension_point", Value: filter},
			stats.Tag{Name: "profile", Value: f.profileName},
			stats.Tag{Name: "status", Value: result.Code().String()})
	}()
	for _, pl := range f.filterPlugins {
		pluginResult := f.runFilterPlugin(ctx, pl, schedulingUnit, cluster)
		if !pluginResult.IsSuccess() {
			pluginResult.SetFailedPlugin(pl.Name())
			return pluginResult
		}
	}
	return framework.NewResult(framework.Success)
}

func (f *frameworkImpl) runFilterPlugin(
	ctx context.Context,
	pl framework.FilterPlugin,
	schedulingUnit *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (result *framework.Result) {
	startTime := time.Now()
	defer func() {
		f.metrics.Duration(metrics.SchedulerPluginExecutionDuration, startTime,
			stats.Tag{Name: "extension_point", Value: filter},
			stats.Tag{Name: "plugin", Value: pl.Name()},
			stats.Tag{Name: "status", Value: result.Code().String()})
	}()
	result = pl.Filter(ctx, schedulingUnit, cluster)
	return result
}

func (f *frameworkImpl) RunScorePlugins(
	ctx context.Context,
	schedulingUnit *framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (pluginToClusterScore framework.PluginToClusterScore, result *framework.Result) {
	startTime := time.Now()
	defer func() {
		f.metrics.Duration(metrics.SchedulerFrameworkExtensionPointDuration, startTime,
			stats.Tag{Name: "extension_point", Value: score},
			stats.Tag{Name: "profile", Value: f.profileName},
			stats.Tag{Name: "status", Value: result.Code().String()})
	}()
	pluginToClusterScore = make(framework.PluginToClusterScore)

	for _, plugin := range f.scorePlugins {
		scoreList := make(framework.ClusterScoreList, len(clusters))
		for i, cluster := range clusters {
			score, res := plugin.Score(ctx, schedulingUnit, cluster)
			if !res.IsSuccess() {
				msg := fmt.Sprintf(
					"plugin %q schedulingUnit %s failed with %s",
					plugin.Name(),
					schedulingUnit.Key(),
					res.AsError(),
				)
				klog.Error(msg)
				return nil, framework.NewResult(framework.Error, msg)
			}
			scoreList[i] = framework.ClusterScore{Cluster: cluster, Score: score}
		}

		if plugin.ScoreExtensions() != nil {
			res := plugin.ScoreExtensions().NormalizeScore(ctx, scoreList)
			if !res.IsSuccess() {
				msg := fmt.Sprintf(
					"plugin %q NormalizeScore schedulingUnit %s failed with %s",
					plugin.Name(),
					schedulingUnit.Key(),
					res.AsError(),
				)
				klog.Error(msg)
				return nil, framework.NewResult(framework.Error, msg)
			}
		}

		pluginToClusterScore[plugin.Name()] = scoreList
	}

	return pluginToClusterScore, nil
}

func (f *frameworkImpl) RunSelectClustersPlugin(
	ctx context.Context,
	schedulingUnit *framework.SchedulingUnit,
	clusterScores framework.ClusterScoreList,
) (clusters []*fedcorev1a1.FederatedCluster, result *framework.Result) {
	startTime := time.Now()
	defer func() {
		f.metrics.Duration(metrics.SchedulerFrameworkExtensionPointDuration, startTime,
			stats.Tag{Name: "extension_point", Value: selectClusters},
			stats.Tag{Name: "profile", Value: f.profileName},
			stats.Tag{Name: "status", Value: result.Code().String()})
	}()
	if len(f.selectPlugins) == 0 {
		for _, clusterScore := range clusterScores {
			clusters = append(clusters, clusterScore.Cluster)
		}
		result = framework.NewResult(framework.Success)
		return clusters, result
	}
	for _, plugin := range f.selectPlugins {
		newClusterScores, result := plugin.SelectClusters(ctx, schedulingUnit, clusterScores)
		if !result.IsSuccess() {
			msg := fmt.Sprintf(
				"plugin %q failed to select clusters for schedulingUnit %s: %v",
				plugin.Name(),
				schedulingUnit.Key(),
				result.Message(),
			)
			klog.Error(msg)
			return nil, framework.NewResult(framework.Error, msg)
		}
		clusterScores = newClusterScores
	}

	for _, clusterScore := range clusterScores {
		clusters = append(clusters, clusterScore.Cluster)
	}
	return clusters, result
}

func (f *frameworkImpl) RunReplicasPlugin(
	ctx context.Context,
	schedulingUnit *framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (clusterReplicasList framework.ClusterReplicasList, result *framework.Result) {
	startTime := time.Now()
	defer func() {
		f.metrics.Duration(metrics.SchedulerFrameworkExtensionPointDuration, startTime,
			stats.Tag{Name: "extension_point", Value: replicas},
			stats.Tag{Name: "profile", Value: f.profileName},
			stats.Tag{Name: "status", Value: result.Code().String()})
	}()
	if len(clusters) == 0 {
		return clusterReplicasList, framework.NewResult(
			framework.Success,
			"unnecessary to schedule due to no clusters",
		)
	}
	if schedulingUnit.DesiredReplicas == nil || *schedulingUnit.DesiredReplicas <= 0 {
		return clusterReplicasList, framework.NewResult(
			framework.Success,
			"unnecessary to schedule due to replicas less or equal zero",
		)
	}
	if len(f.replicasPlugins) == 0 {
		return clusterReplicasList, framework.NewResult(
			framework.Success,
			"no replicas plugin registered in the framework",
		)
	}
	for _, plugin := range f.replicasPlugins {
		clusterReplicasList, result = plugin.ReplicaScheduling(ctx, schedulingUnit, clusters)
		if !result.IsSuccess() {
			msg := fmt.Sprintf(
				"plugin %q failed to replica scheduling for schedulingUnit %s: %v",
				plugin.Name(),
				schedulingUnit.Key(),
				result.Message(),
			)
			klog.Error(msg)
			return clusterReplicasList, framework.NewResult(framework.Error, msg)
		}
		return clusterReplicasList, result
	}
	return clusterReplicasList, result
}
