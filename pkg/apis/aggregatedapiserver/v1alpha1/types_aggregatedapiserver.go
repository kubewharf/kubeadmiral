/*
Copyright 2024 The KubeAdmiral Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Aggregations defines a virtual API endpoint for aggregated apiserver.
type Aggregations struct {
	metav1.TypeMeta `json:",inline"`
}

// +k8s:conversion-gen:explicit-from=net/url.Values
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterProxyOptions is the query options to a Cluster's proxy call.
type ClusterProxyOptions struct {
	metav1.TypeMeta `json:",inline"`

	// Path is the part of URLs that include clusters, suffixes,
	// and parameters to use for the current proxy request to cluster.
	// For example, the whole request URL is
	// http://localhost/apis/aggregated.kubeadmiral.io/v1alpha1/aggregations/{clustername}/proxy/api/v1/nodes
	// Path is api/v1/nodes
	// +optional
	Path string `json:"path,omitempty" protobuf:"bytes,1,opt,name=path"`
}
