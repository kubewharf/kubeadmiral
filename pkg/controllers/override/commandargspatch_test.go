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

func generateEntrypointOverrider(containerName, operator string, values ...string) fedcorev1a1.EntrypointOverrider {
	return fedcorev1a1.EntrypointOverrider{
		ContainerName: containerName,
		Operator:      operator,
		Value:         values,
	}
}

func generateFedWorkloadWithCommandOrArgs(
	kind string, target string, containerNum, initContainerNum int, values ...interface{},
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
		containers = append(containers, generateContainerWithCommandOrArgs(fmt.Sprintf("container-%d", i), target, values))
	}
	for i := 0; i < initContainerNum; i++ {
		initContainers = append(initContainers, generateContainerWithCommandOrArgs(fmt.Sprintf("init-container-%d", i), target, values))
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

func generateContainerWithCommandOrArgs(containerName string, target string, values []interface{}) map[string]interface{} {
	container := map[string]interface{}{"name": containerName}
	if (target == CommandTarget || target == ArgsTarget) && len(values) > 0 {
		container[target] = values
	}
	return container
}

type entrypointTestCases map[string]struct {
	fedObject               fedcorev1a1.GenericFederatedObject
	entrypointOverriders    []fedcorev1a1.EntrypointOverrider
	expectedOverridePatches fedcorev1a1.OverridePatches
	isErrorExpected         bool
}

func Test_parseCommandOrArgsOverriders(t *testing.T) {
	testTargets := []string{CommandTarget, ArgsTarget}
	generateTestCases := func(target string) entrypointTestCases {
		return map[string]struct {
			fedObject               fedcorev1a1.GenericFederatedObject
			entrypointOverriders    []fedcorev1a1.EntrypointOverrider
			expectedOverridePatches fedcorev1a1.OverridePatches
			isErrorExpected         bool
		}{
			// Test workload's command or args scenarios
			// test operations on workload(one container)
			// Deployment append, overwrite, delete
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: non-empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15", "/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						nil),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: non-empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "echo"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"sleep 15"}),
				},
				isErrorExpected: false,
			},

			// DaemonSet append, overwrite, delete
			fmt.Sprintf("apply %sOverriders to DaemonSet(one container), originValue: empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DaemonSetKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to DaemonSet(one container), originValue: non-empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DaemonSetKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15", "/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to DaemonSet(one container), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DaemonSetKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to DaemonSet(one container), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DaemonSetKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to DaemonSet(one container), originValue: empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DaemonSetKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						nil),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to DaemonSet(one container), originValue: non-empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DaemonSetKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "echo"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"sleep 15"}),
				},
				isErrorExpected: false,
			},

			// StatefulSet append, overwrite, delete
			fmt.Sprintf("apply %sOverriders to StatefulSet(one container), originValue: empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.StatefulSetKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to StatefulSet(one container), originValue: non-empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.StatefulSetKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15", "/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to StatefulSet(one container), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.StatefulSetKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to StatefulSet(one container), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.StatefulSetKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to StatefulSet(one container), originValue: empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.StatefulSetKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						nil),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to StatefulSet(one container), originValue: non-empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.StatefulSetKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "echo"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"sleep 15"}),
				},
				isErrorExpected: false,
			},

			// Job append, overwrite, delete
			fmt.Sprintf("apply %sOverriders to Job(one container), originValue: empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.JobKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Job(one container), originValue: non-empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.JobKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15", "/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Job(one container), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.JobKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Job(one container), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.JobKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Job(one container), originValue: empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.JobKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						nil),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Job(one container), originValue: non-empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.JobKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "echo"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/template/spec/containers/0/%s", target),
						[]string{"sleep 15"}),
				},
				isErrorExpected: false,
			},

			// CronJob append, overwrite, delete
			fmt.Sprintf("apply %sOverriders to CronJob(one container), originValue: empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.CronJobKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/jobTemplate/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to CronJob(one container), originValue: non-empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.CronJobKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/jobTemplate/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15", "/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to CronJob(one container), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.CronJobKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/jobTemplate/spec/template/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to CronJob(one container), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.CronJobKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/jobTemplate/spec/template/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to CronJob(one container), originValue: empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.CronJobKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/jobTemplate/spec/template/spec/containers/0/%s", target),
						nil),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to CronJob(one container), originValue: non-empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.CronJobKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "echo"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/jobTemplate/spec/template/spec/containers/0/%s", target),
						[]string{"sleep 15"}),
				},
				isErrorExpected: false,
			},

			// Pod append, overwrite, delete
			fmt.Sprintf("apply %sOverriders to Pod(one container), originValue: empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one container), originValue: non-empty, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15", "/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one container), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one container), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one container), originValue: empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						nil),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one container), originValue: non-empty, operator: delete", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorDelete, "/bin/bash", "echo"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"sleep 15"}),
				},
				isErrorExpected: false,
			},

			// test operations on workload(multiple container)
			fmt.Sprintf("apply %sOverriders to Pod(one container of multiple containers), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 2, 2),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one container of multiple containers), originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 2, 2, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/containers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one initContainer of multiple containers), originValue: empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 2, 2),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("init-container-0", OperatorOverwrite, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/initContainers/0/%s", target),
						[]string{"/bin/bash", "sleep 15"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(one initContainer of multiple containers), originValue: non-empty, "+
				"operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 2, 2, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("init-container-0", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{
					generatePatch("replace",
						fmt.Sprintf("/spec/initContainers/0/%s", target),
						[]string{"echo", "world"}),
				},
				isErrorExpected: false,
			},
			fmt.Sprintf("apply %sOverriders to Pod(nomatched container of multiple containers), "+
				"originValue: non-empty, operator: overwrite", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.PodKind, target, 2, 2, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("test", OperatorOverwrite, "echo", "world"),
					generateEntrypointOverrider("test", OperatorOverwrite, "echo", "world"),
				},
				expectedOverridePatches: fedcorev1a1.OverridePatches{},
				isErrorExpected:         false,
			},

			// Test error scenarios
			// test unsupported operator
			fmt.Sprintf("apply %sOverriders to Deployment(one container), originValue: empty, operator: invalid", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", "invalid", "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			// test append operation on deployment without value
			fmt.Sprintf("apply %sOverriders to Deployment(one container) without value, operator: append", target): {
				fedObject: generateFedWorkloadWithCommandOrArgs(common.DeploymentKind, target, 1, 0, "/bin/bash", "sleep 15"),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
			// test apply entrypointOverrider to unsupported resources
			fmt.Sprintf("apply %sOverriders to ArgoWorkflow, operator: append", target): {
				fedObject: generateFedArgoWorkflowWithImage(""),
				entrypointOverriders: []fedcorev1a1.EntrypointOverrider{
					generateEntrypointOverrider("container-0", OperatorAppend, "/bin/bash", "sleep 15"),
				},
				expectedOverridePatches: nil,
				isErrorExpected:         true,
			},
		}
	}

	for _, testTarget := range testTargets {
		for testName, testCase := range generateTestCases(testTarget) {
			t.Run(testName, func(t *testing.T) {
				overridePatches, err := parseEntrypointOverriders(testCase.fedObject, testCase.entrypointOverriders, testTarget)
				if (err != nil) != testCase.isErrorExpected {
					t.Fatalf("err = %v, but testCase.isErrorExpected = %v", err, testCase.isErrorExpected)
				}

				assert.Equal(t, testCase.expectedOverridePatches, overridePatches)
			})
		}
	}
}
