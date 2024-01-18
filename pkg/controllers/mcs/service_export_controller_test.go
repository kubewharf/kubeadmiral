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

package mcs

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	secretObj = &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
	}
	nodeObj = &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar",
		},
	}
	roleObj = &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
	}
	clusterRoleObj = &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar",
		},
	}
)

func TestReconcileKeyFunc(t *testing.T) {
	tests := []struct {
		name                 string
		object               interface{}
		cluster              string
		expectErr            bool
		expectKeyStr         string
		expectNamespacedName string
	}{
		{
			name:      "namespace scoped resource in core group",
			object:    secretObj,
			cluster:   "member1",
			expectErr: false,
			expectKeyStr: "{\"cluster\": \"member1\", \"gvk\": \"/v1, Kind=Secret\", \"namespace\": \"foo\", " +
				"\"name\": \"bar\"}",
			expectNamespacedName: "foo/bar",
		},
		{
			name:      "cluster scoped resource in core group",
			object:    nodeObj,
			cluster:   "member1",
			expectErr: false,
			expectKeyStr: "{\"cluster\": \"member1\", \"gvk\": \"/v1, Kind=Node\", \"namespace\": \"\", " +
				"\"name\": \"bar\"}",
			expectNamespacedName: "bar",
		},
		{
			name:      "namespace scoped resource not in core group",
			object:    roleObj,
			cluster:   "member1",
			expectErr: false,
			expectKeyStr: "{\"cluster\": \"member1\", \"gvk\": \"rbac.authorization.k8s.io/v1, Kind=Role\", " +
				"\"namespace\": \"foo\", \"name\": \"bar\"}",
			expectNamespacedName: "foo/bar",
		},
		{
			name:      "cluster scoped resource not in core group",
			object:    clusterRoleObj,
			cluster:   "member1",
			expectErr: false,
			expectKeyStr: "{\"cluster\": \"member1\", \"gvk\": \"rbac.authorization.k8s.io/v1, Kind=Role\", " +
				"\"namespace\": \"\", \"name\": \"bar\"}",
			expectNamespacedName: "bar",
		},
		{
			name:      "non runtime object should be error",
			object:    "non-runtime-object",
			cluster:   "member1",
			expectErr: true,
		},
		{
			name:      "nil object should be error",
			object:    nil,
			cluster:   "member1",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := reconcileKeyFunc(tt.cluster, tt.object)
			if err != nil {
				if tt.expectErr == false {
					t.Fatalf("not expect error but got: %v", err)
				}
				return
			}

			if key.String() != tt.expectKeyStr {
				t.Fatalf("expect key string: %s, but got: %s", tt.expectKeyStr, key.String())
			}

			if key.NamespacedName() != tt.expectNamespacedName {
				t.Fatalf("expect namespaced name: %s, but got: %s", tt.expectNamespacedName, key.NamespacedName())
			}
		})
	}
}
