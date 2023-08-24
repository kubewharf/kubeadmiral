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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=federatedobjects,shortName=fo,singular=federatedobject
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true

// FederatedObject describes a namespace-scoped Kubernetes object and how it should be propagated to different member
// clusters.
type FederatedObject struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired behavior of the FederatedObject.
	Spec GenericFederatedObjectSpec `json:"spec"`

	// Status describes the most recently observed status of the FederatedObject.
	// +optional
	Status GenericFederatedObjectStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// FederatedObjectList contains a list of FederatedObject.
type FederatedObjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FederatedObject `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=clusterfederatedobjects,shortName=cfo,singular=clusterfederatedobject,scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterFederatedObject describes a cluster-scoped Kubernetes object and how it should be propagated to different
// member clusters.
type ClusterFederatedObject struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired behavior of the FederatedObject.
	Spec GenericFederatedObjectSpec `json:"spec"`

	// Status describes the most recently observed status of the FederatedObject.
	// +optional
	Status GenericFederatedObjectStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterFederatedObjectList contains a list of ClusterFederatedObject.
type ClusterFederatedObjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterFederatedObject `json:"items"`
}

// GenericFederatedObjectSpec defines the desired behavior of a FederatedObject or ClusterFederatedObject.
type GenericFederatedObjectSpec struct {
	// Template is the base template of the Kubernetes object to be propagated.
	Template apiextensionsv1.JSON `json:"template"`
	// Overrides describe the overrides that should be applied to the base template of the Kubernetes object before it
	// is propagated to individual member clusters.
	// +optional
	Overrides []OverrideWithController `json:"overrides,omitempty"`
	// Placements describe the member clusters that the Kubernetes object will be propagated to, which is a union of all
	// the listed clusters.
	// +optional
	Placements []PlacementWithController `json:"placements,omitempty"`
	// Follows defines other objects, or "leaders", that the Kubernetes object should follow during propagation, i.e.
	// the Kubernetes object should be propagated to all member clusters that its "leaders" are placed in.
	// +optional
	Follows []LeaderReference `json:"follows,omitempty"`
}

// GenericFederatedObjectStatus describes the most recently observed status of a FederatedObject or ClusterFederatedObject.
type GenericFederatedObjectStatus struct {
	// SyncedGeneration is the generation of this FederatedObject when it was last synced to selected member clusters.
	SyncedGeneration int64 `json:"syncedGeneration,omitempty"`
	// Conditions describe the current state of this FederatedObject.
	Conditions []GenericFederatedObjectCondition `json:"conditions,omitempty"`
	// Clusters contains the propagation status of the Kubernetes object for individual member clusters.
	Clusters []PropagationStatus `json:"clusters,omitempty"`
}

// PlacementWithController describes the member clusters that a Kubernetes object should be propagated to.
type PlacementWithController struct {
	// Controller identifies the controller responsible for this placement.
	Controller string `json:"controller"`
	// Placement is the list of member clusters that the Kubernetes object should be propagated to.
	Placement []ClusterReference `json:"placement"`
}

// ClusterReference represents a single member cluster.
type ClusterReference struct {
	// Cluster is the name of the member cluster.
	Cluster string `json:"cluster"`
}

// OverrideWithController describes the overrides that will be applied to a Kubernetes object before it is propagated to
// individual member clusters.
type OverrideWithController struct {
	// Controller identifies the controller responsible for this override.
	Controller string `json:"controller"`
	// Override is the list of member clusters and their respective override patches.
	Override []ClusterReferenceWithPatches `json:"clusters"`
}

// ClusterReferenceWithPatches represents a single member cluster and a list of override patches for the cluster.
type ClusterReferenceWithPatches struct {
	// Cluster is the name of the member cluster.
	Cluster string `json:"cluster"`
	// Patches is the list of override patches for the member cluster.
	Patches OverridePatches `json:"patches,omitempty"`
}

// OverridePatch defines a JSON patch.
type OverridePatch struct {
	Op    string               `json:"op,omitempty"`
	Path  string               `json:"path"`
	Value apiextensionsv1.JSON `json:"value,omitempty"`
}

// OverridePatches is a list of OverridePatch.
type OverridePatches []OverridePatch

// LeaderReference contains the identifying metadata of a "leader" Kubernetes object.
type LeaderReference struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// GenericFederatedObjectCondition contains the current details about a particular condition of a FederatedObject.
type GenericFederatedObjectCondition struct {
	// Type is the type of the condition.
	Type FederatedObjectConditionType `json:"type"`
	// Status is the status of the condition, one of True, False or Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// LastUpdateTime is the last time a reconciliation for this condition occurred.
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	// LastTransitionTime is the last time the status of this condition changed.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// Reason is the reason for the last status change of this condition.
	// +optional
	Reason FederatedObjectConditionReason `json:"reason,omitempty"`
}

// PropagationStatus describes the propagation of a Kubernetes object to a given member cluster.
type PropagationStatus struct {
	// Cluster is the name of the member cluster.
	Cluster string `json:"cluster"`
	// Status describes the current status of propagating the Kubernetes object to the member cluster.
	Status PropagationStatusType `json:"status"`
	// LastObservedGeneration is the last observed generation of the Kubernetes object in the member cluster.
	LastObservedGeneration int64 `json:"lastObservedGeneration,omitempty"`
}

// FederatedObjectConditionType is a unique, camel-case word to describe the type of a FederatedObjectCondition.
type FederatedObjectConditionType string

const (
	PropagationConditionType FederatedObjectConditionType = "Propagated"
)

// FederatedObjectConditionReason is a unique, camel-case word to describe the reason for the last status change of a
// FederatedObjectCondition.
type FederatedObjectConditionReason string

const (
	AggregateSuccess       FederatedObjectConditionReason = ""
	ClusterRetrievalFailed FederatedObjectConditionReason = "ClusterRetrievalFailed"
	CheckClusters          FederatedObjectConditionReason = "CheckClusters"
	EnsureDeletionFailed   FederatedObjectConditionReason = "EnsureDeletionFailed"
)

// PropagationStatusType is a unique, camel-case word to describe the current status of propagating a Kubernetes object
// to a member cluster.
type PropagationStatusType string

const (
	ClusterPropagationOK PropagationStatusType = "OK"
	WaitingForRemoval    PropagationStatusType = "WaitingForRemoval"
	PendingCreate        PropagationStatusType = "PendingCreate"

	// Cluster-specific errors

	ClusterNotReady             PropagationStatusType = "ClusterNotReady"
	ClusterTerminating          PropagationStatusType = "ClusterTerminating"
	CachedRetrievalFailed       PropagationStatusType = "CachedRetrievalFailed"
	ComputeResourceFailed       PropagationStatusType = "ComputeResourceFailed"
	ApplyOverridesFailed        PropagationStatusType = "ApplyOverridesFailed"
	CreationFailed              PropagationStatusType = "CreationFailed"
	UpdateFailed                PropagationStatusType = "UpdateFailed"
	DeletionFailed              PropagationStatusType = "DeletionFailed"
	LabelRemovalFailed          PropagationStatusType = "LabelRemovalFailed"
	RetrievalFailed             PropagationStatusType = "RetrievalFailed"
	AlreadyExists               PropagationStatusType = "AlreadyExists"
	FieldRetentionFailed        PropagationStatusType = "FieldRetentionFailed"
	SetLastReplicasetNameFailed PropagationStatusType = "SetLastReplicasetNameFailed"
	VersionRetrievalFailed      PropagationStatusType = "VersionRetrievalFailed"
	ClientRetrievalFailed       PropagationStatusType = "ClientRetrievalFailed"
	ManagedLabelFalse           PropagationStatusType = "ManagedLabelFalse"
	FinalizerCheckFailed        PropagationStatusType = "FinalizerCheckFailed"

	// Operation timeout errors

	CreationTimedOut     PropagationStatusType = "CreationTimedOut"
	UpdateTimedOut       PropagationStatusType = "UpdateTimedOut"
	DeletionTimedOut     PropagationStatusType = "DeletionTimedOut"
	LabelRemovalTimedOut PropagationStatusType = "LabelRemovalTimedOut"
)
