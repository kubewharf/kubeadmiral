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

package override

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

func TestLookForMatchedPolicies(t *testing.T) {
	type expectation struct {
		expectedPolicyKeys          []string
		expectedNeedsRecheckOnError bool
		isErrorExpected             bool
	}
	testCases := map[string]struct {
		obj                     metav1.ObjectMeta
		overridePolicies        []metav1.ObjectMeta
		clusterOverridePolicies []metav1.ObjectMeta
		expectation
	}{
		"namespaced - no labels specified - should find none": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels:    map[string]string{},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          nil,
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             false,
			},
		},
		"namespaced - references existent pp in same namespace - should find pp": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					OverridePolicyNameLabel: "pp1",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          []string{"default/pp1"},
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             false,
			},
		},
		"namespaced - references non-existent pp - should error": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					OverridePolicyNameLabel: "pp2",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          nil,
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             true,
			},
		},
		"namespaced - references existent pp in different namespace - should error": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					OverridePolicyNameLabel: "pp1",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "kube-public",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          nil,
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             true,
			},
		},
		"namespaced - references existent cpp - should find cpp": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					ClusterOverridePolicyNameLabel: "cpp1",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "kube-public",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          []string{"cpp1"},
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             false,
			},
		},
		"namespaced - references nonexistent cpp - should error": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					ClusterOverridePolicyNameLabel: "cpp2",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          nil,
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             true,
			},
		},
		"namespaced - both pp and cpp valid - should find [cpp, pp]": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					OverridePolicyNameLabel:        "pp1",
					ClusterOverridePolicyNameLabel: "cpp1",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          []string{"cpp1", "default/pp1"},
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             false,
			},
		},
		"namespaced - cpp invalid but pp valid - should error": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					OverridePolicyNameLabel:        "pp1",
					ClusterOverridePolicyNameLabel: "cpp2",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          nil,
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             true,
			},
		},
		"namespaced - cpp valid but pp invalid - should error": {
			obj: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					OverridePolicyNameLabel:        "pp2",
					ClusterOverridePolicyNameLabel: "cpp1",
				},
			},
			overridePolicies: []metav1.ObjectMeta{
				{
					Name:      "pp1",
					Namespace: "default",
				},
			},
			clusterOverridePolicies: []metav1.ObjectMeta{
				{
					Name: "cpp1",
				},
			},
			expectation: expectation{
				expectedPolicyKeys:          nil,
				expectedNeedsRecheckOnError: false,
				isErrorExpected:             true,
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: make(map[string]interface{})}
			isNamespaced := testCase.obj.Namespace != ""
			if isNamespaced {
				err := unstructured.SetNestedField(obj.Object, testCase.obj.Namespace, "metadata", "namespace")
				if err != nil {
					panic(err)
				}
			}
			err := unstructured.SetNestedStringMap(obj.Object, testCase.obj.Labels, "metadata", "labels")
			if err != nil {
				panic(err)
			}

			overridePolicyStore := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc)
			for _, opMeta := range testCase.overridePolicies {
				op := &fedcorev1a1.OverridePolicy{
					ObjectMeta: opMeta,
				}
				err := overridePolicyStore.Add(op)
				if err != nil {
					panic(err)
				}
			}

			clusterOverridePolicyStore := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc)
			for _, copMeta := range testCase.clusterOverridePolicies {
				cop := &fedcorev1a1.ClusterOverridePolicy{
					ObjectMeta: copMeta,
				}
				err := clusterOverridePolicyStore.Add(cop)
				if err != nil {
					panic(err)
				}
			}

			foundPolicies, needsRecheckOnError, err := lookForMatchedPolicies(
				obj,
				isNamespaced,
				overridePolicyStore,
				clusterOverridePolicyStore,
			)
			if (err != nil) != testCase.isErrorExpected {
				t.Fatalf("err = %v, but isErrorExpected = %v", err, testCase.isErrorExpected)
			}

			if err != nil && needsRecheckOnError != testCase.expectedNeedsRecheckOnError {
				t.Fatalf("expected needsRecheckOnError to be %v, but got %v", testCase.expectedNeedsRecheckOnError, needsRecheckOnError)
			}

			var foundPolicyKeys []string
			for _, foundPolicy := range foundPolicies {
				key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(foundPolicy)
				if err != nil {
					panic(err)
				}
				foundPolicyKeys = append(foundPolicyKeys, key)
			}

			assert.Equal(t, testCase.expectedPolicyKeys, foundPolicyKeys, "expected and actual policy keys differ")
		})
	}
}

