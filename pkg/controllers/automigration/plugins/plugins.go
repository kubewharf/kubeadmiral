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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type ClusterHandle struct {
	Client dynamic.Interface
}

type Plugin interface {
	GetPodsForClusterObject(
		ctx context.Context,
		obj *unstructured.Unstructured,
		handle ClusterHandle,
	) ([]*corev1.Pod, error)

	GetTargetObjectFromPod(
		ctx context.Context,
		pod *corev1.Pod,
		handle ClusterHandle,
	) (obj *unstructured.Unstructured, found bool, err error)
}

var NativePlugins = map[schema.GroupVersionKind]Plugin{
	appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind): &deploymentPlugin{},
}

func ResolvePlugin(gvk schema.GroupVersionKind) (Plugin, error) {
	if plugin, exists := NativePlugins[gvk]; exists {
		return plugin, nil
	}

	return nil, fmt.Errorf("unsupported type %s", gvk.String())
}
