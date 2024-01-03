/*
Copyright 2024 The KubeAdmiral Authors.

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

package plugins

import (
	"reflect"
	"testing"
)

func TestResolvePlugins(t *testing.T) {
	AddKatalystPluginIntoDefaultPlugins()
	defer func() {
		// recovery default plugins
		defaultPlugins = map[string]Plugin{}
	}()

	tests := []struct {
		name        string
		annotations map[string]string
		want        map[string]Plugin
		wantErr     bool
	}{
		{
			name:        "get plugins",
			annotations: map[string]string{ClusterResourcePluginsAnnotationKey: `["node.katalyst.kubewharf.io"]`},
			want:        map[string]Plugin{"node.katalyst.kubewharf.io": &katalystPlugin{}},
			wantErr:     false,
		},
		{
			name:        "get plugins with not existed plugins",
			annotations: map[string]string{ClusterResourcePluginsAnnotationKey: `["node.katalyst.kubewharf.io", "foo"]`},
			want:        map[string]Plugin{"node.katalyst.kubewharf.io": &katalystPlugin{}},
			wantErr:     false,
		},
		{
			name:        "get empty plugins",
			annotations: map[string]string{ClusterResourcePluginsAnnotationKey: `["foo"]`},
			want:        map[string]Plugin{},
			wantErr:     false,
		},
		{
			name:        "nil annotations",
			annotations: nil,
			want:        nil,
			wantErr:     false,
		},
		{
			name:        "plugin annotations not exists",
			annotations: map[string]string{"bar": `["foo"]`},
			want:        nil,
			wantErr:     false,
		},
		{
			name:        "failed to unmarshal",
			annotations: map[string]string{ClusterResourcePluginsAnnotationKey: `["foo",]`},
			want:        nil,
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePlugins(tt.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolvePlugins() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolvePlugins() got = %v, want %v", got, tt.want)
			}
		})
	}
}
