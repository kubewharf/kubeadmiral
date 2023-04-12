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
	"testing"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
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

func TestRunFilterPlugins(t *testing.T) {
	tests := []struct {
		name           string
		plugins        map[string]PluginFactory
		expectedResult *framework.Result
	}{
		{
			"single filter plugin, succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(true),
			},
			framework.NewResult(framework.Success),
		},
		{
			"single filter plugin, failed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(false),
			},
			framework.NewResult(framework.Error),
		},
		{
			"multiple filter plugins, all succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(true),
				"b": getNaiveFilterPluginFactory(true),
				"c": getNaiveFilterPluginFactory(true),
			},
			framework.NewResult(framework.Success),
		},
		{
			"multiple filter plugins, some succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(true),
				"b": getNaiveFilterPluginFactory(false),
				"c": getNaiveFilterPluginFactory(true),
			},
			framework.NewResult(framework.Error),
		},
		{
			"multiple filter plugins, none succeed",
			map[string]PluginFactory{
				"a": getNaiveFilterPluginFactory(false),
				"b": getNaiveFilterPluginFactory(false),
				"c": getNaiveFilterPluginFactory(false),
			},
			framework.NewResult(framework.Error),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fwk, err := NewFramework(test.plugins, nil)
			if err != nil {
				t.Errorf("unexpected error when creating framework: %v", err)
			}

			result := fwk.RunFilterPlugins(context.Background(), nil, nil)
			if !reflect.DeepEqual(result, test.expectedResult) {
				t.Errorf("unexpected run filter plugin results: %v, want: %v", result, test.expectedResult)
			}
		})
	}
}
