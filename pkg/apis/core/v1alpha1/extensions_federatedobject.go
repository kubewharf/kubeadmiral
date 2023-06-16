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
	"reflect"
	"sort"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Placement extensions

// GetPlacementUnion returns the union of all clusters listed under the Placement field of the FederatedObject.
func (o *FederatedObject) GetPlacementUnion() sets.Set[string] {
	set := sets.New[string]()
	for _, placement := range o.Spec.Placements {
		for _, cluster := range placement.Placement {
			set.Insert(cluster.Cluster)
		}
	}
	return set
}

// GetControllerPlacement returns the slice containing all the ClusterPlacements from a given controller. Returns nil if
// the controller is not present.
func (o *FederatedObject) GetControllerPlacement(controller string) []ClusterReference {
	for _, placement := range o.Spec.Placements {
		if placement.Controller == controller {
			return placement.Placement
		}
	}
	return nil
}

// SetControllerPlacement sets the ClusterPlacements for a given controller. If clusterNames is nil or empty, the previous
// placement for the given controller will be deleted. Returns a bool indicating if the FederatedObject has changed.
func (o *FederatedObject) SetControllerPlacement(controller string, clusterNames []string) bool {
	if len(clusterNames) == 0 {
		return o.DeleteControllerPlacement(controller)
	}

	newPlacement := make([]ClusterReference, len(clusterNames))
	for i, name := range clusterNames {
		newPlacement[i] = ClusterReference{Cluster: name}
	}
	// sort the clusters by name for readability and to avoid unnecessary updates
	sort.Slice(newPlacement, func(i, j int) bool {
		return newPlacement[i].Cluster < newPlacement[j].Cluster
	})

	oldPlacementWithControllerIdx := -1
	for i := range o.Spec.Placements {
		if o.Spec.Placements[i].Controller == controller {
			oldPlacementWithControllerIdx = i
			break
		}
	}

	newPlacmentWithController := PlacementWithController{
		Controller: controller,
		Placement:  newPlacement,
	}
	if oldPlacementWithControllerIdx == -1 {
		o.Spec.Placements = append(o.Spec.Placements, newPlacmentWithController)
		return true
	}
	if !reflect.DeepEqual(newPlacmentWithController, o.Spec.Placements[oldPlacementWithControllerIdx]) {
		o.Spec.Placements[oldPlacementWithControllerIdx] = newPlacmentWithController
		return true
	}

	return false
}

// DeleteClusterPlacement deletes a controller's placement, returning a bool to indicate if the FederatedObject has
// changed.
func (o *FederatedObject) DeleteControllerPlacement(controller string) bool {
	oldPlacementIdx := -1
	for i := range o.Spec.Placements {
		if o.Spec.Placements[i].Controller == controller {
			oldPlacementIdx = i
			break
		}
	}

	if oldPlacementIdx == -1 {
		return false
	}

	o.Spec.Placements = append(o.Spec.Placements[:oldPlacementIdx], o.Spec.Placements[(oldPlacementIdx+1):]...)
	return true
}

// Follower extensions

func (l *LeaderReference) GroupKind() schema.GroupKind {
	return schema.GroupKind{
		Group: l.Group,
		Kind:  l.Kind,
	}
}
