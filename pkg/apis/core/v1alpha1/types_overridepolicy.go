// The design of the OverridePolicy schema is inspired by Karmada's counterpart. Kudos!

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=overridepolicies,shortName=op,singular=overridepolicy
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OverridePolicy describes the override rules for a resource.
type OverridePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GenericOverridePolicySpec `json:"spec"`
	// +optional
	Status OverridePolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// OverridePolicyList contains a list of OverridePolicy
type OverridePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OverridePolicy `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=clusteroverridepolicies,shortName=cop,singular=clusteroverridepolicy,scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterOverridePolicy describes the override rules for a resource.
type ClusterOverridePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GenericOverridePolicySpec `json:"spec"`
	// +optional
	Status OverridePolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterOverridePolicyList contains a list of ClusterOverridePolicy
type ClusterOverridePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterOverridePolicy `json:"items"`
}

type OverridePolicyStatus struct {
	GenericRefCountedStatus `json:",inline"`
}

type TargetClusters struct {
	// Clusters selects FederatedClusters by their names.
	// Empty Clusters selects all FederatedClusters.
	// +optional
	Clusters []string `json:"clusters,omitempty"`

	// ClusterSelector selects FederatedClusters by their labels.
	// Empty labels selects all FederatedClusters.
	// +optional
	ClusterSelector map[string]string `json:"clusterSelector,omitempty"`

	// ClusterAffinity selects FederatedClusters by matching their labels and fields against expressions.
	// If multiple terms are specified, their results are ORed.
	// +optional
	ClusterAffinity []ClusterSelectorTerm `json:"clusterAffinity,omitempty"`
}

type Overriders struct {
	// JsonPatch specifies overriders in a syntax similar to RFC6902 JSON Patch.
	// +optional
	JsonPatch []JsonPatchOverrider `json:"jsonpatch,omitempty"`
}

type JsonPatchOverrider struct {
	// Operator specifies the operation.
	// If omitted, defaults to "replace".
	// +optional
	Operator string `json:"operator,omitempty"`

	// Path is a JSON pointer (RFC 6901) specifying the location within the resource document where the
	// operation is performed.
	// Each key in the path should be prefixed with "/",
	// while "~" and "/" should be escaped as "~0" and "~1" respectively.
	// For example, to add a label "kubeadmiral.io/label",
	// the path should be "/metadata/labels/kubeadmiral.io~1label".
	Path string `json:"path"`

	// Value is the value(s) required by the operation.
	Value apiextensionsv1.JSON `json:"value,omitempty"`
}
