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
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

type PropagationStatusMap map[string]fedtypesv1a1.PropagationStatus

type CollectedPropagationStatus struct {
	StatusMap        PropagationStatusMap
	GenerationMap    map[string]int64
	ResourcesUpdated bool
}

// SetFederatedStatus sets the conditions and clusters fields of the
// federated resource's object map. Returns a boolean indication of
// whether status should be written to the API.
func SetFederatedStatus(
	fedObject *unstructured.Unstructured,
	collisionCount *int32,
	reason fedtypesv1a1.AggregateReason,
	collectedStatus CollectedPropagationStatus,
) (bool, error) {
	resource := &fedtypesv1a1.GenericObjectWithStatus{}
	err := util.UnstructuredToInterface(fedObject, resource)
	if err != nil {
		return false, errors.Wrapf(err, "Failed to unmarshall to generic resource")
	}
	if resource.Status == nil {
		resource.Status = &fedtypesv1a1.GenericFederatedStatus{}
	}

	changed := update(resource.Status, fedObject.GetGeneration(), collisionCount, reason, collectedStatus)

	if !changed {
		return false, nil
	}

	resourceJSON, err := json.Marshal(resource)
	if err != nil {
		return false, errors.Wrapf(err, "Failed to marshall generic status to json")
	}
	resourceObj := &unstructured.Unstructured{}
	err = resourceObj.UnmarshalJSON(resourceJSON)
	if err != nil {
		return false, errors.Wrapf(err, "Failed to marshall generic resource json to unstructured")
	}
	fedObject.Object[common.StatusField] = resourceObj.Object[common.StatusField]

	return true, nil
}

// update ensures that the status reflects the given generation, reason
// and collected status. Returns a boolean indication of whether the
// status has been changed.
func update(
	s *fedtypesv1a1.GenericFederatedStatus,
	generation int64,
	collisionCount *int32,
	reason fedtypesv1a1.AggregateReason,
	collectedStatus CollectedPropagationStatus,
) bool {
	generationUpdated := s.ObservedGeneration != generation
	if generationUpdated {
		s.ObservedGeneration = generation
	}

	collisionCountUpdated := !reflect.DeepEqual(s.CollisionCount, collisionCount)
	if collisionCountUpdated {
		s.CollisionCount = collisionCount
	}

	// Identify whether one or more clusters could not be reconciled
	// successfully.
	if reason == fedtypesv1a1.AggregateSuccess {
		for _, value := range collectedStatus.StatusMap {
			if value != fedtypesv1a1.ClusterPropagationOK {
				reason = fedtypesv1a1.CheckClusters
				break
			}
		}
	}

	clustersChanged := setClusters(s, collectedStatus.StatusMap, collectedStatus.GenerationMap)

	// Indicate that changes were propagated if either status.clusters
	// was changed or if existing resources were updated (which could
	// occur even if status.clusters was unchanged).
	changesPropagated := clustersChanged || len(collectedStatus.StatusMap) > 0 && collectedStatus.ResourcesUpdated

	propStatusUpdated := setPropagationCondition(s, reason, changesPropagated)

	statusUpdated := generationUpdated || collisionCountUpdated || propStatusUpdated
	return statusUpdated
}

// setClusters sets the status.clusters slice from a propagation status
// map and generation map. Returns a boolean indication of whether the
// status.clusters was modified.
func setClusters(
	s *fedtypesv1a1.GenericFederatedStatus,
	statusMap PropagationStatusMap,
	generationMap map[string]int64,
) bool {
	if !clustersDiffers(s, statusMap, generationMap) {
		return false
	}
	s.Clusters = []fedtypesv1a1.GenericClusterStatus{}
	// Write status in ascending order of cluster names for better readability
	clusterNames := make([]string, 0, len(statusMap))
	for clusterName := range statusMap {
		clusterNames = append(clusterNames, clusterName)
	}
	sort.Strings(clusterNames)
	for _, clusterName := range clusterNames {
		status := statusMap[clusterName]
		s.Clusters = append(s.Clusters, fedtypesv1a1.GenericClusterStatus{
			Name:       clusterName,
			Status:     status,
			Generation: generationMap[clusterName],
		})
	}
	return true
}

// clustersDiffers checks whether `status.clusters` differs from the
// given status map and generation map.
func clustersDiffers(
	s *fedtypesv1a1.GenericFederatedStatus,
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
		if statusMap[status.Name] != status.Status {
			return true
		}
		if generationMap[status.Name] != status.Generation {
			return true
		}
	}
	return false
}

// setPropagationCondition ensures that the Propagation condition is
// updated to reflect the given reason.  The type of the condition is
// derived from the reason (empty -> True, not empty -> False).
func setPropagationCondition(s *fedtypesv1a1.GenericFederatedStatus, reason fedtypesv1a1.AggregateReason,
	changesPropagated bool,
) bool {
	// Determine the appropriate status from the reason.
	var newStatus corev1.ConditionStatus
	if reason == fedtypesv1a1.AggregateSuccess {
		newStatus = corev1.ConditionTrue
	} else {
		newStatus = corev1.ConditionFalse
	}

	if s.Conditions == nil {
		s.Conditions = []*fedtypesv1a1.GenericCondition{}
	}
	var propCondition *fedtypesv1a1.GenericCondition
	for _, condition := range s.Conditions {
		if condition.Type == fedtypesv1a1.PropagationConditionType {
			propCondition = condition
			break
		}
	}

	newCondition := propCondition == nil
	if newCondition {
		propCondition = &fedtypesv1a1.GenericCondition{
			Type: fedtypesv1a1.PropagationConditionType,
		}
		s.Conditions = append(s.Conditions, propCondition)
	}

	now := time.Now().UTC().Format(time.RFC3339)

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
