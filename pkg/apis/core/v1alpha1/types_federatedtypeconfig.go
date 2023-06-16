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

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=federatedtypeconfigs,shortName=ftc,scope=Cluster
// +kubebuilder:subresource:status

// FederatedTypeConfig specifies an API resource type to federate and various type-specific options.
type FederatedTypeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec FederatedTypeConfigSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// FederatedTypeConfigList contains a list of FederatedTypeConfig
type FederatedTypeConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FederatedTypeConfig `json:"items"`
}

type FederatedTypeConfigSpec struct {
	// The API resource type to be federated.
	SourceType APIResource `json:"sourceType"`

	// Configuration for StatusAggregation. If left empty, the StatusAggregation feature will be disabled.
	// +optional
	StatusAggregation *StatusAggregationConfig `json:"statusAggregation,omitempty"`
	// Configuration for RevisionHistory. If left empty, the RevisionHistory feature will be disabled.
	// +optional
	RevisionHistory *RevisionHistoryConfig `json:"revisionHistory,omitempty"`
	// Configuration for RolloutPlan. If left empty, the RolloutPlan feature will be disabled.
	// +optional
	RolloutPlan *RolloutPlanConfig `json:"rolloutPlan,omitempty"`
	// Configuration for StatusCollection. If left empty, the StatusCollection feature will be disabled.
	// +optional
	StatusCollection *StatusCollectionConfig `json:"statusCollection,omitempty"`
	// Configuration for AutoMigration. If left empty, the AutoMigration feature will be disabled.
	// +optional
	AutoMigration *AutoMigrationConfig `json:"autoMigration,omitempty"`

	// The controllers that must run before the source object can be propagated to member clusters.
	// Each inner slice specifies a step. Step T must complete before step T+1 can commence.
	// Controllers within each step can execute in parallel.
	// +optional
	Controllers [][]string `json:"controllers,omitempty"`

	// Defines the paths to various fields in the target object's schema.
	// +optional
	PathDefinition PathDefinition `json:"pathDefinition,omitempty"`
}

// PathDefinition contains paths to various fields in the source object that are required by controllers.
type PathDefinition struct {
	// Path to a metav1.LabelSelector field that selects the replicas for this object.
	// E.g. `spec.selector` for Deployment and ReplicaSet.
	// +optional
	LabelSelector string `json:"labelSelector,omitempty"`

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

	// Path to a numeric field that reflects the number of ready replicas that the object currently has.
	// E.g. `status.readyReplicas` for Deployment and ReplicaSet.
	// +optional
	ReadyReplicasStatus string `json:"readyReplicasStatus,omitempty"`
}

// StatusCollectionConfig defines the configurations for the StatusCollection feature.
type StatusCollectionConfig struct {
	// Whether or not to enable status collection.
	Enabled bool `json:"enabled"`
	// Contains the fields to be collected during status collection. Each field is a dot separated string that
	// corresponds to its path in the source object's schema.
	// E.g. `metadata.creationTimestamp`.
	Fields []string `json:"fields,omitempty"`
}

// StatusAggregationConfig defines the configurations for the StatusAggregation feature.
type StatusAggregationConfig struct {
	// Whether or not to enable status aggregation.
	Enabled bool `json:"enabled"`
}

// RevisionHistoryConfig defines the configurations for the RevisionHistory feature.
type RevisionHistoryConfig struct {
	// Whether or not preserve a RevisionHistory for the federated object during updates.
	Enabled bool `json:"enabled"`
}

// RolloutPlanConfig defines the configurations for the RolloutPlan feature.
type RolloutPlanConfig struct {
	// Whether or not to synchronize the rollout process across clusters.
	Enabled bool `json:"enabled"`
}

// AutoMigrationConfig defines the configurations for the AutoMigration feature.
type AutoMigrationConfig struct {
	// Whether or not to automatically migrate unschedulable pods to a different cluster.
	Enabled bool `json:"enabled"`
}

// APIResource represents a Kubernetes API resource.
type APIResource struct {
	// Group of the resource.
	// +optional
	Group string `json:"group,omitempty"`
	// Version of the resource.
	Version string `json:"version"`
	// Kind of the resource.
	Kind string `json:"kind"`
	// Lower-cased plural name of the resource (e.g. configmaps).  If not provided,
	// 	it will be computed by lower-casing the kind and suffixing an 's'.
	PluralName string `json:"pluralName"`
	// Scope of the resource.
	Scope apiextv1beta1.ResourceScope `json:"scope"`
}