func TestParseOverrides(t *testing.T) {
	testCases := map[string]struct {
		policy               fedcorev1a1.GenericOverridePolicy
		clusters             []*fedcorev1a1.FederatedCluster
		expectedOverridesMap util.OverridesMap
		isErrorExpected      bool
	}{
		"no clusters - should return no overrides": {
			policy: &fedcorev1a1.OverridePolicy{
				Spec: fedcorev1a1.GenericOverridePolicySpec{
					OverrideRules: []fedcorev1a1.OverrideRule{
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								ClusterSelector: map[string]string{},
							},
						},
					},
				},
			},
			clusters:             nil,
			expectedOverridesMap: make(util.OverridesMap),
			isErrorExpected:      false,
		},
		"invalid clusterSelector - should return error": {
			policy: &fedcorev1a1.OverridePolicy{
				Spec: fedcorev1a1.GenericOverridePolicySpec{
					OverrideRules: []fedcorev1a1.OverrideRule{
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								ClusterSelector: map[string]string{
									strings.Repeat("a", 100): strings.Repeat("b", 100),
								},
							},
						},
					},
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster1",
					},
				},
			},
			expectedOverridesMap: nil,
			isErrorExpected:      true,
		},
		"single cluster no OverrideRules - should return no overrides": {
			policy: &fedcorev1a1.OverridePolicy{
				Spec: fedcorev1a1.GenericOverridePolicySpec{
					OverrideRules: []fedcorev1a1.OverrideRule{},
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster1",
					},
				},
			},
			expectedOverridesMap: make(util.OverridesMap),
			isErrorExpected:      false,
		},
		"single cluster multiple OverrideRules - should return overrides from matched rules in order": {
			policy: &fedcorev1a1.OverridePolicy{
				Spec: fedcorev1a1.GenericOverridePolicySpec{
					OverrideRules: []fedcorev1a1.OverrideRule{
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								// should match all clusters
								ClusterSelector: map[string]string{},
							},
							Overriders: &fedcorev1a1.Overriders{
								JsonPatch: []fedcorev1a1.JsonPatchOverrider{
									{
										Operator: "add",
										Path:     "/a/b",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`1`),
										},
									},
									{
										Operator: "replace",
										Path:     "/aa/bb",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`["banana", "mango"]`),
										},
									},
								},
							},
						},
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								Clusters: []string{
									"fake",
								},
							},
							Overriders: &fedcorev1a1.Overriders{
								JsonPatch: []fedcorev1a1.JsonPatchOverrider{
									{
										Operator: "replace",
										Path:     "/c/d",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`1`),
										},
									},
								},
							},
						},
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								Clusters: []string{
									"cluster1",
								},
							},
							Overriders: &fedcorev1a1.Overriders{
								JsonPatch: []fedcorev1a1.JsonPatchOverrider{
									{
										Operator: "remove",
										Path:     "/e/f",
									},
									{
										Operator: "add",
										Path:     "/ee/ff",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`"some string"`),
										},
									},
								},
							},
						},
					},
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster1",
					},
				},
			},
			expectedOverridesMap: util.OverridesMap{
				"cluster1": fedtypesv1a1.OverridePatches{
					{
						Op:    "add",
						Path:  "/a/b",
						Value: float64(1),
					},
					{
						Op:    "replace",
						Path:  "/aa/bb",
						Value: []interface{}{"banana", "mango"},
					},
					{
						Op:   "remove",
						Path: "/e/f",
					},
					{
						Op:    "add",
						Path:  "/ee/ff",
						Value: "some string",
					},
				},
			},
			isErrorExpected: false,
		},
		"multiple clusters multiple Overrides - should return overrides for each cluster in order": {
			policy: &fedcorev1a1.OverridePolicy{
				Spec: fedcorev1a1.GenericOverridePolicySpec{
					OverrideRules: []fedcorev1a1.OverrideRule{
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								// should match all clusters
								ClusterSelector: map[string]string{},
							},
							Overriders: &fedcorev1a1.Overriders{
								JsonPatch: []fedcorev1a1.JsonPatchOverrider{
									{
										Operator: "add",
										Path:     "/a/b",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`1`),
										},
									},
									{
										Operator: "replace",
										Path:     "/aa/bb",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`["banana", "mango"]`),
										},
									},
								},
							},
						},
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								Clusters: []string{
									"cluster1",
								},
							},
							Overriders: &fedcorev1a1.Overriders{
								JsonPatch: []fedcorev1a1.JsonPatchOverrider{
									{
										Operator: "replace",
										Path:     "/c/d",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`1`),
										},
									},
									{
										Operator: "replace",
										Path:     "/cc/dd",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`{"key": "value"}`),
										},
									},
								},
							},
						},
						{
							TargetClusters: &fedcorev1a1.TargetClusters{
								Clusters: []string{
									"cluster2",
								},
							},
							Overriders: &fedcorev1a1.Overriders{
								JsonPatch: []fedcorev1a1.JsonPatchOverrider{
									{
										Operator: "remove",
										Path:     "/e/f",
									},
									{
										Operator: "add",
										Path:     "/ee/ff",
										Value: apiextensionsv1.JSON{
											Raw: []byte(`"some string"`),
										},
									},
								},
							},
						},
					},
				},
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster2",
					},
				},
			},
			expectedOverridesMap: util.OverridesMap{
				"cluster1": fedtypesv1a1.OverridePatches{
					{
						Op:    "add",
						Path:  "/a/b",
						Value: float64(1),
					},
					{
						Op:    "replace",
						Path:  "/aa/bb",
						Value: []interface{}{"banana", "mango"},
					},
					{
						Op:    "replace",
						Path:  "/c/d",
						Value: float64(1),
					},
					{
						Op:   "replace",
						Path: "/cc/dd",
						Value: map[string]interface{}{
							"key": "value",
						},
					},
				},
				"cluster2": fedtypesv1a1.OverridePatches{
					{
						Op:    "add",
						Path:  "/a/b",
						Value: float64(1),
					},
					{
						Op:    "replace",
						Path:  "/aa/bb",
						Value: []interface{}{"banana", "mango"},
					},
					{
						Op:   "remove",
						Path: "/e/f",
					},
					{
						Op:    "add",
						Path:  "/ee/ff",
						Value: "some string",
					},
				},
			},
			isErrorExpected: false,
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			overrides, err := parseOverrides(testCase.policy, testCase.clusters)
			if (err != nil) != testCase.isErrorExpected {
				t.Fatalf("err = %v, but testCase.isErrorExpected = %v", err, testCase.isErrorExpected)
			}

			assert.Equal(t, testCase.expectedOverridesMap, overrides)
		})
	}
}

