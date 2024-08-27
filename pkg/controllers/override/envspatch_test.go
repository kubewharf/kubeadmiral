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

package override

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func generateEnvOverrider(containerName, operator string, values ...corev1.EnvVar) fedcorev1a1.EnvOverrider {
	return fedcorev1a1.EnvOverrider{
		ContainerName: containerName,
		Operator:      operator,
		Value:         values,
	}
}

func generateFedWorkloadWithEnvs(
	kind string, containerNum, initContainerNum int, values []interface{},
) *fedcorev1a1.FederatedObject {
	var workload *unstructured.Unstructured
	initPath := []string{"spec", "template", "spec", "initContainers"}
	path := []string{"spec", "template", "spec", "containers"}
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
		initPath = []string{"spec", "jobTemplate", "spec", "template", "spec", "initContainers"}
		path = []string{"spec", "jobTemplate", "spec", "template", "spec", "containers"}
	case common.PodKind:
		workload = basicPodTemplate.DeepCopy()
		initPath = []string{"spec", "initContainers"}
		path = []string{"spec", "containers"}
	}

	var containers, initContainers []interface{}

	for i := 0; i < containerNum; i++ {
		containers = append(containers, generateContainerWithEnvs(fmt.Sprintf("container-%d", i), values))
	}
	for i := 0; i < initContainerNum; i++ {
		initContainers = append(initContainers, generateContainerWithEnvs(fmt.Sprintf("init-container-%d", i), values))
	}

	if len(initContainers) > 0 {
		_ = unstructured.SetNestedSlice(
			workload.Object, initContainers, initPath...)
	}
	if len(containers) > 0 {
		_ = unstructured.SetNestedSlice(
			workload.Object, containers, path...)
	}

	return generateFedObj(workload)
}

func generateContainerWithEnvs(containerName string, values []interface{}) map[string]interface{} {
	container := map[string]interface{}{"name": containerName}
	if len(values) > 0 {
		container["env"] = values
	}
	return container
}

type envTestCases map[string]struct {
	fedObject               fedcorev1a1.GenericFederatedObject
	envOverriders           []fedcorev1a1.EnvOverrider
	expectedOverridePatches fedcorev1a1.OverridePatches
	isErrorExpected         bool
}

