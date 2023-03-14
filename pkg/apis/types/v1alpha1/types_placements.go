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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:skip

// GenericObjectWithPlacements represents a generic FederatedObject and its placement field
type GenericObjectWithPlacements struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GenericSpecWithPlacements `json:"spec,omitempty"`
}

type GenericSpecWithPlacements struct {
	Placements []PlacementWithController `json:"placements,omitempty"`
}

type PlacementWithController struct {
	Controller string    `json:"controller"`
	Placement  Placement `json:"placement"`
}

type Placement struct {
	Clusters []GenericClusterReference `json:"clusters,omitempty"`
}

type GenericClusterReference struct {
	Name string `json:"name"`
}
