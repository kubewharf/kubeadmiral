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

import "github.com/jinzhu/copier"

// +kubebuilder:skip

// GenericObjectWithOverrides represents a generic FederatedObject and its overrides field
type GenericObjectWithOverrides struct {
	Spec *GenericSpecWithOverrides `json:"spec,omitempty"`
}

type GenericSpecWithOverrides struct {
	Overrides []ControllerOverride `json:"overrides,omitempty"`
}

type ControllerOverride struct {
	Controller string            `json:"controller"`
	Clusters   []ClusterOverride `json:"clusters"`
}

type ClusterOverride struct {
	ClusterName string          `json:"clusterName"`
	Patches     []OverridePatch `json:"paths,omitempty"`
}

// +k8s:deepcopy-gen=false

type OverridePatch struct {
	Op    string      `json:"op,omitempty"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// As controller-gen doesn't support interface member by now(2019-12-13), we hack it.
// ref: https://github.com/kubernetes-sigs/kubebuilder/issues/528
func (in *OverridePatch) DeepCopyInto(out *OverridePatch) {
	copier.Copy(out, in)
}

type OverridePatches []OverridePatch
