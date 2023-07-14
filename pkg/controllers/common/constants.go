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

import (
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	DefaultFedSystemNamespace = "kube-admiral-system"
	DefaultPrefix             = "kubeadmiral.io/"
	InternalPrefix            = "internal." + DefaultPrefix
	FederateControllerPrefix  = "federate.controller." + DefaultPrefix
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

	SourceGenerationAnnotation = DefaultPrefix + "source-generation"

	// The following annotations control the behavior of Kubeadmiral controllers.

	NoSchedulingAnnotation = DefaultPrefix + "no-scheduling"

	// RetainReplicasAnnotation indicates that the replicas field of the cluster objects should be retained during propagation.
	RetainReplicasAnnotation = DefaultPrefix + "retain-replicas"

	// FollowersAnnotation indicates the additional followers of a leader.
	FollowersAnnotation = DefaultPrefix + "followers"
	// EnableFollowerSchedulingAnnotation indicates whether follower scheduling should be enabled for the leader object.
	EnableFollowerSchedulingAnnotation = InternalPrefix + "enable-follower-scheduling"

	// When a pod remains unschedulable beyond this threshold, it becomes eligible for automatic migration.
	PodUnschedulableThresholdAnnotation = InternalPrefix + "pod-unschedulable-threshold"
	// AutoMigrationInfoAnnotation contains auto migration information.
	AutoMigrationInfoAnnotation = DefaultPrefix + "auto-migration-info"
	// ObservedAnnotationKeysAnnotation contains annotation keys observed in the last reconcile.
	// It will be in the format of `a,b|c,d`, where `a` and `b` are the keys that are synced
	// from source annotations to federated object annotations.
	ObservedAnnotationKeysAnnotation = FederateControllerPrefix + "observed-annotations"
	// ObservedLabelKeysAnnotation contains label keys observed in the last reconcile.
	// It will be in the format of `a,b|c,d`, where `a` and `b` are the keys that are synced from source labels to federated object labels.
	ObservedLabelKeysAnnotation = FederateControllerPrefix + "observed-labels"
	// TemplateGeneratorMergePatchAnnotation indicates the merge patch document capable of converting
	// the source object to the template object.
	TemplateGeneratorMergePatchAnnotation = FederateControllerPrefix + "template-generator-merge-patch"

	LatestReplicasetDigestsAnnotation = DefaultPrefix + "latest-replicaset-digests"
)

// PropagatedAnnotationKeys and PropagatedLabelKeys are used to store the keys of annotations and labels that are present
// on the resource to propagate. By persisting these, we can tell whether an annotation/label is deleted from the propagated
// and prevent accidental retention.
const (
	PropagatedAnnotationKeys = DefaultPrefix + "propagated-annotation-keys"
	PropagatedLabelKeys      = DefaultPrefix + "propagated-label-keys"
)

// The following consts are keys used to store information in the federated cluster secret

const (
	ClusterClientCertificateKey    = "client-certificate-data"
	ClusterClientKeyKey            = "client-key-data"
	ClusterCertificateAuthorityKey = "certificate-authority-data"
	ClusterServiceAccountTokenKey  = "service-account-token-data"
	ClusterServiceAccountCAKey     = "service-account-ca-data"
)

const (
	NamespaceResource  = "namespaces"
	DeploymentResource = "deployments"
	DaemonSetResource  = "daemonsets"
	ConfigMapResource  = "configmaps"
	SecretResource     = "secrets"
	PodResource        = "pods"
	ReplicaSetResource = "replicasets"

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
	PodKind                   = "Pod"
	ReplicaSetKind            = "ReplicaSet"
)

var (
	ServiceGVK               = corev1.SchemeGroupVersion.WithKind(ServiceKind)
	ServiceAccountGVK        = corev1.SchemeGroupVersion.WithKind(ServiceAccountKind)
	PersistentVolumeGVK      = corev1.SchemeGroupVersion.WithKind(PersistentVolumeKind)
	PersistentVolumeClaimGVK = corev1.SchemeGroupVersion.WithKind(PersistentVolumeClaimKind)
	PodGVK                   = corev1.SchemeGroupVersion.WithKind(PodKind)

	JobGVK = batchv1.SchemeGroupVersion.WithKind(JobKind)
)

var (
	NamespaceGVR = corev1.SchemeGroupVersion.WithResource(NamespaceResource)
	ConfigMapGVR = corev1.SchemeGroupVersion.WithResource(ConfigMapResource)
	SecretGVR    = corev1.SchemeGroupVersion.WithResource(SecretResource)
	PodGVR       = corev1.SchemeGroupVersion.WithResource(PodResource)

	DeploymentGVR = appsv1.SchemeGroupVersion.WithResource(DeploymentResource)
	DaemonSetGVR  = appsv1.SchemeGroupVersion.WithResource(DaemonSetResource)
	ReplicaSetGVR = appsv1.SchemeGroupVersion.WithResource(ReplicaSetResource)
)

// MaxFederatedObjectNameLength defines the max length of a federated object name.
// A custom resource name must be a DNS subdomain as defined in RFC1123 with a maximum length of 253.
// For more information about the custom resource validator, please refer to
// https://github.com/kubernetes/kubernetes/blob/a17149e/staging/src/k8s.io/apiextensions-apiserver/pkg/registry/customresource/validator.go#L61
//
//nolint:lll
const MaxFederatedObjectNameLength = 253
