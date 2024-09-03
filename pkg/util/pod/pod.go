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

package pod

import (
	"errors"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

var ErrUnknownTypeToGetPodSpec = errors.New("unknown type to get pod spec")

// PodSpecPaths maps supported type to pod spec path.
// TODO: think about whether PodTemplatePath/PodSpecPath should be specified in the FTC instead.
// Specifying in the FTC allows changing the path according to the api version.
// Other controllers should consider using the specified paths instead of hardcoded paths.
var PodSpecPaths = map[schema.GroupKind]string{
	{Group: appsv1.GroupName, Kind: common.DeploymentKind}:  "spec.template.spec",
	{Group: appsv1.GroupName, Kind: common.StatefulSetKind}: "spec.template.spec",
	{Group: appsv1.GroupName, Kind: common.DaemonSetKind}:   "spec.template.spec",
	{Group: batchv1.GroupName, Kind: common.JobKind}:        "spec.template.spec",
	{Group: batchv1.GroupName, Kind: common.CronJobKind}:    "spec.jobTemplate.spec.template.spec",
	{Group: "", Kind: common.PodKind}:                       "spec",
}

func getPodSpecFromUnstructuredObj(unstructuredObj *unstructured.Unstructured, podSpecPath string) (*corev1.PodSpec, error) {
	podSpecMap, found, err := unstructured.NestedMap(unstructuredObj.Object, strings.Split(podSpecPath, ".")...)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("pod spec does not exist at path %q", podSpecPath)
	}
	podSpec := &corev1.PodSpec{}
	if err := pkgruntime.DefaultUnstructuredConverter.FromUnstructured(podSpecMap, podSpec); err != nil {
		return nil, err
	}
	return podSpec, nil
}

func GetPodSpec(fedObject fedcorev1a1.GenericFederatedObject, podSpecPath string) (*corev1.PodSpec, error) {
	if fedObject == nil {
		return nil, fmt.Errorf("fedObject is nil")
	}
	unsFedObject, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		return nil, err
	}
	return getPodSpecFromUnstructuredObj(unsFedObject, podSpecPath)
}

func GetResourcePodSpec(fedObject fedcorev1a1.GenericFederatedObject, gvk schema.GroupVersionKind) (*corev1.PodSpec, error) {
	path, ok := PodSpecPaths[gvk.GroupKind()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTypeToGetPodSpec, gvk.String())
	}
	return GetPodSpec(fedObject, path)
}

func GetResourcePodSpecFromUnstructuredObj(
	unstructuredObj *unstructured.Unstructured, gvk schema.GroupVersionKind,
) (*corev1.PodSpec, error) {
	podSpecPath, ok := PodSpecPaths[gvk.GroupKind()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTypeToGetPodSpec, gvk.String())
	}
	return getPodSpecFromUnstructuredObj(unstructuredObj, podSpecPath)
}
