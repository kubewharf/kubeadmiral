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

package common

const (
	DefaultFedSystemNamespace = "kube-admiral-system"
	DefaultPrefix             = "kubeadmiral.io/"
	InternalPrefix            = "internal." + DefaultPrefix
)

const (
	NamespaceResource = "namespaces"

	NamespaceKind             = "Namespace"
	DeploymentKind            = "Deployment"
	StatefulSetKind           = "StatefulSet"
	DaemonSetKind             = "DaemonSet"
	JobKind                   = "Job"
	CronJobKind               = "CronJob"
	ConfigMapKind             = "ConfigMap"
	SecretKind                = "Secret"
	ServiceKind               = "Service"
	ServiceAccountKind        = "ServiceAccount"
	IngressKind               = "Ingress"
	PersistentVolumeKind      = "PersistentVolume"
	PersistentVolumeClaimKind = "PersistentVolumeClaim"
)

// The following consts are spec fields used to interact with unstructured resources

const (
	// Common fields

	SpecField       = "spec"
	StatusField     = "status"
	MetadataField   = "metadata"
	GenerationField = "generation"

	// ManagedFields for serverside apply

	ManagedFields = "managedFields"

	// ServiceAccount fields

	SecretsField = "secrets"

	// Scale types

	RetainReplicasField = "retainReplicas"

	// RevisionHistoryLimit fields

	RevisionHistoryLimit = "revisionHistoryLimit"

	// Federated object fields

	TemplateField   = "template"
	PlacementsField = "placements"
	OverridesField  = "overrides"
	FollowsField    = "follows"

	// Rolling Update

	StrategyField       = "strategy"
	RollingUpdateField  = "rollingUpdate"
	MaxSurgeField       = "maxSurge"
	MaxUnavailableField = "maxUnavailable"

	// Status

	AvailableReplicasField = "availableReplicas"
)

var (
	TemplatePath   = []string{SpecField, TemplateField}
	PlacementsPath = []string{SpecField, PlacementsField}
	OverridesPath  = []string{SpecField, OverridesField}
	FollowsPath    = []string{SpecField, FollowsField}
)

// The following consts are annotatation key-values used by Kubeadmiral controllers.

const (
	AnnotationValueTrue  = "true"
	AnnotationValueFalse = "false"

	// The following annotations contain metadata.

	LastRevisionAnnotation        = DefaultPrefix + "last-revision"
	CurrentRevisionAnnotation     = DefaultPrefix + "current-revision"
	LastReplicasetName            = DefaultPrefix + "last-replicaset-name"
	SourceGenerationAnnotation    = DefaultPrefix + "source-generation"
	FederatedGenerationAnnotation = DefaultPrefix + "federated-generation"

	// The following annotations control the behavior of Kubeadmiral controllers.

	NoSchedulingAnnotation = DefaultPrefix + "no-scheduling"

	// FederatedObjectAnnotation indicates that that the object was created by the federate controller.
	FederatedObjectAnnotation = DefaultPrefix + "federated-object"

	// FollowersAnnotation indicates the additional followers of a leader.
	FollowersAnnotation = DefaultPrefix + "followers"
	// EnableFollowerSchedulingAnnotation indicates whether follower scheduling should be enabled for the leader object.
	EnableFollowerSchedulingAnnotation = InternalPrefix + "enable-follower-scheduling"

	// When a pod remains unschedulable beyond this threshold, it becomes eligible for automatic migration.
	PodUnschedulableThresholdAnnotation = InternalPrefix + "pod-unschedulable-threshold"
	// AutoMigrationAnnotation contains auto migration information.
	AutoMigrationAnnotation = DefaultPrefix + "auto-migration"
)

// The following consts are keys used to store information in the federated cluster secret

const (
	ClusterClientCertificateKey    = "client-certificate-data"
	ClusterClientKeyKey            = "client-key-data"
	ClusterCertificateAuthorityKey = "certificate-authority-data"
	ClusterServiceAccountTokenKey  = "service-account-token-data"
	ClusterServiceAccountCAKey     = "service-account-ca-data"
)