func TestIsClusterMatched(t *testing.T) {
	testCases := map[string]struct {
		targetClusters *fedcorev1a1.TargetClusters
		cluster        *fedcorev1a1.FederatedCluster
		shouldMatch    bool
	}{
		"nil targetClusters - should match any cluster": {
			targetClusters: nil,
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"clusterNames should match a clusters with matching name": {
			targetClusters: &fedcorev1a1.TargetClusters{
				Clusters: []string{"cluster1", "cluster2"},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"clusterNames should not match a clusters without matching name": {
			targetClusters: &fedcorev1a1.TargetClusters{
				Clusters: []string{"cluster1", "cluster2"},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster3",
					Labels: nil,
				},
			},
			shouldMatch: false,
		},
		"nil clusterNames should match any cluster": {
			targetClusters: &fedcorev1a1.TargetClusters{
				Clusters: nil,
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"empty clusterNames should match any cluster": {
			targetClusters: &fedcorev1a1.TargetClusters{
				Clusters: []string{},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"empty selector should select all clusters": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterSelector: map[string]string{},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"labels selectors should select clusters with the label": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"foo": "bar",
					},
				},
			},
			shouldMatch: true,
		},
		"labels selectors should not select clusters without the label": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"qux": "baz",
					},
				},
			},
			shouldMatch: false,
		},
		"labels selectors should not select clusters with the label key but different value": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"foo": "lol",
					},
				},
			},
			shouldMatch: false,
		},
		"nil cluster affinity should select any cluster": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: nil,
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"foo": "lol",
					},
				},
			},
			shouldMatch: true,
		},
		"empty cluster affinity should select any cluster": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"foo": "lol",
					},
				},
			},
			shouldMatch: true,
		},
		"single MatchExpressions requirement in clusterAffinity should select clusters with label match": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "zone",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values: []string{
									"north",
									"south",
								},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"zone": "north",
					},
				},
			},
			shouldMatch: true,
		},
		"single MatchExpressions requirement in clusterAffinity should not select clusters without label match": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "zone",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values: []string{
									"north",
									"south",
								},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"zone": "west",
					},
				},
			},
			shouldMatch: false,
		},
		"single MatchExpression requirement in clusterAffinity should select clusters with field match": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"cluster1"},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"single MatchExpression requirement in clusterAffinity should not select clusters without field match": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"cluster2"},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: false,
		},
		"multiple requirements in clusterAffinity should be ANDed": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						// should not match
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "foo",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"bar"},
							},
						},
						// should match
						MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"cluster1"},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: false,
		},
		"multiple terms in clusterAffinity should be ORed": {
			targetClusters: &fedcorev1a1.TargetClusters{
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					// should not match
					{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "foo",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"bar"},
							},
						},
					},
					// should match
					{
						MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"cluster1"},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster1",
					Labels: nil,
				},
			},
			shouldMatch: true,
		},
		"clusterName, clusterSelector and clusterAffinity should be ANDed": {
			targetClusters: &fedcorev1a1.TargetClusters{
				// should match
				Clusters: []string{"cluster1"},
				// should match
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
				// should not match
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "foo",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"quux"},
							},
						},
					},
				},
			},
			cluster: &fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster1",
					Labels: map[string]string{
						"foo": "bar",
					},
				},
			},
			shouldMatch: false,
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			matched, err := isClusterMatched(testCase.targetClusters, testCase.cluster)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if matched != testCase.shouldMatch {
				t.Fatalf("expected matched = %v, but actual matched = %v", testCase.shouldMatch, matched)
			}
		})
	}
}

