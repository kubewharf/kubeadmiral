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
	"testing"

	"github.com/stretchr/testify/assert"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func Test_parseImageOverriders(t *testing.T) {
	testCases := map[string]struct {
		fedObject               fedcorev1a1.GenericFederatedObject
		imageOverriders         []fedcorev1a1.ImageOverrider
		expectedOverridePatches fedcorev1a1.OverridePatches
		isErrorExpected         bool
	}{
		// Test workload scenarios
		// test operations on workload(one container)
		"apply imageOverriders to Deployment(one container), component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedDeploymentWithImage(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Deployment(one container), component: full, operator: replace": {
			fedObject: generateFedDeploymentWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Deployment(one container), component: full, operator: remove": {
			fedObject: generateFedDeploymentWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace", "/spec/template/spec/containers/0/image", ""),
			},
			isErrorExpected: false,
		},

		"apply imageOverriders to DaemonSet(one container), component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedDaemonSetWithImage(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to DaemonSet(one container), component: full, operator: replace": {
			fedObject: generateFedDaemonSetWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to DaemonSet(one container), component: full, operator: remove": {
			fedObject: generateFedDaemonSetWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace", "/spec/template/spec/containers/0/image", ""),
			},
			isErrorExpected: false,
		},

		"apply imageOverriders to StatefulSet(one container), component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedStatefulSetWithImage(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to StatefulSet(one container), component: full, operator: replace": {
			fedObject: generateFedStatefulSetWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to StatefulSet(one container), component: full, operator: remove": {
			fedObject: generateFedStatefulSetWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace", "/spec/template/spec/containers/0/image", ""),
			},
			isErrorExpected: false,
		},

		"apply imageOverriders to Job(one container), component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedJobWithImage(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Job(one container), component: full, operator: replace": {
			fedObject: generateFedJobWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/template/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Job(one container), component: full, operator: remove": {
			fedObject: generateFedJobWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace", "/spec/template/spec/containers/0/image", ""),
			},
			isErrorExpected: false,
		},

		"apply imageOverriders to CronJob(one container), component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedObjWithCronJob(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/jobTemplate/spec/template/spec/containers/0/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to CronJob(one container), component: full, operator: replace": {
			fedObject: generateFedObjWithCronJob(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/jobTemplate/spec/template/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to CronJob(one container), component: full, operator: remove": {
			fedObject: generateFedObjWithCronJob(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/jobTemplate/spec/template/spec/containers/0/image",
					""),
			},
			isErrorExpected: false,
		},

		"apply imageOverriders to Pod(one container), component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedPodWithImage(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Pod(one container), component: full, operator: replace": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Pod(one container), component: full, operator: remove": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace", "/spec/containers/0/image", ""),
			},
			isErrorExpected: false,
		},
		// test replace operation on pod(multiple containers)
		"apply imageOverriders to Pod(two of three containers), component: full, operator: replace": {
			fedObject: generateFedPodWithThreeContainersWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ContainerNames: []string{"server-1", "server-2"},
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/containers/1/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Pod(three of three containers), component: full, operator: replace": {
			fedObject: generateFedPodWithThreeContainersWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/containers/1/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/containers/2/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		// test replace operation on pod(multiple init containers and containers)
		"apply imageOverriders to Pod(one containers,two one containers), component: full, operator: replace": {
			fedObject: generateFedObjWithPodWithTwoNormalAndTwoInit(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ContainerNames: []string{"init-server-1", "server-1"},
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/initContainers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to Pod(two containers,two init containers), component: full, operator: replace": {
			fedObject: generateFedObjWithPodWithTwoNormalAndTwoInit(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/containers/1/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/initContainers/0/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/initContainers/1/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		// test replace operation on pod without value
		"apply imageOverriders to Pod without value, component: [registry,tag], operator: replace": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnRegistryAndTag(OperatorOverwrite, "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		// test replace operation on pod, the origin value of registry is empty
		"apply imageOverriders to Pod(one container), component: registry, OriginValue: empty, operator: replace": {
			fedObject: generateFedPodWithImage(
				"ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:1.0@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		// test replace operation on pod, the origin value of tag is empty
		"apply imageOverriders to Pod(one container), component: tag, OriginValue: empty, operator: replace": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:1.0@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		// test replace operation on pod, the origin value of digest is empty
		"apply imageOverriders to Pod(one container), component: digest, OriginValue: empty, operator: replace": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server:latest",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/containers/0/image",
					"temp.io/temp/echo-server:1.0@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},

		// Test specified path scenarios
		// test operations on specified path(one path)
		"apply imageOverriders to specified path, component: [registry,tag], OriginValue: empty, operator: add": {
			fedObject: generateFedArgoWorkflowWithImage(
				"ealen/echo-server@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath:  "/spec/templates/0/container/image",
					Operations: generateOperationsOnRegistryAndTag(OperatorAddIfAbsent, "temp.io", "0.5"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/templates/0/container/image",
					"temp.io/ealen/echo-server:0.5@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to specified path, component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/templates/0/container/image",
					"temp.io/temp/echo-server:0.5@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		"apply imageOverriders to specified path, component: full, operator: remove": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath:  "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorDelete, "", "", "", ""),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace", "/spec/templates/0/container/image", ""),
			},
			isErrorExpected: false,
		},
		// test replace operations on specified path(two path)
		"apply imageOverriders to specified paths(two path), component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
				{
					ImagePath: "/spec/templates/1/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp-two/echo-server",
						"2.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/templates/0/container/image",
					"temp.io/temp/echo-server:1.0@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				generatePatch("replace",
					"/spec/templates/1/container/image",
					"temp.io/temp-two/echo-server:2.0@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},
		// test empty operator, the default operator value should be "replace"
		"apply imageOverriders to specified path, component: full, operator: empty": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent("",
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: fedcorev1a1.OverridePatches{
				generatePatch("replace",
					"/spec/templates/0/container/image",
					"temp.io/temp/echo-server:1.0@sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
			},
			isErrorExpected: false,
		},

		// Test error scenarios
		// test add operation on workloads, the origin value is not empty
		"apply imageOverriders to Deployment(one container), component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedDeploymentWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to DaemonSet(one container), component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedDaemonSetWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to StatefulSet(one container), component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedStatefulSetWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to Job(one container), component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedJobWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to CronJob(one container), component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedObjWithCronJob(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to Pod(one container), component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		// test add operation on specified path, the origin value is not empty
		"apply imageOverriders to specified path, component: full, OriginValue: not-empty, operator: add": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},

		// test add operation on pod without value
		"apply imageOverriders to Pod without value, component: full, operator: add": {
			fedObject: generateFedPodWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorAddIfAbsent, "", "", "", ""),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		// test apply imageOverriders to unsupported resources without image path
		"apply imageOverriders to unsupported resources without image path, component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"0.5",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		// test invalid image path
		"apply imageOverriders to invalid path, component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to invalid path(don't have prefix '/'), component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to invalid path(index out of range), component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/2/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to invalid path(index < 0), component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/-1/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		"apply imageOverriders to invalid path(index not int), component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/tt/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		// test invalid tag
		"apply imageOverriders with invalid tag to specified path, component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1@0",
						"sha256:aaaaf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
		// test invalid digest
		"apply imageOverriders with invalid digest to specified path, component: full, operator: replace": {
			fedObject: generateFedArgoWorkflowWithImage(
				"docker.io/ealen/echo-server:latest@sha256:bbbbf56b44807c64d294e6c8059b479f35350b454492398225034174808d1726",
			),
			imageOverriders: []fedcorev1a1.ImageOverrider{
				{
					ImagePath: "/spec/templates/0/container/image",
					Operations: generateOperationsOnFullComponent(OperatorOverwrite,
						"temp.io",
						"temp/echo-server",
						"1.0",
						"sha256:aaaa"),
				},
			},
			expectedOverridePatches: nil,
			isErrorExpected:         true,
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			overridePatches, err := parseImageOverriders(testCase.fedObject, testCase.imageOverriders)
			if (err != nil) != testCase.isErrorExpected {
				t.Fatalf("err = %v, but testCase.isErrorExpected = %v", err, testCase.isErrorExpected)
			}

			assert.Equal(t, testCase.expectedOverridePatches, overridePatches)
		})
	}
}

func generatePatch(op, path string, value any) fedcorev1a1.OverridePatch {
	return fedcorev1a1.OverridePatch{Op: op, Path: path, Value: asJSON(value)}
}

func generateOperationsOnFullComponent(operator, registry, repository, tag, digest string) []fedcorev1a1.Operation {
	return []fedcorev1a1.Operation{
		{ImageComponent: Registry, Operator: operator, Value: registry},
		{ImageComponent: Repository, Operator: operator, Value: repository},
		{ImageComponent: Tag, Operator: operator, Value: tag},
		{ImageComponent: Digest, Operator: operator, Value: digest},
	}
}

func generateOperationsOnRegistryAndTag(operator, registry, tag string) []fedcorev1a1.Operation {
	return []fedcorev1a1.Operation{
		{ImageComponent: Registry, Operator: operator, Value: registry},
		{ImageComponent: Tag, Operator: operator, Value: tag},
	}
}

var (
	basicDeploymentTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "deployment-test",
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{},
					},
				},
			},
		},
	}
	basicDaemonSetTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "DaemonSet",
			"metadata": map[string]interface{}{
				"name": "daemonSet-test",
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{},
					},
				},
			},
		},
	}
	basicStatefulSetTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name": "statefulSet-test",
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{},
					},
				},
			},
		},
	}
	basicJobTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "batch/v1",
			"kind":       "Job",
			"metadata": map[string]interface{}{
				"name": "job-test",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{},
					},
				},
			},
		},
	}
	basicCronJobTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "batch/v1beta1",
			"kind":       "CronJob",
			"metadata": map[string]interface{}{
				"name": "cronjob-test",
			},
			"spec": map[string]interface{}{
				"schedule": "*/1 * * * *",
				"jobTemplate": map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"spec": map[string]interface{}{
								"containers": []interface{}{},
							},
						},
					},
				},
			},
		},
	}
	basicPodTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name": "pod-test",
			},
			"spec": map[string]interface{}{
				"containers": []interface{}{},
			},
		},
	}
	basicArgoWorkflowTemplate = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Workflow",
			"metadata": map[string]interface{}{
				"name": "workflow-test",
			},
			"spec": map[string]interface{}{
				"templates": []interface{}{},
			},
		},
	}
)

