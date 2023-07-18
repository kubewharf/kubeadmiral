//go:build exclude
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

package plugins

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type Plugin interface {
	// AggregateStatuses aggregates member cluster object statues to source object status and returns the latter
	AggregateStatuses(
		ctx context.Context,
		sourceObject, fedObject *unstructured.Unstructured,
		clusterObjs map[string]interface{},
		clusterObjsUpToDate bool,
	) (*unstructured.Unstructured, bool, error)
}

var pluginsMap = map[schema.GroupVersionKind]Plugin{
	appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind):  NewDeploymentPlugin(),
	appsv1.SchemeGroupVersion.WithKind(common.StatefulSetKind): NewSingleClusterPlugin(),
	batchv1.SchemeGroupVersion.WithKind(common.JobKind):        NewJobPlugin(),
	corev1.SchemeGroupVersion.WithKind(common.PodKind):         NewPodPlugin(),
}

func GetPlugin(apiResource *metav1.APIResource) Plugin {
	return pluginsMap[schema.GroupVersionKind{
		Group:   apiResource.Group,
		Version: apiResource.Version,
		Kind:    apiResource.Kind,
	}]
}
