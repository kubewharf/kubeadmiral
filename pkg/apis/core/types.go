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

package core

import "k8s.io/apimachinery/pkg/util/sets"

type EnabledPlugins struct {
	FilterPlugins   []string
	ScorePlugins    []string
	SelectPlugins   []string
	ReplicasPlugins []string
}

func (ep EnabledPlugins) IsPluginEnabled(pluginName string) bool {
	if sets.New(ep.FilterPlugins...).Has(pluginName) {
		return true
	}
	if sets.New(ep.ScorePlugins...).Has(pluginName) {
		return true
	}
	if sets.New(ep.SelectPlugins...).Has(pluginName) {
		return true
	}
	if sets.New(ep.ReplicasPlugins...).Has(pluginName) {
		return true
	}

	return false
}
