// The design of the OverridePolicy schema is inspired by Karmada's counterpart. Kudos!

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=propagationpolicies,shortName=pp,singular=propagationpolicy
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true

// PropagationPolicy describes the scheduling rules for a resource.
type PropagationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PropagationPolicySpec `json:"spec"`
	// +optional
	Status PropagationPolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type PropagationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PropagationPolicy `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=clusterpropagationpolicies,shortName=cpp,singular=clusterpropagationpolicy,scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterPropagationPolicy describes the scheduling rules for a resource.
type ClusterPropagationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PropagationPolicySpec `json:"spec"`
	// +optional
	Status PropagationPolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterPropagationPolicyList contains a list of ClusterPropagationPolicy
type ClusterPropagationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterPropagationPolicy `json:"items"`
}

type PropagationPolicySpec struct {
	// Profile determines the scheduling profile to be used for scheduling
	// +optional
	SchedulingProfile string `json:"schedulingProfile"`

	// SchedulingMode determines the mode used for scheduling.
	SchedulingMode SchedulingMode `json:"schedulingMode"`
	// StickyCluster determines if a federated object can be rescheduled.
	// +optional
	// Deprecated: Please use reschedulePolicy.disableRescheduling instead. This field will be removed in the next release.
	StickyCluster bool `json:"stickyCluster"`

	// ClusterSelector is a label query over clusters to consider for scheduling.
	// An empty or nil ClusterSelector selects everything.
	// +optional
	ClusterSelector map[string]string `json:"clusterSelector,omitempty"`
	// ClusterAffinity is a list of cluster selector terms, the terms are ORed.
	// A empty or nil ClusterAffinity selects everything.
	// +optional
	ClusterAffinity []ClusterSelectorTerm `json:"clusterAffinity,omitempty"`
	// Tolerations describe a set of cluster taints that the policy tolerates.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// MaxClusters is the maximum number of replicas that the federated object can be propagated to.
	// The maximum number of clusters is unbounded if no value is provided.
	// +optional
	MaxClusters *int64 `json:"maxClusters,omitempty"`

	// Placement is an explicit list of clusters used to select member clusters to propagate resources to.
	// +optional
	Placements []DesiredPlacement `json:"placement,omitempty"`

	// DisableFollowerScheduling is a boolean that determines if follower scheduling is disabled.
	// Resources that depend on other resources (e.g. deployments) are called leaders,
	// and resources that are depended on (e.g. configmaps and secrets) are called followers.
	// If a leader enables follower scheduling, its followers will additionally be scheduled
	// to clusters where the leader is scheduled.
	// +optional
	DisableFollowerScheduling bool `json:"disableFollowerScheduling,omitempty"`

	// Configures behaviors related to auto migration. If absent, auto migration will be disabled.
	// +optional
	AutoMigration *AutoMigration `json:"autoMigration,omitempty"`

	// Configures behaviors related to replica rescheduling.
	// +optional
	// Default set via a post-generation patch.
	// See patch file for details.
	// Deprecated: Please use reschedulePolicy.disableRescheduling instead. This field will be removed in the next release.
	ReplicaRescheduling *ReplicaRescheduling `json:"replicaRescheduling,omitempty"`

	// Configures behaviors related to rescheduling.
	// +optional
	// +kubebuilder:default:={rescheduleWhen:{policyContentChanged:true}}
	ReschedulePolicy *ReschedulePolicy `json:"reschedulePolicy,omitempty"`
}

type PropagationPolicyStatus struct {
	GenericRefCountedStatus `json:",inline"`
}

// SchedulingMode determines the mode used by the scheduler when scheduling federated objects.
// +kubebuilder:validation:Enum=Duplicate;Divide
type SchedulingMode string

const (
	// Duplicate mode means the federated object will be duplicated to member clusters.
	SchedulingModeDuplicate SchedulingMode = "Duplicate"
	// Divide mode means the federated object's replicas will be divided between member clusters.
	SchedulingModeDivide SchedulingMode = "Divide"
)

// DesiredPlacement describes a cluster that a federated object can be propagated to and its propagation preferences.
type DesiredPlacement struct {
	// Cluster is the name of the FederatedCluster to propagate to.
	Cluster string `json:"cluster"`
	// Preferences contains the cluster's propagation preferences.
	// +optional
	Preferences Preferences `json:"preferences,omitempty"`
}

// Preferences regarding number of replicas assigned to a cluster workload object (dep, rs, ..) within
// a federated workload object.
type Preferences struct {
	// Minimum number of replicas that should be assigned to this cluster workload object. 0 by default.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MinReplicas int64 `json:"minReplicas,omitempty"`

	// Maximum number of replicas that should be assigned to this cluster workload object.
	// Unbounded if no value provided (default).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxReplicas *int64 `json:"maxReplicas,omitempty"`

	// A number expressing the preference to put an additional replica to this cluster workload object.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Weight *int64 `json:"weight,omitempty"`
}

// Preferences regarding auto migration.
type AutoMigration struct {
	// When a replica should be subject to auto migration.
	// +optional
	// +kubebuilder:default:={podUnschedulableFor:"1m"}
	Trigger AutoMigrationTrigger `json:"when"`

	// Besides starting new replicas in other cluster(s), whether to keep the unschedulable replicas
	// in the original cluster so we can go back to the desired state when the cluster recovers.
	// +optional
	// +kubebuilder:default:=false
	KeepUnschedulableReplicas bool `json:"keepUnschedulableReplicas"`
}

// Criteria for determining when a replica is subject to auto migration.
// +kubebuilder:validation:MinProperties:=1
type AutoMigrationTrigger struct {
	// A pod will be subject to auto migration if it remains unschedulable beyond this duration.
	// Duration should be specified in a format that can be parsed by Go's time.ParseDuration.
	// +optional
	// +kubebuilder:validation:Format:=duration
	PodUnschedulableDuration *metav1.Duration `json:"podUnschedulableFor,omitempty"`
}

// Preferences regarding replica rescheduling.
type ReplicaRescheduling struct {
	// If set to true, the scheduler will attempt to prevent migrating existing replicas during rescheduling.
	// In order to do so, replica scheduling preferences might not be fully respected.
	// If set to false, the scheduler will always rebalance the replicas based on the specified preferences, which might
	// cause temporary service disruption.
	// +optional
	// +kubebuilder:default:=true
	AvoidDisruption bool `json:"avoidDisruption"`
}

// ReschedulePolicy describes the rescheduling policy.
type ReschedulePolicy struct {
	// DisableRescheduling determines if a federated object can be rescheduled.
	// +optional
	DisableRescheduling bool `json:"disableRescheduling,omitempty"`
	// When the related objects should be subject to reschedule.
	// +optional
	Trigger *RescheduleTrigger `json:"rescheduleWhen,omitempty"`
	// Configures behaviors related to replica rescheduling.
	// +optional
	// Default set via a post-generation patch.
	// See patch file for details.
	ReplicaRescheduling *ReplicaRescheduling `json:"replicaRescheduling,omitempty"`
}

// RescheduleTrigger configures the criteria for triggering rescheduling.
type RescheduleTrigger struct {
	// If set to true, the scheduler will trigger rescheduling when the semantics of the policy changes. For example,
	// modifying placement, schedulingMode, maxClusters, clusterSelector, and other configurations related to
	// scheduling (includes reschedulePolicy itself) will immediately trigger rescheduling. Modifying the labels,
	// annotations, autoMigration configuration will not trigger rescheduling.
	// It set to false, the scheduler will not reschedule when the policy content changes.
	// +optional
	// +kubebuilder:default:=true
	PolicyContentChanged bool `json:"policyContentChanged"`
	// If set to true, clusters joining the federation will trigger rescheduling.
	// It set to false, the scheduler will reschedule only when other options are triggered or the replicas or the
	// requested resources of the template changed.
	// +optional
	// +kubebuilder:default:=false
	ClusterJoined bool `json:"clusterJoined"`
	// If set to true, changes to cluster labels will trigger rescheduling.
	// It set to false, the scheduler will reschedule only when other options are triggered or the replicas or the
	// requested resources of the template changed.
	// +optional
	// +kubebuilder:default:=false
	ClusterLabelsChanged bool `json:"clusterLabelsChanged"`
	// If set to true, changes to clusters' enabled list of api resources will trigger rescheduling.
	// It set to false, the scheduler will reschedule only when other options are triggered or the replicas or the
	// requested resources of the template changed.
	// +optional
	// +kubebuilder:default:=false
	ClusterAPIResourcesChanged bool `json:"clusterAPIResourcesChanged"`
}
