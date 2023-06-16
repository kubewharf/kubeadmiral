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

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

// PayloadVersion is the version of the payload that is used to communicate with the scheduler webhook.
const PayloadVersion = "v1alpha1"

// SchedulingUnit represents an object that is being scheduled.
type SchedulingUnit struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`

	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`

	// SchedulingMode is the scheduling mode that will be used for scheduling the Kubernetes resource. It can be
	// Duplicate or Divide.
	SchedulingMode fedcorev1a1.SchedulingMode `json:"schedulingMode"`
	// DesiredReplicas is the number of replicas requested in the template of the Kubernetes resource. If SchedulingMode
	// is Duplicated, this is the number of replicas that will be propagated to each selected cluster. If SchedulingMode
	// is Divide, this is the total number of replicas to distribute to all selected member clusters.
	// If the object is not replica-based, this field will be nil.
	DesiredReplicas *int64 `json:"desiredReplicas,omitempty"`
	// ResourceRequest is the list of resources that will be requested by each replica of the Kubernetes resource.
	ResourceRequest corev1.ResourceList `json:"resourceRequest,omitempty"`

	// CurrentClusters is the list of clusters that the object is currently scheduled to
	CurrentClusters []string `json:"currentClusters"`
	// CurrentReplicaDistribution is the number of replicas scheduled to each cluster in Divide mode. Note that this map
	// will be empty if SchedulingMode is set to Duplicate.
	CurrentReplicaDistribution map[string]int64 `json:"currentReplicaDistribution,omitempty"`

	// ClusterSelector is the ClusterSelector set in the PropagationPolicy.
	ClusterSelector map[string]string `json:"clusterSelector,omitempty"`
	// ClusterAffinity is the ClusterAffinity set in the PropagationPolicy.
	ClusterAffinity []fedcorev1a1.ClusterSelectorTerm `json:"clusterAffinity,omitempty"`
	// Tolerations is the Tolerations set in the PropagationPolicy.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// MaxClusters is the max clusters set in the PropgationPolicy.
	MaxClusters *int64 `json:"maxClusters,omitempty"`
	// Placements is the placements set in the PropgationPolicy.
	Placements []fedcorev1a1.DesiredPlacement `json:"placements,omitempty"`
}

type FilterRequest struct {
	SchedulingUnit SchedulingUnit               `json:"schedulingUnit"`
	Cluster        fedcorev1a1.FederatedCluster `json:"cluster"`
}

// TODO: allow webhook to return reason when we use reasons in the framework
type FilterResponse struct {
	Selected bool   `json:"selected"`
	Error    string `json:"error"`
}

type ScoreRequest struct {
	SchedulingUnit SchedulingUnit               `json:"schedulingUnit"`
	Cluster        fedcorev1a1.FederatedCluster `json:"cluster"`
}

type ScoreResponse struct {
	Score int64  `json:"score"`
	Error string `json:"error"`
}

type ClusterScore struct {
	Cluster fedcorev1a1.FederatedCluster `json:"cluster"`
	Score   int64                        `json:"score"`
}

type SelectRequest struct {
	SchedulingUnit SchedulingUnit `json:"schedulingUnit"`
	ClusterScores  []ClusterScore `json:"clusterScores"`
}

type SelectResponse struct {
	SelectedClusterNames []string `json:"selectedClusterNames"`
	Error                string   `json:"error"`
}
