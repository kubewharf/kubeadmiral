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

	katalystv1a1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/resource"
)

var katalystCNR = katalystv1a1.SchemeGroupVersion.WithResource(katalystv1a1.ResourceNameKatalystCNR)

type katalystPlugin struct{}

func (k *katalystPlugin) CollectClusterResources(
	ctx context.Context,
	nodes []*corev1.Node,
	pods []*corev1.Pod,
	handle ClusterHandle,
) (allocatable, available corev1.ResourceList, err error) {
	_, logger := logging.InjectLoggerValues(ctx, "gvr", katalystCNR)

	cnr, ok := handle.DynamicLister[katalystCNR]
	if !ok {
		logger.V(4).Info("Lister not found, cluster resource collection skipped")
		return nil, nil, nil
	}
	unsList, err := cnr.List(labels.Everything())
	if err != nil || len(unsList) == 0 {
		return nil, nil, err
	}

	allocatable = make(corev1.ResourceList)
	nodeSet := sets.New[string]()
	for _, node := range nodes {
		nodeSet.Insert(node.Name)
	}

	for _, uns := range unsList {
		unsCNR, ok := uns.(*unstructured.Unstructured)
		if !ok || !nodeSet.Has(unsCNR.GetName()) {
			continue
		}

		cnr := &katalystv1a1.CustomNodeResource{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(unsCNR.UnstructuredContent(), cnr); err != nil {
			logger.Error(err, "Failed to convert unstructured to CNR")
			continue
		}
		if cnr.Status.Resources.Allocatable == nil {
			continue
		}
		resource.AddResources(*cnr.Status.Resources.Allocatable, allocatable)
	}

	available = make(corev1.ResourceList)
	for name, quantity := range allocatable {
		available[name] = quantity.DeepCopy()
	}

	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		podRequests := resource.GetPodResourceRequests(&pod.Spec)
		for name, requestedQuantity := range podRequests {
			if availableQuantity, ok := available[name]; ok {
				availableQuantity.Sub(requestedQuantity)
				available[name] = availableQuantity
			}
		}
	}

	return allocatable, available, nil
}

func (k *katalystPlugin) ClusterResourcesToCollect() sets.Set[schema.GroupVersionResource] {
	return sets.New(katalystCNR)
}

func AddKatalystPluginIntoDefaultPlugins() {
	if _, ok := defaultPlugins[katalystv1a1.GroupName]; ok {
		return
	}
	defaultPlugins[katalystv1a1.GroupName] = &katalystPlugin{}
}
