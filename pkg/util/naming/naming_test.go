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

package naming

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateFederatedObjectName(t *testing.T) {
	type args struct {
		objectName string
		ftcName    string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "generate federated object name",
			args: args{
				objectName: "foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "foo-roles.rbac.authorization.k8s.io",
		},
		{
			name: "generate federated object name with consecutive .",
			args: args{
				objectName: "system...foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1857674172",
		},
		{
			name: "generate federated object name with :",
			args: args{
				objectName: "system:foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-2728495308",
		},
		{
			name: "generate federated object name with consecutive :",
			args: args{
				objectName: "system::foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-2999937238",
		},
		{
			name: "generate federated object name with $",
			args: args{
				objectName: "system$foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-4258037882",
		},
		{
			name: "generate federated object name with consecutive $",
			args: args{
				objectName: "system$foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-4258037882",
		},
		{
			name: "generate federated object name with %",
			args: args{
				objectName: "system%foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1244789457",
		},
		{
			name: "generate federated object name with consecutive %",
			args: args{
				objectName: "system%%%foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-4069727015",
		},
		{
			name: "generate federated object name with #",
			args: args{
				objectName: "system#foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1128546011",
		},
		{
			name: "generate federated object name with consecutive #",
			args: args{
				objectName: "system####foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-3227827662",
		},
		{
			name: "generate federated object name with upper case letter",
			args: args{
				objectName: "system#Foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1133665787",
		},
		{
			name: "generate federated object name with number",
			args: args{
				objectName: "system.foo123",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo123-roles.rbac.authorization.k8s.io",
		},
		{
			name: "generate federated object name for source object with long name",
			args: args{
				objectName: strings.Repeat("foo", 80),
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: strings.Repeat("foo", 80) + "-r-3980386512",
		},
		{
			name: "generate federated object name with transformation and truncation",
			args: args{
				objectName: strings.Repeat("system#foo", 25),
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: strings.Repeat("system.foo", 24) + "sys-552681660",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(
				t,
				tt.want,
				GenerateFederatedObjectName(tt.args.objectName, tt.args.ftcName),
				"GenerateFederatedObjectName(%v, %v)",
				tt.args.objectName,
				tt.args.ftcName,
			)
		})
	}
}

func TestGenerateImportedEndpointSliceName(t *testing.T) {
	tests := []struct {
		name              string
		endpointSliceName string
		cluster           string
		want              string
	}{
		{
			name:              "generate imported endpointSlice",
			endpointSliceName: "serve-94gdd",
			cluster:           "member-1",
			want:              "imported-member-1-serve-94gdd",
		},
		{
			name:              "generate imported endpointSlice with long name",
			endpointSliceName: strings.Repeat("foo", 80),
			cluster:           "member-123",
			want:              "imported-member-123-" + strings.Repeat("foo", 74) + "-4267687316",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(
				t,
				tt.want,
				GenerateImportedEndpointSliceName(tt.endpointSliceName, tt.cluster),
				"GenerateFederatedObjectName(%v, %v)",
				tt.endpointSliceName,
				tt.cluster,
			)
		})
	}
}

func TestGenerateDerivedSvcFedObjName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		want        string
	}{
		{
			name:        "generate derived service federated object",
			serviceName: "serve",
			want:        "derived-serve-services",
		},
		{
			name:        "generate derived service federated object with long name",
			serviceName: strings.Repeat("foo", 80),
			want:        "derived-" + strings.Repeat("foo", 78) + "-1019150234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(
				t,
				tt.want,
				GenerateDerivedSvcFedObjName(tt.serviceName),
				"GenerateDerivedSvcFedObjName(%v)",
				tt.serviceName,
			)
		})
	}
}

func Test_GenerateSourceClusterValue(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		value       string
	}{
		{
			name:        "cluster name less than 63",
			clusterName: "kubeadmiral-member-1",
			value:       "kubeadmiral-member-1",
		},
		{
			name:        "cluster name more than 63",
			clusterName: strings.Repeat("test", 80),
			value:       strings.Repeat("test", 13) + "t-988422597",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := GenerateSourceClusterValue(tt.clusterName)
			if val != tt.value {
				t.Errorf("GenerateSourceClusterValue() want %s, but got %s", tt.value, val)
			}
		})
	}
}
