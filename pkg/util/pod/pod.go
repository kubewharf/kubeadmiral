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

var ErrUnknownTypeToGetPodTemplate = errors.New("unknown type to get pod template")

// PodTemplatePaths maps supported type to pod template path.
// TODO: think about whether PodTemplatePath/PodSpecPath should be specified in the FTC instead.
// Specifying in the FTC allows changing the path according to the api version.
// Other controllers should consider using the specified paths instead of hardcoded paths.
var PodTemplatePaths = map[schema.GroupKind]string{
	{Group: appsv1.GroupName, Kind: common.DeploymentKind}:  "spec.template",
	{Group: appsv1.GroupName, Kind: common.StatefulSetKind}: "spec.template",
	{Group: appsv1.GroupName, Kind: common.DaemonSetKind}:   "spec.template",
	{Group: batchv1.GroupName, Kind: common.JobKind}:        "spec.template",
	{Group: batchv1.GroupName, Kind: common.CronJobKind}:    "spec.jobTemplate.spec.template",
	{Group: corev1.GroupName, Kind: common.PodKind}:         "spec",
}

func GetPodSpec(fedObject fedcorev1a1.GenericFederatedObject, podTemplatePath string) (*corev1.PodSpec, error) {
	if fedObject == nil {
		return nil, fmt.Errorf("fedObject is nil")
	}
	unsFedObject, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		return nil, err
	}
	podTemplateMap, found, err := unstructured.NestedMap(unsFedObject.Object, strings.Split(podTemplatePath, ".")...)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("pod template does not exist at path %q", podTemplatePath)
	}
	podTemplate := &corev1.PodTemplateSpec{}
	if err := pkgruntime.DefaultUnstructuredConverter.FromUnstructured(podTemplateMap, podTemplate); err != nil {
		return nil, err
	}
	return &podTemplate.Spec, nil
}

func GetResourcePodSpec(fedObject fedcorev1a1.GenericFederatedObject, gvk schema.GroupVersionKind) (*corev1.PodSpec, error) {
	path, ok := PodTemplatePaths[gvk.GroupKind()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTypeToGetPodTemplate, gvk.String())
	}
	return GetPodSpec(fedObject, path)
}