func generateFedDeploymentWithImage(image string) *fedcorev1a1.FederatedObject {
	deployment := basicDeploymentTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(deployment.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server"},
	}, "spec", "template", "spec", "containers")
	return generateFedObj(deployment)
}

func generateFedDaemonSetWithImage(image string) *fedcorev1a1.FederatedObject {
	daemonSetTemplate := basicDaemonSetTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(daemonSetTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server"},
	}, "spec", "template", "spec", "containers")
	return generateFedObj(daemonSetTemplate)
}

func generateFedStatefulSetWithImage(image string) *fedcorev1a1.FederatedObject {
	statefulSetTemplate := basicStatefulSetTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(statefulSetTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server"},
	}, "spec", "template", "spec", "containers")
	return generateFedObj(statefulSetTemplate)
}

func generateFedJobWithImage(image string) *fedcorev1a1.FederatedObject {
	jobTemplate := basicJobTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(jobTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server"},
	}, "spec", "template", "spec", "containers")
	return generateFedObj(jobTemplate)
}

func generateFedObjWithCronJob(image string) *fedcorev1a1.FederatedObject {
	cronJobTemplate := basicCronJobTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(cronJobTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server"},
	}, "spec", "jobTemplate", "spec", "template", "spec", "containers")
	return generateFedObj(cronJobTemplate)
}

