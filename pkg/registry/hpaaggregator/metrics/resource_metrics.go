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

package metrics

import (
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/metrics/pkg/apis/metrics"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/kubewharf/kubeadmiral/pkg/registry/hpaaggregator/metrics/resource"
)

// BuildResourceMetrics constructs APIGroupInfo the metrics.k8s.io API group using the given getters.
func BuildResourceMetrics(
	scheme *runtime.Scheme,
	parameterCodec runtime.ParameterCodec,
	codecs serializer.CodecFactory,
	m resource.MetricsGetter,
	podMetadataLister cache.GenericLister,
	nodeLister corev1listers.NodeLister,
	nodeSelector []labels.Requirement,
) genericapiserver.APIGroupInfo {
	node := resource.NewNodeMetrics(metrics.Resource("nodemetrics"), m, nodeLister, nodeSelector)
	pod := resource.NewPodMetrics(metrics.Resource("podmetrics"), m, podMetadataLister)

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(metrics.GroupName, scheme, parameterCodec, codecs)
	metricsServerResources := map[string]rest.Storage{
		"nodes": node,
		"pods":  pod,
	}
	apiGroupInfo.VersionedResourcesStorageMap[metricsv1beta1.SchemeGroupVersion.Version] = metricsServerResources

	return apiGroupInfo
}
