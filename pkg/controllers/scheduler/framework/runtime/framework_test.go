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

package runtime

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	fedcore "github.com/kubewharf/kubeadmiral/pkg/apis/core"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

type naiveFilterPlugin struct {
	result bool
}

func (n *naiveFilterPlugin) Name() string {
	return "NaiveFilterPlugin"
}

func (n *naiveFilterPlugin) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	if n.result {
		return framework.NewResult(framework.Success)
	}
	return framework.NewResult(framework.Error)
}

func getNaiveFilterPluginFactory(result bool) PluginFactory {
	return func(_ framework.Handle) (framework.Plugin, error) {
		return &naiveFilterPlugin{result: result}, nil
	}
}

type fakeFilterPlugin struct{}

func (*fakeFilterPlugin) Name() string {
	panic("unimplemented")
}

func (*fakeFilterPlugin) Filter(context.Context, *framework.SchedulingUnit, *fedcorev1a1.FederatedCluster) *framework.Result {
	panic("unimplemented")
}

var _ framework.FilterPlugin = &fakeFilterPlugin{}

type fakeScorePlugin struct{}

func (*fakeScorePlugin) Name() string {
	panic("unimplemented")
}

func (*fakeScorePlugin) Score(context.Context, *framework.SchedulingUnit, *fedcorev1a1.FederatedCluster) (int64, *framework.Result) {
	panic("unimplemented")
}

func (*fakeScorePlugin) ScoreExtensions() framework.ScoreExtensions {
	panic("unimplemented")
}

var _ framework.ScorePlugin = &fakeScorePlugin{}

type fakeSelectPlugin struct{}

func (*fakeSelectPlugin) Name() string {
	panic("unimplemented")
}

func (*fakeSelectPlugin) SelectClusters(
	context.Context,
	*framework.SchedulingUnit,
	framework.ClusterScoreList,
) (framework.ClusterScoreList, *framework.Result) {
	panic("unimplemented")
}

var _ framework.SelectPlugin = &fakeSelectPlugin{}

type fakeReplicasPlugin struct{}

func (*fakeReplicasPlugin) Name() string {
	panic("unimplemented")
}

func (*fakeReplicasPlugin) ReplicaScheduling(
	context.Context,
	*framework.SchedulingUnit,
	[]*fedcorev1a1.FederatedCluster,
) (framework.ClusterReplicasList, *framework.Result) {
	panic("unimplemented")
}

var _ framework.ReplicasPlugin = &fakeReplicasPlugin{}

type fakeFilterAndScorePlugin struct {
	*fakeFilterPlugin
	*fakeScorePlugin
}

func (*fakeFilterAndScorePlugin) Name() string {
	panic("unimplemented")
}

var (
	_ framework.FilterPlugin = &fakeFilterAndScorePlugin{}
	_ framework.ScorePlugin  = &fakeFilterAndScorePlugin{}
)

type fakeScoreAndSelectPlugin struct {
	*fakeScorePlugin
	*fakeSelectPlugin
}

func (*fakeScoreAndSelectPlugin) Name() string {
	panic("unimplemented")
}

var (
	_ framework.ScorePlugin  = &fakeScoreAndSelectPlugin{}
	_ framework.SelectPlugin = &fakeScoreAndSelectPlugin{}
)

func TestRunFilterPlugins(t *testing.T) {
	tests := []struct {
		name           string
		plugins        map[string]PluginFactory
		enabledPlugins *fedcore.EnabledPlugins
		expectedResult *framework.Result
	}{
		{
			"single filter plugin, succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(true),
			},
			&fedcore.EnabledPlugins{FilterPlugins: []string{"a"}},
			framework.NewResult(framework.Success),
		},
		{
			"single filter plugin, failed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(false),
			},
			&fedcore.EnabledPlugins{FilterPlugins: []string{"a"}},
			framework.NewResult(framework.Error).WithFailedPlugin("NaiveFilterPlugin"),
		},
		{
			"multiple filter plugins, all succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(true),
				"b": getNaiveFilterPluginFactory(true),
				"c": getNaiveFilterPluginFactory(true),
			},
			&fedcore.EnabledPlugins{FilterPlugins: []string{"a", "b", "c"}},
			framework.NewResult(framework.Success),
		},
		{
			"multiple filter plugins, some succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(true),
				"b": getNaiveFilterPluginFactory(false),
				"c": getNaiveFilterPluginFactory(true),
			},
			&fedcore.EnabledPlugins{FilterPlugins: []string{"a", "b", "c"}},
			framework.NewResult(framework.Error).WithFailedPlugin("NaiveFilterPlugin"),
		},
		{
			"multiple filter plugins, none succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(false),
				"b": getNaiveFilterPluginFactory(false),
				"c": getNaiveFilterPluginFactory(false),
			},
			&fedcore.EnabledPlugins{FilterPlugins: []string{"a", "b", "c"}},
			framework.NewResult(framework.Error).WithFailedPlugin("NaiveFilterPlugin"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics := stats.NewMock("test", "kubeadmiral_controller_manager", false)
			fwk, err := NewFramework(test.plugins, nil, test.enabledPlugins, "", metrics)
			if err != nil {
				t.Fatalf("unexpected error when creating framework: %v", err)
			}

			result := fwk.RunFilterPlugins(context.Background(), nil, nil)
			if !reflect.DeepEqual(result, test.expectedResult) {
				t.Errorf("unexpected run filter plugin results: %v, want: %v", result, test.expectedResult)
			}
		})
	}
}