func Test_parseEnvOverriders(t *testing.T) {
	generateTestCases := func() envTestCases {
		return map[string]struct {
			fedObject               fedcorev1a1.GenericFederatedObject
			envOverriders           []fedcorev1a1.EnvOverrider
			expectedOverridePatches fedcorev1a1.OverridePatches
			isErrorExpected         bool
		}{
			// Test workload's envs scenarios
			// test operations on workload(one container)
			// Deployment addIfAbsent, overwrite, delete
			"apply envOverriders to Deployment(one container), originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}, corev1.EnvVar{Name: "b", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", Value: "b"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Deployment(one container), originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "b", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", Value: "b"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Deployment(one container), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Deployment(one container), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "b"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Deployment(one container), originValue: empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Deployment(one container), originValue: non-empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env", nil),
				},
				isErrorExpected: false,
			},

			// DaemonSet addIfAbsent, overwrite, delete
			"apply envOverriders to DaemonSet(one container), originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.DaemonSetKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to DaemonSet(one container), originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.DaemonSetKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "b", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to DaemonSet(one container), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.DaemonSetKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to DaemonSet(one container), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.DaemonSetKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to DaemonSet(one container), originValue: empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.DaemonSetKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to DaemonSet(one container), originValue: non-empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.DaemonSetKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},

			// StatefulSet addIfAbsent, overwrite, delete
			"apply envOverriders to StatefulSet(one container), originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.StatefulSetKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to StatefulSet(one container), originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.StatefulSetKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "b", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to StatefulSet(one container), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.StatefulSetKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to StatefulSet(one container), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.StatefulSetKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to StatefulSet(one container), originValue: empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.StatefulSetKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to StatefulSet(one container), originValue: non-empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.StatefulSetKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},

			// Job addIfAbsent, overwrite, delete
			"apply envOverriders to Job(one container), originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.JobKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Job(one container), originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.JobKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "b", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Job(one container), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.JobKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Job(one container), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.JobKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Job(one container), originValue: empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.JobKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Job(one container), originValue: non-empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.JobKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},

			// CronJob addIfAbsent, overwrite, delete
			"apply envOverriders to CronJob(one container), originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.CronJobKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/jobTemplate/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to CronJob(one container), originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.CronJobKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "b", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/jobTemplate/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to CronJob(one container), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.CronJobKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/jobTemplate/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to CronJob(one container), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.CronJobKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/jobTemplate/spec/template/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to CronJob(one container), originValue: empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.CronJobKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/jobTemplate/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to CronJob(one container), originValue: non-empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.CronJobKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/jobTemplate/spec/template/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},

			// Pod addIfAbsent, overwrite, delete
			"apply envOverriders to Pod(one container), originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(one container), originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "b", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "a"}, {Name: "b", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(one container), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(one container), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "b",
								},
								Key: "b",
							},
						}}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(one container), originValue: empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(one container), originValue: non-empty, operator: delete": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "b",
							},
							Key: "b",
						},
					}}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						nil),
				},
				isErrorExpected: false,
			},

			// test operations on workload(multiple container)
			"apply envOverriders to Pod(one initContainer of multiple containers), originValue: empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 2, 2, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("init-container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/initContainers/0/env",
						nil),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(one initContainer of multiple containers), originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 2, 2, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("init-container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/initContainers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "b"}}),
				},
				isErrorExpected: false,
			},
			"apply envOverriders to Pod(nomatched container of multiple containers), " +
				"originValue: non-empty, operator: overwrite": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 2, 2, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "b"}),
					generateEnvOverrider("container-1", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "b"}}),
					generatePatch("replace",
						"/spec/containers/1/env",
						[]corev1.EnvVar{{Name: "a", Value: "b"}}),
				},
				isErrorExpected: false,
			},

			// Test overwrite multiple envs
			"apply envOverriders to Pod(one container), operator: overwrite, multiple envs": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
					map[string]interface{}{
						"name":  "b",
						"value": "b",
					},
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
					map[string]interface{}{
						"name":  "c",
						"value": "c",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "a", Value: "b"}, {Name: "b", Value: "b"}, {Name: "a", Value: "b"}, {Name: "c", Value: "c"}}),
				},
				isErrorExpected: false,
			},

			// Test delete multiple envs
			"apply envOverriders to Pod(one container), operator: delete, multiple envs": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
					map[string]interface{}{
						"name":  "b",
						"value": "b",
					},
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
					map[string]interface{}{
						"name":  "c",
						"value": "c",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "b", Value: "b"}, {Name: "c", Value: "c"}}),
				},
				isErrorExpected: false,
			},

			// Test multiple operations
			"apply envOverriders to Pod(one container), multiple operations, multiple envs": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
					map[string]interface{}{
						"name":  "b",
						"value": "b",
					},
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
					map[string]interface{}{
						"name":  "c",
						"value": "c",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "d", Value: "d"}, corev1.EnvVar{Name: "e", Value: "e"}),
					generateEnvOverrider("container-0", OperatorOverwrite, corev1.EnvVar{Name: "b", Value: "c"},
						corev1.EnvVar{Name: "c", Value: "e"}, corev1.EnvVar{Name: "a", Value: "f"}),
					generateEnvOverrider("container-0", OperatorDelete, corev1.EnvVar{Name: "a", Value: "c"}, corev1.EnvVar{Name: "e", Value: "e"}),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						"/spec/containers/0/env",
						[]corev1.EnvVar{{Name: "b", Value: "c"}, {Name: "c", Value: "e"}, {Name: "d", Value: "d"}}),
				},
				isErrorExpected: false,
			},

			// Test error scenarios
			// test unsupported operator
			"apply envOverriders to Deployment(one container), originValue: empty, operator: invalid": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", "invalid", corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			// test addIfAbsent operation on deployment without value
			"apply envOverriders to Deployment(one container) without value, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.DeploymentKind, 1, 0, nil),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			// test apply envOverrider to unsupported resources
			"apply envOverriders to ArgoWorkflow, operator: addIfAbsent": {
				fedObject: generateFedArgoWorkflowWithImage(""),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			// Test duplicate item in the list
			"apply envOverriders to Pod(one container) with duplicate items, originValue: non-empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{
					map[string]interface{}{
						"name":  "a",
						"value": "a",
					},
				}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			"apply envOverriders to Pod(one container) with duplicate items, originValue: empty, operator: addIfAbsent": {
				fedObject: generateFedWorkloadWithEnvs(common.PodKind, 1, 0, []interface{}{}),
				envOverriders: []fedcorev1a1.EnvOverrider{
					generateEnvOverrider("container-0", OperatorAddIfAbsent, corev1.EnvVar{Name: "a", Value: "a"}, corev1.EnvVar{Name: "a", Value: "b"}),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
		}
	}

	for testName, testCase := range generateTestCases() {
		t.Run(testName, func(t *testing.T) {
			overridePatches, err := parseEnvOverriders(testCase.fedObject, testCase.envOverriders)
			if (err != nil) != testCase.isErrorExpected {
				t.Fatalf("err = %v, but testCase.isErrorExpected = %v", err, testCase.isErrorExpected)
			}

			assert.Equal(t, testCase.expectedOverridePatches, overridePatches)
		})
	}
}
