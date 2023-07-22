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
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/pointer"
)

func TestMatchedPolicyKey(t *testing.T) {
	testCases := map[string]struct {
		objectNamespace string
		ppLabelValue    *string
		cppLabelValue   *string

		expectedPolicyFound     bool
		expectedPolicyName      string
		expectedPolicyNamespace string
	}{
		"namespaced object does not reference any policy": {
			objectNamespace:     "default",
			expectedPolicyFound: false,
		},
		"cluster-scoped object does not reference any policy": {
			expectedPolicyFound: false,
		},
		"namespaced object references pp in same namespace": {
			objectNamespace:         "default",
			ppLabelValue:            pointer.String("pp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "pp1",
			expectedPolicyNamespace: "default",
		},
		"namespaced object references cpp": {
			objectNamespace:         "default",
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
		"namespaced object references both pp and cpp": {
			objectNamespace:         "default",
			ppLabelValue:            pointer.String("pp1"),
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "pp1",
			expectedPolicyNamespace: "default",
		},
		"cluster-scoped object references pp": {
			ppLabelValue:        pointer.String("pp1"),
			expectedPolicyFound: false,
		},
		"cluster-scoped object references cpp": {
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
		"cluster-scoped object references both pp and cpp": {
			ppLabelValue:            pointer.String("pp1"),
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: make(map[string]interface{})}
			object.SetNamespace(testCase.objectNamespace)
			labels := map[string]string{}
			if testCase.ppLabelValue != nil {
				labels[PropagationPolicyNameLabel] = *testCase.ppLabelValue
			}
			if testCase.cppLabelValue != nil {
				labels[ClusterPropagationPolicyNameLabel] = *testCase.cppLabelValue
			}
			object.SetLabels(labels)

			policy, found := GetMatchedPolicyKey(object, object.GetNamespace() != "")
			if found != testCase.expectedPolicyFound {
				t.Fatalf("found = %v, but expectedPolicyFound = %v", found, testCase.expectedPolicyFound)
			}

			if !found {
				return
			}

			if policy.Name != testCase.expectedPolicyName {
				t.Fatalf("policyName = %s, but expectedPolicyName = %s", policy.Name, testCase.expectedPolicyName)
			}
			if policy.Namespace != testCase.expectedPolicyNamespace {
				t.Fatalf("policyNamespace = %v, but expectedPolicyNamespace = %v", policy.Namespace, testCase.expectedPolicyNamespace)
			}
		})
	}
}
