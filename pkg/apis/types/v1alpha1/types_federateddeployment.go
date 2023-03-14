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
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:skip

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// FederatedDeployment is the Schema for the federateddeployments API
type FederatedDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FederatedDeploymentSpec `json:"spec,omitempty"`
	Status GenericFederatedStatus  `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// FederatedDeploymentList contains a list of FederatedDeployment
type FederatedDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FederatedDeployment `json:"items"`
}

// FederatedDeploymentSpec defines the desired state of FederatedDeployment
type FederatedDeploymentSpec struct {
	GenericSpecWithPlacements `json:",inline"`
	GenericSpecWithOverrides  `json:",inline"`

	Template appsv1.Deployment `json:"template,omitempty"`

	// revisionHistoryLimit is the maximum number of revisions that will
	// be maintained in the FederatedDeployment's revision history. The revision history
	// consists of all revisions not represented by a currently applied
	// FederatedDeploymentSpec version. The default value is 10.

	// +kubebuilder:default:=10
	RevisionHistoryLimit int64 `json:"revisionHistoryLimit,omitempty"`

	RetainReplicas bool `json:"retainReplicas,omitempty"`
}
