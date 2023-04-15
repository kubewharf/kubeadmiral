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

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=schedulerpluginwebhookconfigurations,singular=schedulerpluginwebhookconfiguration,scope=Cluster
// +kubebuilder:object:root=true

// SchedulerPluginWebhookConfiguration is a webhook that can be used as a scheduler plugin.
type SchedulerPluginWebhookConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SchedulerPluginWebhookConfigurationSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SchedulerPluginWebhookConfigurationList contains a list of SchedulerPluginWebhookConfiguration.
type SchedulerPluginWebhookConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SchedulerPluginWebhookConfiguration `json:"items"`
}

type SchedulerPluginWebhookConfigurationSpec struct {
	// PayloadVersions is an ordered list of preferred request and response
	// versions the webhook expects.
	// The scheduler will try to use the first version in
	// the list which it supports. If none of the versions specified in this list
	// supported by the scheduler, scheduling will fail for this object.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	PayloadVersions []string `json:"payloadVersions"`
	// URLPrefix at which the webhook is available
	// +kubebuilder:validation:Required
	URLPrefix string `json:"urlPrefix"`
	// Path for the filter call, empty if not supported. This path is appended to the URLPrefix when issuing the filter call to webhook.
	FilterPath string `json:"filterPath,omitempty"`
	// Path for the score call, empty if not supported. This verb is appended to the URLPrefix when issuing the score call to webhook.
	ScorePath string `json:"scorePath,omitempty"`
	// Path for the select call, empty if not supported. This verb is appended to the URLPrefix when issuing the select call to webhook.
	SelectPath string `json:"selectPath,omitempty"`
	// TLSConfig specifies the transport layer security config.
	TLSConfig *WebhookTLSConfig `json:"tlsConfig,omitempty"`
	// HTTPTimeout specifies the timeout duration for a call to the webhook. Timeout fails the scheduling of the workload.
	// Defaults to 5 seconds.
	// +kubebuilder:default:="5s"
	// +kubebuilder:validation:Format:=duration
	HTTPTimeout metav1.Duration `json:"httpTimeout,omitempty"`
}

// WebhookTLSConfig contains settings to enable TLS with the webhook server.
type WebhookTLSConfig struct {
	// Server should be accessed without verifying the TLS certificate. For testing only.
	Insecure bool `json:"insecure,omitempty"`
	// ServerName is passed to the server for SNI and is used in the client to check server
	// certificates against. If ServerName is empty, the hostname used to contact the
	// server is used.
	ServerName string `json:"serverName,omitempty"`

	// CertData holds PEM-encoded bytes (typically read from a client certificate file).
	CertData []byte `json:"certData,omitempty"`
	// KeyData holds PEM-encoded bytes (typically read from a client certificate key file).
	KeyData []byte `json:"keyData,omitempty"`
	// CAData holds PEM-encoded bytes (typically read from a root certificates bundle).
	CAData []byte `json:"caData,omitempty"`
}
