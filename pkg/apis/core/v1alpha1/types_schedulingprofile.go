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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=schedulingprofiles,shortName=sp,singular=schedulingprofile,scope=Cluster
// +kubebuilder:object:root=true

// SchedulingProfile configures the plugins to use when scheduling a resource
type SchedulingProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SchedulingProfileSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SchedulingProfileList contains a list of SchedulingProfile
type SchedulingProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SchedulingProfile `json:"items"`
}

type SchedulingProfileSpec struct {
	// Plugins is the list of plugins to use when scheduling a resource
	Plugins []SchedulingPlugin `json:"plugins"`
}

type SchedulingPlugin struct {
	// Name is the name of the scheduler plugin
	Name string `json:"name"`

	// Parameters is a map of custom parameters that is passed to the plugin
	// +optional
	Parameters map[string]apiextensionsv1.JSON `json:"parameters,omitempty"`
}
