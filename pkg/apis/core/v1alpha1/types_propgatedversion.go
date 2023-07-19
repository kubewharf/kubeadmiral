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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=propagatedversions
// +kubebuilder:subresource:status

// PropagatedVersion holds version information about the state propagated from
// FederatedObject to member clusters. The name of a PropagatedVersion is the
// same as its FederatedObject. If a target resource has a populated
// metadata.Generation field, the generation will be stored with a prefix of
// `gen:` as the version for the cluster.  If metadata.Generation is not
// available, metadata.ResourceVersion will be stored with a prefix of `rv:` as
// the version for the cluster.
type PropagatedVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Status PropagatedVersionStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// PropagatedVersionList contains a list of PropagatedVersion
type PropagatedVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PropagatedVersion `json:"items"`
}

// PropagatedVersionStatus defines the observed state of PropagatedVersion
type PropagatedVersionStatus struct {
	// The observed version of the template for this resource.
	TemplateVersion string `json:"templateVersion"`
	// The observed version of the overrides for this resource.
	OverrideVersion string `json:"overridesVersion"`
	// The last versions produced in each cluster for this resource.
	// +optional
	ClusterVersions []ClusterObjectVersion `json:"clusterVersions,omitempty"`
}

type ClusterObjectVersion struct {
	// The name of the cluster the version is for.
	ClusterName string `json:"clusterName"`
	// The last version produced for the resource by a KubeFed
	// operation.
	Version string `json:"version"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=clusterpropagatedversions,scope=Cluster
// +kubebuilder:subresource:status

// ClusterPropagatedVersion holds version information about the state propagated
// from ClusterFederatedObject to member clusters. The name of a
// ClusterPropagatedVersion is the same as its ClusterFederatedObject. If a
// target resource has a populated metadata.Generation field, the generation
// will be stored with a prefix of `gen:` as the version for the cluster.  If
// metadata.Generation is not available, metadata.ResourceVersion will be stored
// with a prefix of `rv:` as the version for the cluster.
type ClusterPropagatedVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Status PropagatedVersionStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ClusterPropagatedVersionList contains a list of ClusterPropagatedVersion
type ClusterPropagatedVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterPropagatedVersion `json:"items"`
}
