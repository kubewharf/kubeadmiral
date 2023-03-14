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

type GenericOverridePolicySpec struct {
	// OverrideRules specify the override rules.
	// Each rule specifies the overriders and the clusters these overriders should be applied to.
	// +optional
	OverrideRules []OverrideRule `json:"overrideRules,omitempty"`
}

type OverrideRule struct {
	// TargetClusters selects the clusters in which the overriders in this rule should be applied.
	// If multiple types of selectors are specified, the overall result is the intersection of all of them.
	// +optional
	TargetClusters *TargetClusters `json:"targetClusters,omitempty"`

	// Overriders specify the overriders to be applied in the target clusters.
	// +optional
	Overriders *Overriders `json:"overriders,omitempty"`
}

type GenericRefCountedStatus struct {
	// +kubebuilder:validation:Minimum=0
	RefCount      int64           `json:"refCount,omitempty"`
	TypedRefCount []TypedRefCount `json:"typedRefCount,omitempty"`
}

type TypedRefCount struct {
	Group    string `json:"group,omitempty"`
	Resource string `json:"resource"`
	// +kubebuilder:validation:Minimum=0
	Count int64 `json:"count"`
}

type ClusterSelectorTerm struct {
	// A list of cluster selector requirements by cluster labels.
	// +optional
	MatchExpressions []ClusterSelectorRequirement `json:"matchExpressions,omitempty"`
	// A list of cluster selector requirements by cluster fields.
	// +optional
	MatchFields []ClusterSelectorRequirement `json:"matchFields,omitempty"`
}

// ClusterSelectorRequirement is a selector that contains values, a key, and an operator that relates the values and keys
type ClusterSelectorRequirement struct {
	Key      string                  `json:"key"`
	Operator ClusterSelectorOperator `json:"operator"`
	Values   []string                `json:"values"`
}

// ClusterSelectorOperator is the set of operators that can be used in a cluster selector requirement.
// +kubebuilder:validation:Enum=In;NotIn;Exists;DoesNotExist;Gt;Lt
type ClusterSelectorOperator string

const (
	ClusterSelectorOpIn           ClusterSelectorOperator = "In"
	ClusterSelectorOpNotIn        ClusterSelectorOperator = "NotIn"
	ClusterSelectorOpExists       ClusterSelectorOperator = "Exists"
	ClusterSelectorOpDoesNotExist ClusterSelectorOperator = "DoesNotExist"
	ClusterSelectorOpGt           ClusterSelectorOperator = "Gt"
	ClusterSelectorOpLt           ClusterSelectorOperator = "Lt"
)
