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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
)

type ClusterHandle struct {
	Client generic.Client
}

type Plugin interface {
	GetPodsForClusterObject(
		ctx context.Context,
		obj *unstructured.Unstructured,
		handle ClusterHandle,
	) ([]*corev1.Pod, error)
}

var nativePlugins = map[schema.GroupVersionResource]Plugin{
	common.DeploymentGVR: &deploymentPlugin{},
}

func ResolvePlugin(typeConfig *fedcorev1a1.FederatedTypeConfig) (Plugin, error) {
	targetAPIResource := typeConfig.GetTargetType()
	targetGVR := schemautil.APIResourceToGVR(&targetAPIResource)

	if plugin, exists := nativePlugins[targetGVR]; exists {
		return plugin, nil
	}

	return nil, fmt.Errorf("unsupported type %s", targetGVR.String())
}
