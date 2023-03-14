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
)

func (object *GenericObjectWithPlacements) ClusterNameUnion() map[string]struct{} {
	set := map[string]struct{}{}
	for _, placement := range object.Spec.Placements {
		for _, cluster := range placement.Placement.Clusters {
			set[cluster.Name] = struct{}{}
		}
	}

	return set
}

func (spec *GenericSpecWithPlacements) GetPlacementOrNil(controller string) *Placement {
	for i := range spec.Placements {
		placement := &spec.Placements[i]
		if placement.Controller == controller {
			return &placement.Placement
		}
	}

	return nil
}

func (spec *GenericSpecWithPlacements) GetOrCreatePlacement(controller string) *Placement {
	for i := range spec.Placements {
		placement := &spec.Placements[i]
		if placement.Controller == controller {
			return &placement.Placement
		}
	}

	spec.Placements = append(spec.Placements, PlacementWithController{
		Controller: controller,
	})
	return &spec.Placements[len(spec.Placements)-1].Placement
}

func (spec *GenericSpecWithPlacements) DeletePlacement(controller string) (hasChange bool) {
	index := -1
	for i, placement := range spec.Placements {
		if placement.Controller == controller {
			index = i
			break
		}
	}

	if index == -1 {
		return false
	}

	spec.Placements = append(spec.Placements[:index], spec.Placements[(index+1):]...)

	return true
}

func (spec *GenericSpecWithPlacements) SetPlacementNames(controller string, newClusterNames map[string]struct{}) (hasChange bool) {
	if len(newClusterNames) == 0 {
		return spec.DeletePlacement(controller)
	}

	placement := spec.GetOrCreatePlacement(controller)
	oldClusterNames := placement.ClusterNames()

	if !reflect.DeepEqual(newClusterNames, oldClusterNames) {
		placement.Clusters = nil

		// write the clusters in ascending order for better readability
		sortedClusterNames := make([]string, 0, len(newClusterNames))
		for clusterName := range newClusterNames {
			sortedClusterNames = append(sortedClusterNames, clusterName)
		}
		sort.Strings(sortedClusterNames)
		for _, name := range sortedClusterNames {
			placement.Clusters = append(placement.Clusters, GenericClusterReference{Name: name})
		}

		return true
	}

	return false
}

func (spec *Placement) ClusterNames() map[string]struct{} {
	set := map[string]struct{}{}

	for _, cluster := range spec.Clusters {
		set[cluster.Name] = struct{}{}
	}

	return set
}

func (spec *Placement) SetClusterNames(names []string) {
	spec.Clusters = nil
	for _, name := range names {
		spec.Clusters = append(spec.Clusters, GenericClusterReference{Name: name})
	}
}