func TestMergeOverrides(t *testing.T) {
	testCases := map[string]struct {
		dst            util.OverridesMap
		src            util.OverridesMap
		expectedResult util.OverridesMap
	}{
		"nil dst - result should be equivalent to src": {
			dst: nil,
			src: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 1,
					},
				},
				"cluster2": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 2,
					},
				},
			},
			expectedResult: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 1,
					},
				},
				"cluster2": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 2,
					},
				},
			},
		},
		"non-nil dst - override patches for the same cluster should be appended from src": {
			dst: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 1,
					},
				},
			},
			src: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "add",
						Path:  "/spec/replicas",
						Value: 10,
					},
				},
			},
			expectedResult: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 1,
					},
					{
						Op:    "add",
						Path:  "/spec/replicas",
						Value: 10,
					},
				},
			},
		},
		"non-nil dst - existing overrides patches should be kept": {
			dst: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 1,
					},
				},
				"cluster2": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 2,
					},
				},
			},
			src: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 3,
					},
				},
			},
			expectedResult: util.OverridesMap{
				"cluster1": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 1,
					},
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 3,
					},
				},
				"cluster2": []fedtypesv1a1.OverridePatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 2,
					},
				},
			},
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			result := mergeOverrides(testCase.dst, testCase.src)
			assert.Equal(t, testCase.expectedResult, result)
		})
	}
}
