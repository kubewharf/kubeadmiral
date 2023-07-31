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

package status

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

type PropagationStatusMap map[string]fedcorev1a1.PropagationStatusType

type CollectedPropagationStatus struct {
	StatusMap        PropagationStatusMap
	GenerationMap    map[string]int64
	ResourcesUpdated bool
}

// SetFederatedStatus sets the conditions and clusters fields of the
// federated resource's object map. Returns a boolean indication of
// whether status should be written to the API.
func SetFederatedStatus(
	fedObject fedcorev1a1.GenericFederatedObject,
	reason fedcorev1a1.FederatedObjectConditionReason,
	collectedStatus CollectedPropagationStatus,
) bool {
	return update(fedObject.GetStatus(), fedObject.GetGeneration(), reason, collectedStatus)
}

// update ensures that the status reflects the given generation, reason
// and collected status. Returns a boolean indication of whether the
// status has been changed.
func update(
	s *fedcorev1a1.GenericFederatedObjectStatus,
	generation int64,
	reason fedcorev1a1.FederatedObjectConditionReason,
	collectedStatus CollectedPropagationStatus,
) bool {
	generationUpdated := s.SyncedGeneration != generation
	if generationUpdated {
		s.SyncedGeneration = generation
	}

	clustersChanged := setClusters(s, collectedStatus.StatusMap, collectedStatus.GenerationMap)

	// Indicate that changes were propagated if either status.clusters
	// was changed or if existing resources were updated (which could
	// occur even if status.clusters was unchanged).
	changesPropagated := clustersChanged || len(collectedStatus.StatusMap) > 0 && collectedStatus.ResourcesUpdated

	// Identify whether one or more clusters could not be reconciled
	// successfully.
	if reason == fedcorev1a1.AggregateSuccess {
		for _, value := range collectedStatus.StatusMap {
			if value != fedcorev1a1.ClusterPropagationOK {
				reason = fedcorev1a1.CheckClusters
				break
			}
		}
	}

	propStatusUpdated := setPropagationCondition(s, reason, changesPropagated)

	statusUpdated := generationUpdated || propStatusUpdated
	return statusUpdated
}

// setClusters sets the status.clusters slice from a propagation status
// map and generation map. Returns a boolean indication of whether the
// status.clusters was modified.
func setClusters(
	s *fedcorev1a1.GenericFederatedObjectStatus,
	statusMap PropagationStatusMap,
	generationMap map[string]int64,
) bool {
	if !clustersDiffers(s, statusMap, generationMap) {
		return false
	}
	s.Clusters = []fedcorev1a1.PropagationStatus{}
	// Write status in ascending order of cluster names for better readability
	clusterNames := make([]string, 0, len(statusMap))
	for clusterName := range statusMap {
		clusterNames = append(clusterNames, clusterName)
	}
	sort.Strings(clusterNames)
	for _, clusterName := range clusterNames {
		status := statusMap[clusterName]
		s.Clusters = append(s.Clusters, fedcorev1a1.PropagationStatus{
			Cluster:                clusterName,
			Status:                 status,
			LastObservedGeneration: generationMap[clusterName],
		})
	}
	return true
}

// clustersDiffers checks whether `status.clusters` differs from the
// given status map and generation map.
func clustersDiffers(
	s *fedcorev1a1.GenericFederatedObjectStatus,
	statusMap PropagationStatusMap,
	generationMap map[string]int64,
) bool {
	if len(s.Clusters) != len(statusMap) {
		return true
	}
	if len(s.Clusters) != len(generationMap) {
		return true
	}
	for _, status := range s.Clusters {
		if statusMap[status.Cluster] != status.Status {
			return true
		}
		if generationMap[status.Cluster] != status.LastObservedGeneration {
			return true
		}
	}
	return false
}

// setPropagationCondition ensures that the Propagation condition is
// updated to reflect the given reason.  The type of the condition is
// derived from the reason (empty -> True, not empty -> False).
func setPropagationCondition(
	s *fedcorev1a1.GenericFederatedObjectStatus,
	reason fedcorev1a1.FederatedObjectConditionReason,
	changesPropagated bool,
) bool {
	// Determine the appropriate status from the reason.
	var newStatus corev1.ConditionStatus
	if reason == fedcorev1a1.AggregateSuccess {
		newStatus = corev1.ConditionTrue
	} else {
		newStatus = corev1.ConditionFalse
	}

	if s.Conditions == nil {
		s.Conditions = []fedcorev1a1.GenericFederatedObjectCondition{}
	}
	var propCondition *fedcorev1a1.GenericFederatedObjectCondition
	for i := range s.Conditions {
		condition := &s.Conditions[i]
		if condition.Type == fedcorev1a1.PropagationConditionType {
			propCondition = condition
			break
		}
	}

	newCondition := propCondition == nil
	if newCondition {
		s.Conditions = append(s.Conditions, fedcorev1a1.GenericFederatedObjectCondition{
			Type: fedcorev1a1.PropagationConditionType,
		})
		propCondition = &s.Conditions[len(s.Conditions)-1]
	}

	now := metav1.Now()

	transition := newCondition || !(propCondition.Status == newStatus && propCondition.Reason == reason)
	if transition {
		propCondition.LastTransitionTime = now
		propCondition.Status = newStatus
		propCondition.Reason = reason
	}

	updateRequired := changesPropagated || transition
	if updateRequired {
		propCondition.LastUpdateTime = now
	}

	return updateRequired
}
