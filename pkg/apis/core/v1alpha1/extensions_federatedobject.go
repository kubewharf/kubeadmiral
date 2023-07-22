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
	"encoding/json"
	"reflect"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Implementations for GenericFederatedObject

func (o *FederatedObject) GetSpec() *GenericFederatedObjectSpec {
	return &o.Spec
}

func (o *FederatedObject) GetStatus() *GenericFederatedObjectStatus {
	return &o.Status
}

func (o *FederatedObject) DeepCopyGenericFederatedObject() GenericFederatedObject {
	return o.DeepCopy()
}

var _ GenericFederatedObject = &FederatedObject{}

func (o *ClusterFederatedObject) GetSpec() *GenericFederatedObjectSpec {
	return &o.Spec
}

func (o *ClusterFederatedObject) GetStatus() *GenericFederatedObjectStatus {
	return &o.Status
}

func (o *ClusterFederatedObject) DeepCopyGenericFederatedObject() GenericFederatedObject {
	return o.DeepCopy()
}

var _ GenericFederatedObject = &ClusterFederatedObject{}

// Placement extensions

// GetPlacementUnion returns the union of all clusters listed under the Placement field of the GenericFederatedObject.
func (spec *GenericFederatedObjectSpec) GetPlacementUnion() sets.Set[string] {
	set := sets.New[string]()
	for _, placement := range spec.Placements {
		for _, cluster := range placement.Placement {
			set.Insert(cluster.Cluster)
		}
	}
	return set
}

// GetControllerPlacement returns the slice containing all the ClusterPlacements from a given controller. Returns nil if
// the controller is not present.
func (spec *GenericFederatedObjectSpec) GetControllerPlacement(controller string) []ClusterReference {
	for _, placement := range spec.Placements {
		if placement.Controller == controller {
			return placement.Placement
		}
	}
	return nil
}

// SetControllerPlacement sets the cluster placements for a given controller. If clusterNames is nil or empty, the
// previous placement for the given controller will be deleted. Returns a bool indicating if the GenericFederatedObject
// has changed.
func (spec *GenericFederatedObjectSpec) SetControllerPlacement(controller string, clusterNames []string) bool {
	if len(clusterNames) == 0 {
		return spec.DeleteControllerPlacement(controller)
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
	for i := range spec.Placements {
		if spec.Placements[i].Controller == controller {
			oldPlacementWithControllerIdx = i
			break
		}
	}

	newPlacmentWithController := PlacementWithController{
		Controller: controller,
		Placement:  newPlacement,
	}
	if oldPlacementWithControllerIdx == -1 {
		spec.Placements = append(spec.Placements, newPlacmentWithController)
		return true
	}
	if !reflect.DeepEqual(newPlacmentWithController, spec.Placements[oldPlacementWithControllerIdx]) {
		spec.Placements[oldPlacementWithControllerIdx] = newPlacmentWithController
		return true
	}

	return false
}

// DeleteClusterPlacement deletes a controller's placement, returning a bool to indicate if the GenericFederatedObject has
// changed.
func (spec *GenericFederatedObjectSpec) DeleteControllerPlacement(controller string) bool {
	oldPlacementIdx := -1
	for i := range spec.Placements {
		if spec.Placements[i].Controller == controller {
			oldPlacementIdx = i
			break
		}
	}

	if oldPlacementIdx == -1 {
		return false
	}

	spec.Placements = append(spec.Placements[:oldPlacementIdx], spec.Placements[(oldPlacementIdx+1):]...)
	return true
}

// Overrides extensions

func (spec *GenericFederatedObjectSpec) GetControllerOverrides(controller string) []ClusterReferenceWithPatches {
	for _, overrides := range spec.Overrides {
		if overrides.Controller == controller {
			return overrides.Override
		}
	}
	return nil
}

// SetControllerOverrides sets the cluster overrides for a given controller. If clusterNames is nil or empty, the
// previous overrides for the given controller will be deleted. Returns a bool indicating if the GenericFederatedObject
// has changed.
func (spec *GenericFederatedObjectSpec) SetControllerOverrides(
	controller string,
	clusterOverrides []ClusterReferenceWithPatches,
) bool {
	if len(clusterOverrides) == 0 {
		return spec.DeleteControllerOverrides(controller)
	}

	// sort the clusters by name for readability and to avoid unnecessary updates
	sort.Slice(clusterOverrides, func(i, j int) bool {
		return clusterOverrides[i].Cluster < clusterOverrides[j].Cluster
	})

	oldOverridesWithControllerIdx := -1
	for i := range spec.Overrides {
		if spec.Overrides[i].Controller == controller {
			oldOverridesWithControllerIdx = i
			break
		}
	}

	newOverridesWithController := OverrideWithController{
		Controller: controller,
		Override:   clusterOverrides,
	}
	if oldOverridesWithControllerIdx == -1 {
		spec.Overrides = append(spec.Overrides, newOverridesWithController)
		return true
	}
	if !reflect.DeepEqual(newOverridesWithController, spec.Overrides[oldOverridesWithControllerIdx]) {
		spec.Overrides[oldOverridesWithControllerIdx] = newOverridesWithController
		return true
	}

	return false
}

// DeleteControllerOverrides deletes a controller's overrides, returning a bool to indicate if the
// GenericFederatedObject has changed.
func (spec *GenericFederatedObjectSpec) DeleteControllerOverrides(controller string) bool {
	oldOverridesIdx := -1
	for i := range spec.Overrides {
		if spec.Overrides[i].Controller == controller {
			oldOverridesIdx = i
			break
		}
	}

	if oldOverridesIdx == -1 {
		return false
	}

	spec.Overrides = append(spec.Overrides[:oldOverridesIdx], spec.Overrides[(oldOverridesIdx+1):]...)
	return true
}

// Template extensions

// GetTemplateAsUnstructured returns the FederatedObject's template unmarshalled into an *unstructured.Unstructured.
func (spec *GenericFederatedObjectSpec) GetTemplateAsUnstructured() (*unstructured.Unstructured, error) {
	template := &unstructured.Unstructured{}
	if err := template.UnmarshalJSON(spec.Template.Raw); err != nil {
		return nil, err
	}
	return template, nil
}

// GetTemplateGVK returns the GVK of the FederatedObject's source object by parsing the FederatedObject's template.
func (spec *GenericFederatedObjectSpec) GetTemplateGVK() (schema.GroupVersionKind, error) {
	type partialTypeMetadata struct {
		metav1.TypeMeta `json:",inline"`
	}
	metadata := &partialTypeMetadata{}
	if err := json.Unmarshal(spec.Template.Raw, metadata); err != nil {
		return schema.GroupVersionKind{}, nil
	}
	return metadata.GroupVersionKind(), nil
}

// Follower extensions

func (l *LeaderReference) GroupKind() schema.GroupKind {
	return schema.GroupKind{
		Group: l.Group,
		Kind:  l.Kind,
	}
}