func generateFedPodWithImage(image string) *fedcorev1a1.FederatedObject {
	podTemplate := basicPodTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(podTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server"},
	}, "spec", "containers")
	return generateFedObj(podTemplate)
}

func generateFedArgoWorkflowWithImage(image string) *fedcorev1a1.FederatedObject {
	argoWorkflowTemplate := basicArgoWorkflowTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(argoWorkflowTemplate.Object, []interface{}{
		map[string]interface{}{"container": map[string]interface{}{"image": image}},
		map[string]interface{}{"container": map[string]interface{}{"image": image}},
	}, "spec", "templates")
	return generateFedObj(argoWorkflowTemplate)
}

func generateFedPodWithThreeContainersWithImage(image string) *fedcorev1a1.FederatedObject {
	podTemplate := basicPodTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(podTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server-1"},
		map[string]interface{}{"image": image, "name": "server-2"},
		map[string]interface{}{"image": image, "name": "server-3"},
	}, "spec", "containers")
	return generateFedObj(podTemplate)
}

func generateFedObjWithPodWithTwoNormalAndTwoInit(image string) *fedcorev1a1.FederatedObject {
	podTemplate := basicPodTemplate.DeepCopy()
	_ = unstructured.SetNestedSlice(podTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "init-server-1"},
		map[string]interface{}{"image": image, "name": "init-server-2"},
	}, "spec", "initContainers")

	_ = unstructured.SetNestedSlice(podTemplate.Object, []interface{}{
		map[string]interface{}{"image": image, "name": "server-1"},
		map[string]interface{}{"image": image, "name": "server-2"},
	}, "spec", "containers")

	return generateFedObj(podTemplate)
}

func generateFedObj(workload *unstructured.Unstructured) *fedcorev1a1.FederatedObject {
	rawTargetTemplate, _ := workload.MarshalJSON()
	return &fedcorev1a1.FederatedObject{
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: rawTargetTemplate},
		},
	}
}
