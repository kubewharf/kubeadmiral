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
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	StatusAggregationEnabled  StatusAggregationMode = "Enabled"
	StatusAggregationDisabled StatusAggregationMode = "Disabled"

	RevisionHistoryEnabled  RevisionHistoryMode = "Enabled"
	RevisionHistoryDisabled RevisionHistoryMode = "Disabled"

	RolloutPlanEnabled  RolloutPlanMode = "Enabled"
	RolloutPlanDisabled RolloutPlanMode = "Disabled"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=federatedtypeconfigs,shortName=ftc,scope=Cluster
// +kubebuilder:subresource:status

// FederatedTypeConfig is the Schema for the federatedtypeconfigs API
type FederatedTypeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FederatedTypeConfigSpec   `json:"spec,omitempty"`
	Status FederatedTypeConfigStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// FederatedTypeConfigList contains a list of FederatedTypeConfig
type FederatedTypeConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FederatedTypeConfig `json:"items"`
}

type FederatedTypeConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// The configuration of the source type. If set, each object of the source
	// type will be federated to object of the federated type with the same name
	// and namespace.
	SourceType *APIResource `json:"sourceType,omitempty"`
	// The configuration of the target type. If not set, the pluralName and
	// groupName fields will be set from the metadata.name of this resource. The
	// kind field must be set.
	TargetType APIResource `json:"targetType"`
	// Configuration for the federated type that defines (via
	// template, placement and overrides fields) how the target type
	// should appear in multiple cluster.
	FederatedType APIResource `json:"federatedType"`
	// Configuration for the status type that holds information about which type
	// holds the status of the federated resource. If not provided, the group
	// and version will default to those provided for the federated type api
	// resource.
	// +optional
	StatusType *APIResource `json:"statusType,omitempty"`
	// Whether or not Status object should be populated.
	// +optional
	StatusCollection *StatusCollection `json:"statusCollection,omitempty"`
	// Whether or not Status should be aggregated to source type object
	StatusAggregation *StatusAggregationMode `json:"statusAggregation,omitempty"`
	// Whether or not keep revisionHistory for the federatedType resource
	RevisionHistory *RevisionHistoryMode `json:"revisionHistory,omitempty"`
	// Whether or not to plan the rollout process
	// +optional
	RolloutPlan *RolloutPlanMode `json:"rolloutPlan,omitempty"`
	// The controllers that must run before the resource can be propagated to member clusters.
	// Each inner slice specifies a step. Step T must complete before step T+1 can commence.
	// Controllers within each step can execute in parallel.
	// +optional
	Controllers [][]string `json:"controllers,omitempty"`

	// Defines the paths in the target object schema.
	// +optional
	PathDefinition PathDefinition `json:"pathDefinition,omitempty"`
}

type PathDefinition struct {
	// Path to a numeric field that indicates the number of replicas that an object can be divided into.
	// E.g. `spec.replicas` for Deployment and ReplicaSet.
	// +optional
	ReplicasSpec string `json:"replicasSpec,omitempty"`

	// Path to a numeric field that reflects the number of replicas that the object currently has.
	// E.g. `status.replicas` for Deployment and ReplicaSet.
	// +optional
	ReplicasStatus string `json:"replicasStatus,omitempty"`

	// Path to a numeric field that reflects the number of available replicas that the object currently has.
	// E.g. `status.availableReplicas` for Deployment and ReplicaSet.
	// +optional
	AvailableReplicasStatus string `json:"availableReplicasStatus,omitempty"`
}

// FederatedTypeConfigStatus defines the observed state of FederatedTypeConfig
type FederatedTypeConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ObservedGeneration is the generation as observed by the controller consuming the FederatedTypeConfig.
	ObservedGeneration int64 `json:"observedGeneration"`
}

// StatusCollection defines the fields that the status controller needs to collect
type StatusCollection struct {
	Fields []string `json:"fields,omitempty"`
}

// StatusAggregationMode defines the state of status aggregation.
type StatusAggregationMode string

type RevisionHistoryMode string

type RolloutPlanMode string

// APIResource defines how to configure the dynamic client for an API resource.
type APIResource struct {
	// metav1.GroupVersion is not used since the json annotation of
	// the fields enforces them as mandatory.

	// Group of the resource.
	// +optional
	Group string `json:"group,omitempty"`
	// Version of the resource.
	Version string `json:"version"`
	// Camel-cased singular name of the resource (e.g. ConfigMap)
	Kind string `json:"kind"`
	// Lower-cased plural name of the resource (e.g. configmaps).  If
	// not provided, it will be computed by lower-casing the kind and
	// suffixing an 's'.
	PluralName string `json:"pluralName"`
	// Scope of the resource.
	Scope apiextv1beta1.ResourceScope `json:"scope"`
}
