/*
Copyright 2018 The Kubernetes Authors.

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

package util

import (
	"encoding/json"
	"sort"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

// Namespace and name may not be overridden since these fields are the
// primary mechanism of association between a federated resource in
// the host cluster and the target resources in the member clusters.
//
// Kind should always be sourced from the FTC and not vary across
// member clusters.
//
// apiVersion can be overridden to support managing resources like
// Ingress which can exist in different groups at different
// versions. Users will need to take care not to abuse this
// capability.
var invalidPaths = sets.NewString(
	"/metadata/namespace",
	"/metadata/name",
	"/metadata/generateName",
	"/kind",
)

// Mapping of clusterName to overrides for the cluster
type OverridesMap map[string]fedtypesv1a1.OverridePatches

func UnmarshalGenericOverrides(uns *unstructured.Unstructured) (*fedtypesv1a1.GenericObjectWithOverrides, error) {
	obj := &fedtypesv1a1.GenericObjectWithOverrides{}
	err := UnstructuredToInterface(uns, obj)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// GetOverrides returns a map of overrides populated from the given
// unstructured object.
func GetOverrides(rawObj *unstructured.Unstructured, controller string) (OverridesMap, error) {
	overridesMap := make(OverridesMap)

	if rawObj == nil {
		return overridesMap, nil
	}

	overrideObj, err := UnmarshalGenericOverrides(rawObj)
	if err != nil {
		return nil, err
	}

	if overrideObj.Spec == nil || overrideObj.Spec.Overrides == nil {
		// No overrides defined for the federated type
		return overridesMap, nil
	}

	overrides := overrideObj.Spec.Overrides
	var clusterOverrides []fedtypesv1a1.ClusterOverride
	for i := range overrides {
		if overrides[i].Controller == controller {
			clusterOverrides = overrides[i].Clusters
			break
		}
	}

	if clusterOverrides == nil {
		return overridesMap, nil
	}

	for _, overrideItem := range clusterOverrides {
		clusterName := overrideItem.ClusterName
		if _, ok := overridesMap[clusterName]; ok {
			return nil, errors.Errorf("cluster %q appears more than once", clusterName)
		}

		for i, pathEntry := range overrideItem.Patches {
			path := pathEntry.Path
			if invalidPaths.Has(path) {
				return nil, errors.Errorf("override[%d] for cluster %q has an invalid path: %s", i, clusterName, path)
			}
		}
		overridesMap[clusterName] = overrideItem.Patches
	}

	return overridesMap, nil
}

// SetOverrides sets the spec.overrides field of the unstructured
// object from the provided overrides map.
//
// This function takes ownership of the `overridesMap` and may mutate it arbitrarily.
func SetOverrides(uns *unstructured.Unstructured, controller string, overridesMap OverridesMap) error {
	for clusterName, clusterOverrides := range overridesMap {
		if len(clusterOverrides) == 0 {
			delete(overridesMap, clusterName)
		}
	}

	overrideObj, err := UnmarshalGenericOverrides(uns)
	if err != nil {
		return err
	}

	index := -1
	for i, overrides := range overrideObj.Spec.Overrides {
		if overrides.Controller == controller {
			index = i
			break
		}
	}

	if len(overridesMap) == 0 {
		// delete index
		if index != -1 {
			overrideObj.Spec.Overrides = append(overrideObj.Spec.Overrides[:index], overrideObj.Spec.Overrides[(index+1):]...)
		}
	} else {
		if index == -1 {
			index = len(overrideObj.Spec.Overrides)
			overrideObj.Spec.Overrides = append(overrideObj.Spec.Overrides, fedtypesv1a1.ControllerOverride{
				Controller: controller,
			})
		}

		overrides := &overrideObj.Spec.Overrides[index]
		overrides.Clusters = nil

		// Write in ascending order of cluster names for better readability
		clusterNames := make([]string, 0, len(overridesMap))
		for clusterName := range overridesMap {
			clusterNames = append(clusterNames, clusterName)
		}
		sort.Strings(clusterNames)
		for _, clusterName := range clusterNames {
			clusterOverrides := overridesMap[clusterName]
			overrides.Clusters = append(overrides.Clusters, fedtypesv1a1.ClusterOverride{
				ClusterName: clusterName,
				Patches:     clusterOverrides,
			})
		}
	}

	overridesUns, err := InterfaceToUnstructured(overrideObj.Spec.Overrides)
	if err != nil {
		return err
	}

	return unstructured.SetNestedField(uns.Object, overridesUns, common.OverridesPath...)
}

// UnstructuredToInterface converts an unstructured object to the
// provided interface by json marshalling/unmarshalling.
func UnstructuredToInterface(rawObj *unstructured.Unstructured, obj interface{}) error {
	content, err := rawObj.MarshalJSON()
	if err != nil {
		return err
	}
	return json.Unmarshal(content, obj)
}

// InterfaceToUnstructured converts the provided object to an
// unstructured by json marshalling/unmarshalling.
func InterfaceToUnstructured(obj interface{}) (ret interface{}, err error) {
	var buf []byte
	buf, err = json.Marshal(obj)
	if err != nil {
		return
	}

	err = json.Unmarshal(buf, &ret)
	return
}

// ApplyJsonPatch applies the override on to the given unstructured object.
func ApplyJsonPatch(obj *unstructured.Unstructured, overrides fedtypesv1a1.OverridePatches) error {
	// TODO: Do the defaulting of "op" field to "replace" in API defaulting
	for i, overrideItem := range overrides {
		if overrideItem.Op == "" {
			overrides[i].Op = "replace"
		}
	}
	jsonPatchBytes, err := json.Marshal(overrides)
	if err != nil {
		return err
	}

	patch, err := jsonpatch.DecodePatch(jsonPatchBytes)
	if err != nil {
		return err
	}

	ObjectJSONBytes, err := obj.MarshalJSON()
	if err != nil {
		return err
	}

	patchedObjectJSONBytes, err := patch.Apply(ObjectJSONBytes)
	if err != nil {
		return err
	}

	err = obj.UnmarshalJSON(patchedObjectJSONBytes)
	return err
}
