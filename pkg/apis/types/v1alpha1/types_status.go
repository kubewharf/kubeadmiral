/*
Copyright 2019 The Kubernetes Authors.

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

// +kubebuilder:skip

// GenericObjectWithStatus represents a generic FederatedObject and its status field
type GenericObjectWithStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status *GenericFederatedStatus `json:"status,omitempty"`
}

type GenericFederatedStatus struct {
	CollisionCount     *int32                 `json:"collisionCount,omitempty"`
	ObservedGeneration int64                  `json:"observedGeneration,omitempty"`
	Conditions         []*GenericCondition    `json:"conditions,omitempty"`
	Clusters           []GenericClusterStatus `json:"clusters,omitempty"`
}

type GenericCondition struct {
	// Type of cluster condition
	Type ConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Last time reconciliation resulted in an error or the last time a
	// change was propagated to member clusters.
	// +optional
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
	// Last time the condition transit from one status to another.
	// +optional
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
	// (brief) reason for the condition's last transition.
	// +optional
	Reason AggregateReason `json:"reason,omitempty"`
}

type GenericClusterStatus struct {
	Name       string            `json:"name"`
	Status     PropagationStatus `json:"status,omitempty"`
	Generation int64             `json:"generation,omitempty"`
}

type PropagationStatus string

const (
	ClusterPropagationOK PropagationStatus = "OK"
	WaitingForRemoval    PropagationStatus = "WaitingForRemoval"

	// Cluster-specific errors

	ClusterNotReady             PropagationStatus = "ClusterNotReady"
	ClusterTerminating          PropagationStatus = "ClusterTerminating"
	CachedRetrievalFailed       PropagationStatus = "CachedRetrievalFailed"
	ComputeResourceFailed       PropagationStatus = "ComputeResourceFailed"
	ApplyOverridesFailed        PropagationStatus = "ApplyOverridesFailed"
	CreationFailed              PropagationStatus = "CreationFailed"
	UpdateFailed                PropagationStatus = "UpdateFailed"
	DeletionFailed              PropagationStatus = "DeletionFailed"
	LabelRemovalFailed          PropagationStatus = "LabelRemovalFailed"
	RetrievalFailed             PropagationStatus = "RetrievalFailed"
	AlreadyExists               PropagationStatus = "AlreadyExists"
	FieldRetentionFailed        PropagationStatus = "FieldRetentionFailed"
	SetLastReplicasetNameFailed PropagationStatus = "SetLastReplicasetNameFailed"
	VersionRetrievalFailed      PropagationStatus = "VersionRetrievalFailed"
	ClientRetrievalFailed       PropagationStatus = "ClientRetrievalFailed"
	ManagedLabelFalse           PropagationStatus = "ManagedLabelFalse"
	FinalizerCheckFailed        PropagationStatus = "FinalizerCheckFailed"

	// Operation timeout errors

	CreationTimedOut     PropagationStatus = "CreationTimedOut"
	UpdateTimedOut       PropagationStatus = "UpdateTimedOut"
	DeletionTimedOut     PropagationStatus = "DeletionTimedOut"
	LabelRemovalTimedOut PropagationStatus = "LabelRemovalTimedOut"
)

type AggregateReason string

const (
	AggregateSuccess       AggregateReason = ""
	SyncRevisionsFailed    AggregateReason = "SyncRevisionsFailed"
	ClusterRetrievalFailed AggregateReason = "ClusterRetrievalFailed"
	ComputePlacementFailed AggregateReason = "ComputePlacementFailed"
	PlanRolloutFailed      AggregateReason = "PlanRolloutFailed"
	CheckClusters          AggregateReason = "CheckClusters"
	NamespaceNotFederated  AggregateReason = "NamespaceNotFederated"
	EnsureDeletionFailed   AggregateReason = "EnsureDeletionFailed"
)

type ConditionType string

const (
	PropagationConditionType ConditionType = "Propagation"
)
