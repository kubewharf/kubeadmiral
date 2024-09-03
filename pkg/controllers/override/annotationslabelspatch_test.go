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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type stringMapTestCases map[string]struct {
	fedObject               fedcorev1a1.GenericFederatedObject
	stringMapOverriders     []fedcorev1a1.StringMapOverrider
	expectedOverridePatches fedcorev1a1.OverridePatches
	isErrorExpected         bool
}

func Test_parseAnnotationsOrLabelsOverriders(t *testing.T) {
	testTargets := []string{AnnotationsTarget, LabelsTarget}
	generateTestCases := func(testTarget string) stringMapTestCases {
		return map[string]struct {
			fedObject               fedcorev1a1.GenericFederatedObject
			stringMapOverriders     []fedcorev1a1.StringMapOverrider
			expectedOverridePatches fedcorev1a1.OverridePatches
			isErrorExpected         bool
		}{
			// Test workload annotations or labels scenarios
			// addIfAbsent
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: empty, key conflict: false, operator: addIfAbsent", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, nil),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorAddIfAbsent, map[string]string{
						"abc": "xxx",
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{"abc": "xxx", "def": "xxx"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: non-empty, key conflict: false, operator: addIfAbsent", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorAddIfAbsent, map[string]string{
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{"abc": "xxx", "def": "xxx"}),
				},
				isErrorExpected: false,
			},

			// overwrite
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: empty, key conflict: false, operator: empty", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, nil),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider("", map[string]string{
						"abc": "xxx",
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: empty, key conflict: false, operator: overwrite", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, nil),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorOverwrite, map[string]string{
						"abc": "xxx",
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: non-empty, "+
				"key conflict: false, operator: overwrite", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
					"def": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorOverwrite, map[string]string{
						"ghi": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{"abc": "xxx", "def": "xxx"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: non-empty, key conflict: true, operator: overwrite", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorOverwrite, map[string]string{
						"abc": "test",
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{"abc": "test"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment without value, originValue: non-empty, operator: overwrite", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorOverwrite, map[string]string{}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{"abc": "xxx"}),
				},
				isErrorExpected: false,
			},

			// delete
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: empty, key conflict: false, operator: delete", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, nil),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorDelete, map[string]string{
						"abc": "test",
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: non-empty, key conflict: false, operator: delete", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorDelete, map[string]string{
						"def": "xxx",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{
							"abc": "xxx",
						}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: non-empty, key conflict: true, operator: delete", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
					"def": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorDelete, map[string]string{
						"def": "",
					}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{
							"abc": "xxx",
						}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment without value, originValue: non-empty, operator: delete", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{
					"abc": "xxx",
				}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorDelete, map[string]string{}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/metadata/%s", testTarget),
						map[string]string{"abc": "xxx"}),
				},
				isErrorExpected: false,
			},

			// Test error scenarios
			fmt.Sprintf("apply %sOverriders to Deployment, originValue: non-empty, key conflict: true, operator: addIfAbsent", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{"abc": "xxx"}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorAddIfAbsent, map[string]string{
						"abc": "xxx",
						"def": "xxx",
					}),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			fmt.Sprintf("apply %sOverriders to Deployment without value, originValue: non-empty, operator: addIfAbsent", testTarget): {
				fedObject: generateFedWorkloadWithAnnotationsOrLabels(common.DeploymentKind, testTarget, map[string]string{"abc": "xxx"}),
				stringMapOverriders: []fedcorev1a1.StringMapOverrider{
					generateStringMapOverrider(OperatorAddIfAbsent, map[string]string{}),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
		}
	}
	for _, testTarget := range testTargets {
		testCases := generateTestCases(testTarget)
		for testName, testCase := range testCases {
			t.Run(testName, func(t *testing.T) {
				helper, err := getHelpDataFromFedObj(testCase.fedObject)
				if err != nil {
					t.Fatalf("failed to construct helper: %v", err)
				}
				overridePatches, err := parseStringMapOverriders(helper, testCase.stringMapOverriders, testTarget)
				if (err != nil) != testCase.isErrorExpected {
					t.Fatalf("err = %v, but testCase.isErrorExpected = %v", err, testCase.isErrorExpected)
				}

				assert.Equal(t, testCase.expectedOverridePatches, overridePatches)
			})
		}
	}
}

func generateStringMapOverrider(operator string, values map[string]string) fedcorev1a1.StringMapOverrider {
	return fedcorev1a1.StringMapOverrider{
		Operator: operator,
		Value:    values,
	}
}

func generateFedWorkloadWithAnnotationsOrLabels(kind string, target string, values map[string]string) *fedcorev1a1.FederatedObject {
	var workload *unstructured.Unstructured
	switch kind {
	case common.DeploymentKind:
		workload = basicDeploymentTemplate.DeepCopy()
	case common.DaemonSetKind:
		workload = basicDaemonSetTemplate.DeepCopy()
	case common.StatefulSetKind:
		workload = basicStatefulSetTemplate.DeepCopy()
	case common.JobKind:
		workload = basicJobTemplate.DeepCopy()
	case common.CronJobKind:
		workload = basicCronJobTemplate.DeepCopy()
	case common.PodKind:
		workload = basicPodTemplate.DeepCopy()
	}

	if target == AnnotationsTarget {
		workload.SetAnnotations(values)
	} else {
		workload.SetLabels(values)
	}

	return generateFedObj(workload)
}
