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
	"testing"

	corev1 "k8s.io/api/core/v1"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
)

func TestGenericPropagationStatusUpdateChanged(t *testing.T) {
	testCases := map[string]struct {
		generation       int64
		collisionCount   *int32
		reason           fedtypesv1a1.AggregateReason
		statusMap        PropagationStatusMap
		resourcesUpdated bool
		expectedChanged  bool
	}{
		"No change in clusters indicates unchanged": {
			statusMap: PropagationStatusMap{
				"cluster1": fedtypesv1a1.ClusterPropagationOK,
			},
		},
		"No change in clusters with update indicates changed": {
			statusMap: PropagationStatusMap{
				"cluster1": fedtypesv1a1.ClusterPropagationOK,
			},
			resourcesUpdated: true,
			expectedChanged:  true,
		},
		"Change in clusters indicates changed": {
			expectedChanged: true,
		},
		"Transition indicates changed": {
			reason:          fedtypesv1a1.NamespaceNotFederated,
			expectedChanged: true,
		},
		"Changed generation indicates changed": {
			generation:      1,
			expectedChanged: true,
		},
	}
	for testName, tc := range testCases {
		t.Run(testName, func(t *testing.T) {
			propStatus := &fedtypesv1a1.GenericFederatedStatus{
				Clusters: []fedtypesv1a1.GenericClusterStatus{
					{
						Name:   "cluster1",
						Status: fedtypesv1a1.ClusterPropagationOK,
					},
				},
				Conditions: []*fedtypesv1a1.GenericCondition{
					{
						Type:   fedtypesv1a1.PropagationConditionType,
						Status: corev1.ConditionTrue,
					},
				},
			}
			collectedStatus := CollectedPropagationStatus{
				StatusMap:        tc.statusMap,
				ResourcesUpdated: tc.resourcesUpdated,
				GenerationMap:    map[string]int64{"cluster1": tc.generation},
			}
			changed := update(propStatus, tc.generation, tc.collisionCount, tc.reason, collectedStatus)
			if tc.expectedChanged != changed {
				t.Fatalf("Expected changed to be %v, got %v", tc.expectedChanged, changed)
			}
		})
	}
}
