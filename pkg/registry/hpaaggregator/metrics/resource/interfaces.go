/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package resource

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/metrics/pkg/apis/metrics"
)

// MetricsGetter is both a PodMetricsGetter and a NodeMetricsGetter
type MetricsGetter interface {
	PodMetricsGetter
	NodeMetricsGetter
}

// PodMetricsGetter knows how to fetch metrics for the containers in a pod.
type PodMetricsGetter interface {
	// GetPodMetrics gets the latest metrics for all containers in each listed pod,
	// returning both the metrics and the associated collection timestamp.
	GetPodMetrics(pods ...*metav1.PartialObjectMetadata) ([]metrics.PodMetrics, error)
}

// NodeMetricsGetter knows how to fetch metrics for a node.
type NodeMetricsGetter interface {
	// GetNodeMetrics gets the latest metrics for the given nodes,
	// returning both the metrics and the associated collection timestamp.
	GetNodeMetrics(nodes ...*corev1.Node) ([]metrics.NodeMetrics, error)
}
