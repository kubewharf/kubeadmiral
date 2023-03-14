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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func UnmarshalGenericPlacements(uns *unstructured.Unstructured) (*fedtypesv1a1.GenericObjectWithPlacements, error) {
	placements := &fedtypesv1a1.GenericObjectWithPlacements{}
	err := UnstructuredToInterface(uns, placements)
	if err != nil {
		return nil, err
	}
	return placements, nil
}

func SetGenericPlacements(uns *unstructured.Unstructured, placements []fedtypesv1a1.PlacementWithController) error {
	unsPlacements, err := InterfaceToUnstructured(placements)
	if err != nil {
		return err
	}

	return unstructured.SetNestedField(uns.Object, unsPlacements, common.PlacementsPath...)
}

func SetPlacementClusterNames(uns *unstructured.Unstructured, controller string, clusters map[string]struct{}) (hasChange bool, err error) {
	obj, err := UnmarshalGenericPlacements(uns)
	if err != nil {
		return false, err
	}

	if hasChange := obj.Spec.SetPlacementNames(controller, clusters); !hasChange {
		return false, nil
	}

	return true, SetGenericPlacements(uns, obj.Spec.Placements)
}