func TestNewFramework(t *testing.T) {
	filterPlugin := &fakeFilterPlugin{}
	scorePlugin := &fakeScorePlugin{}
	selectPlugin := &fakeSelectPlugin{}
	replicasPlugin := &fakeReplicasPlugin{}
	filterAndScorePlugin := &fakeFilterAndScorePlugin{}
	scoreAndSelectPlugin := &fakeScoreAndSelectPlugin{}

	newRegistry := func() Registry {
		var (
			filterConstructed         atomic.Bool
			scoreConstructed          atomic.Bool
			selectConstructed         atomic.Bool
			replicasConstructed       atomic.Bool
			filterAndScoreConstructed atomic.Bool
			scoreAndSelectConstructed atomic.Bool
		)
		return Registry{
			"filter": func(f framework.Handle) (framework.Plugin, error) {
				if filterConstructed.Load() {
					t.Fatalf("filter plugin constructed more than once")
				}
				filterConstructed.Store(true)
				return filterPlugin, nil
			},
			"score": func(f framework.Handle) (framework.Plugin, error) {
				if scoreConstructed.Load() {
					t.Fatalf("score plugin constructed more than once")
				}
				scoreConstructed.Store(true)
				return scorePlugin, nil
			},
			"select": func(f framework.Handle) (framework.Plugin, error) {
				if selectConstructed.Load() {
					t.Fatalf("select plugin constructed more than once")
				}
				selectConstructed.Store(true)
				return selectPlugin, nil
			},
			"replicas": func(f framework.Handle) (framework.Plugin, error) {
				if replicasConstructed.Load() {
					t.Fatalf("replicas constructed more than once")
				}
				replicasConstructed.Store(true)
				return replicasPlugin, nil
			},
			"filterAndScore": func(f framework.Handle) (framework.Plugin, error) {
				if filterAndScoreConstructed.Load() {
					t.Fatalf("filterAndScore constructed more than once")
				}
				filterAndScoreConstructed.Store(true)
				return filterAndScorePlugin, nil
			},
			"scoreAndSelect": func(f framework.Handle) (framework.Plugin, error) {
				if scoreAndSelectConstructed.Load() {
					t.Fatalf("scoreAndSelect constructed more than once")
				}
				scoreAndSelectConstructed.Store(true)
				return scoreAndSelectPlugin, nil
			},
			"notEnabled": func(f framework.Handle) (framework.Plugin, error) {
				t.Fatalf("plugin not enabled should not be constructed")
				return nil, nil
			},
		}
	}
	tests := []struct {
		name           string
		enabledPlugins fedcore.EnabledPlugins
		expected       *frameworkImpl
		shouldError    bool
	}{
		{
			"enabled plugins are respected",
			fedcore.EnabledPlugins{
				FilterPlugins:   []string{"filter"},
				ScorePlugins:    []string{"score"},
				SelectPlugins:   []string{"scoreAndSelect"},
				ReplicasPlugins: []string{"replicas"},
			},
			&frameworkImpl{
				filterPlugins:   []framework.FilterPlugin{filterPlugin},
				scorePlugins:    []framework.ScorePlugin{scorePlugin},
				selectPlugins:   []framework.SelectPlugin{scoreAndSelectPlugin},
				replicasPlugins: []framework.ReplicasPlugin{replicasPlugin},
				profileName:     "",
				metrics:         stats.NewMock("test", "kubeadmiral_controller_manager", false),
			},
			false,
		},
		{
			"enabled plugins are respected 2",
			fedcore.EnabledPlugins{
				FilterPlugins: []string{"filter", "filterAndScore"},
				ScorePlugins:  []string{"score", "scoreAndSelect"},
				SelectPlugins: []string{"scoreAndSelect", "select"},
			},
			&frameworkImpl{
				filterPlugins: []framework.FilterPlugin{filterPlugin, filterAndScorePlugin},
				scorePlugins:  []framework.ScorePlugin{scorePlugin, scoreAndSelectPlugin},
				selectPlugins: []framework.SelectPlugin{scoreAndSelectPlugin, selectPlugin},
				profileName:   "",
				metrics:       stats.NewMock("test", "kubeadmiral_controller_manager", false),
			},
			false,
		},
		{
			"repeated plugins returns error",
			fedcore.EnabledPlugins{
				FilterPlugins:   []string{"filter"},
				ScorePlugins:    []string{"score", "score"},
				SelectPlugins:   []string{"scoreAndSelect"},
				ReplicasPlugins: []string{"replicas"},
			},
			nil,
			true,
		},
		{
			"incorrect type returns error",
			fedcore.EnabledPlugins{
				FilterPlugins:   []string{"replicas"},
				ScorePlugins:    []string{"score", "scoreAndSelect"},
				SelectPlugins:   []string{"scoreAndSelect", "select"},
				ReplicasPlugins: []string{"filter"},
			},
			nil,
			true,
		},
		{
			"plugins not found in registry returns error",
			fedcore.EnabledPlugins{
				FilterPlugins:   []string{"filter"},
				ScorePlugins:    []string{"score", "scoreAndSelect"},
				SelectPlugins:   []string{"scoreAndSelect", "select", "notexists"},
				ReplicasPlugins: []string{"replicas"},
			},
			nil,
			true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics := stats.NewMock("test", "kubeadmiral_controller_manager", false)
			fwk, err := NewFramework(newRegistry(), nil, &test.enabledPlugins, "", metrics)
			if test.shouldError {
				if err == nil {
					t.Fatal("expected error when creating framework but got nil")
				}
				t.Logf("got expected error: %v", err)
				return
			}

			if err != nil {
				t.Fatalf("unexpected error when creating framework: %v", err)
			}

			if !reflect.DeepEqual(fwk, test.expected) {
				t.Errorf("expected framework %+v but got %+v", test.expected, fwk)
			}
		})
	}
}
