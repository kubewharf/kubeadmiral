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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:openapi-gen=true
// +kubebuilder:resource:path=federatedclusters,shortName=fcluster,scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=ready,type=string,JSONPath=.status.conditions[?(@.type=='Ready')].status
// +kubebuilder:printcolumn:name=joined,type=string,JSONPath=.status.conditions[?(@.type=='Joined')].status
// +kubebuilder:printcolumn:name=age,type=date,JSONPath=.metadata.creationTimestamp

// FederatedCluster is the Schema for the federatedclusters API
type FederatedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FederatedClusterSpec   `json:"spec,omitempty"`
	Status FederatedClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// FederatedClusterList contains a list of FederatedCluster
type FederatedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FederatedCluster `json:"items"`
}

// FederatedClusterSpec defines the desired state of FederatedCluster
type FederatedClusterSpec struct {
	// The API endpoint of the member cluster. This can be a hostname, hostname:port, IP or IP:port.
	APIEndpoint string `json:"apiEndpoint"`

	// Access API endpoint with security.
	Insecure bool `json:"insecure,omitempty"`

	// Whether to use service account token to authenticate to the member cluster.
	// +optional
	UseServiceAccountToken bool `json:"useServiceAccount"`

	// Name of the secret containing the token required to access the member cluster.
	// The secret needs to exist in the fed system namespace.
	// +optional
	SecretRef LocalSecretReference `json:"secretRef"`

	// If specified, the cluster's taints.
	// +optional
	Taints []corev1.Taint `json:"taints,omitempty"`
}

// FederatedClusterStatus defines the observed state of FederatedCluster
type FederatedClusterStatus struct {
	// Conditions is an array of current cluster conditions.
	Conditions []ClusterCondition `json:"conditions"`
	// Resources describes the cluster's resources.
	// +optional
	Resources Resources `json:"resources,omitempty"`
	// The list of api resource types defined in the federated cluster
	// +optional
	APIResourceTypes []APIResource `json:"apiResourceTypes,omitempty"`
}

// LocalSecretReference is a reference to a secret within the enclosing namespace.
type LocalSecretReference struct {
	// Name of a secret within the enclosing namespace
	Name string `json:"name"`
}

// ClusterCondition describes current state of a cluster.
type ClusterCondition struct {
	// Type of cluster condition, Ready or Offline.
	Type ClusterConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Last time the condition was checked.
	LastProbeTime metav1.Time `json:"lastProbeTime"`
	// Last time the condition transit from one status to another.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
	// (brief) reason for the condition's last transition.
	// +optional
	Reason *string `json:"reason,omitempty"`
	// Human readable message indicating details about last transition.
	// +optional
	Message *string `json:"message,omitempty"`
}

type ClusterConditionType string

// These are valid conditions of a cluster.
const (
	// ClusterJoined means the cluster has joined the federation.
	ClusterJoined ClusterConditionType = "Joined"
	// ClusterReady means the cluster is ready to accept workloads.
	ClusterReady ClusterConditionType = "Ready"
	// ClusterOffline means the cluster is temporarily down or not reachable.
	ClusterOffline ClusterConditionType = "Offline"
)

// Resources describes a cluster's resources
type Resources struct {
	// SchedulableNodes represents number of nodes which is ready and schedulable.
	// +optional
	SchedulableNodes *int64 `json:"schedulableNodes,omitempty"`
	// Allocatable represents the total resources that are allocatable for scheduling.
	// +optional
	Allocatable corev1.ResourceList `json:"allocatable,omitempty"`
	// Available represents the resources currently available for scheduling.
	// +optional
	Available corev1.ResourceList `json:"available,omitempty"`
}
