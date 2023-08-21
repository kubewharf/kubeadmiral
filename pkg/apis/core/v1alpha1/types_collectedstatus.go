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

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=collectedstatuses,shortName=cs,singular=collectedstatus
// +kubebuilder:object:root=true

// CollectedStatus stores the collected fields of Kubernetes objects from member clusters, that are propagated by a
// FederatedObject.
type CollectedStatus struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	GenericCollectedStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// CollectedStatusList contains a list of CollectedStatuses.
type CollectedStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CollectedStatus `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=clustercollectedstatuses,shortName=ccs,singular=clustercollectedstatus,scope=Cluster
// +kubebuilder:object:root=true

// ClusterCollectedStatus stores the collected fields of Kubernetes objects from member clusters, that are propagated by
// a ClusterFederatedObject.
type ClusterCollectedStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	GenericCollectedStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterCollectedStatusList contains a list of ClusterCollectedStatuses.
type ClusterCollectedStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterCollectedStatus `json:"items"`
}

// GenericCollectedStatus contains the shared fields of CollectedStatus and ClusterCollectedStatus
type GenericCollectedStatus struct {
	// Clusters is the list of member clusters and collected fields for its propagated Kubernetes object.
	Clusters []CollectedFieldsWithCluster `json:"clusters"`

	// LastUpdateTime is the last time that a collection was performed.
	LastUpdateTime metav1.Time `json:"lastUpdateTime"`
}

// CollectedFieldsWithCluster stores the collected fields of a Kubernetes object in a member cluster.
type CollectedFieldsWithCluster struct {
	// Cluster is the name of the member cluster.
	Cluster string `json:"cluster"`
	// CollectedFields is the the set of fields collected for the Kubernetes object.
	CollectedFields apiextensionsv1.JSON `json:"collectedFields"`
	// Error records any errors encountered while collecting fields from the cluster.
	// +optional
	Error string `json:"error,omitempty"`
}
