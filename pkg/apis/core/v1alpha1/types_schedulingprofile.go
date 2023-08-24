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
	// Plugins specify the set of plugins that should be enabled or disabled.
	// Enabled plugins are the ones that should be enabled in addition to the
	// default plugins. Disabled plugins are any of the default plugins that
	// should be disabled.
	// When no enabled or disabled plugin is specified for an extension point,
	// default plugins for that extension point will be used if there is any.
	// +optional
	Plugins *Plugins `json:"plugins,omitempty"`
	// PluginConfig is an optional set of custom plugin arguments for each plugin.
	// Omitting config args for a plugin is equivalent to using the default config
	// for that plugin.
	// +optional
	PluginConfig []PluginConfig `json:"pluginConfig,omitempty"`
}

// Plugins include multiple extension points. When specified, the list of plugins for
// a particular extension point are the only ones enabled. If an extension point is
// omitted from the config, then the default set of plugins is used for that extension point.
type Plugins struct {
	// Filter is the list of plugins that should be invoked during the filter phase.
	// +optional
	Filter PluginSet `json:"filter,omitempty"`
	// Score is the list of plugins that should be invoked during the score phase.
	// +optional
	Score PluginSet `json:"score,omitempty"`
	// Select is the list of plugins that should be invoked during the select phase.
	// +optional
	Select PluginSet `json:"select,omitempty"`
}

// PluginSet contains the list of enabled and disabled plugins.
type PluginSet struct {
	// Enabled specifies plugins that should be enabled in addition to the default plugins.
	// Enabled plugins are called in the order specified here, after default plugins. If they need to
	// be invoked before default plugins, default plugins must be disabled and re-enabled here in desired order.
	// +optional
	Enabled []Plugin `json:"enabled,omitempty"`
	// Disabled specifies default plugins that should be disabled.
	// +optional
	Disabled []Plugin `json:"disabled,omitempty"`
}

// Plugin specifies a plugin type, name and its weight when applicable. Weight is used only for Score plugins.
type Plugin struct {
	// Type defines the type of the plugin. Type should be omitted when referencing in-tree plugins.
	// +optional
	Type PluginType `json:"type,omitempty"`
	// Name defines the name of the plugin.
	Name string `json:"name,omitempty"`
	// Weight defines the weight of the plugin.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Weight int64 `json:"wait,omitempty"`
}

// +kubebuilder:validation:Enum=Webhook
type PluginType string

const (
	WebhookPlugin PluginType = "Webhook"
)

const (
	// DefaultSchedulerName defines the name of default scheduler.
	DefaultSchedulerName = "default-scheduler"
)

// PluginConfig specifies arguments that should be passed to a plugin at the time of initialization.
// A plugin that is invoked at multiple extension points is initialized once. Args can have arbitrary structure.
// It is up to the plugin to process these Args.
type PluginConfig struct {
	// Name defines the name of plugin being configured.
	Name string `json:"name"`
	// Args defines the arguments passed to the plugins at the time of initialization. Args can have arbitrary structure.
	// +optional
	Args apiextensionsv1.JSON `json:"args"`
}

func (s *SchedulingProfile) ProfileName() string {
	if s == nil {
		return DefaultSchedulerName
	}
	return s.Name
}
