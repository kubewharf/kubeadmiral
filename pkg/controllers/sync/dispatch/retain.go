/*
Copyright 2019 The Kubernetes Authors.

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

package dispatch

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/controllers/util/unstructured"
)

// RemovalRespectedAnnotations is a list of annotation keys whose removal will be propagated to the cluster object.
// TODO: make this configurable?
var RemovalRespectedAnnotations = sets.New(
	common.CurrentRevisionAnnotation,
	common.SourceGenerationAnnotation,
)

const (
	// see serviceaccount admission plugin in kubernetes
	ServiceAccountVolumeNamePrefix = "kube-api-access-"
	//nolint:gosec
	DefaultAPITokenMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// RetainOrMergeClusterFields updates the desired object with values retained
// from the cluster object.
func RetainOrMergeClusterFields(
	targetGvk schema.GroupVersionKind,
	desiredObj, clusterObj, fedObj *unstructured.Unstructured,
) error {
	// Pass the same ResourceVersion as in the cluster object for update operation, otherwise operation will fail.
	desiredObj.SetResourceVersion(clusterObj.GetResourceVersion())

	// Retain finalizers and merge annotations since they will typically be set by
	// controllers in a member cluster.  It is still possible to set the fields
	// via overrides.
	desiredObj.SetFinalizers(clusterObj.GetFinalizers())
	mergedAnnotations := mergeAnnotations(desiredObj.GetAnnotations(), clusterObj.GetAnnotations())
	// Propagate the removal of special annotations.
	templateAnnotations := desiredObj.GetAnnotations()
	for key := range RemovalRespectedAnnotations {
		if _, ok := templateAnnotations[key]; !ok {
			delete(mergedAnnotations, key)
		}
	}
	desiredObj.SetAnnotations(mergedAnnotations)

	switch {
	case schemautil.IsServiceGvk(targetGvk):
		if err := retainServiceFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case schemautil.IsServiceAccountGvk(targetGvk):
		if err := retainServiceAccountFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case schemautil.IsJobGvk(targetGvk):
		if err := retainJobFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case schemautil.IsPersistentVolumeGvk(targetGvk):
		if err := retainPersistentVolumeFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case schemautil.IsPersistentVolumeClaimGvk(targetGvk):
		if err := retainPersistentVolumeClaimFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case schemautil.IsPodGvk(targetGvk):
		if err := retainPodFields(desiredObj, clusterObj); err != nil {
			return err
		}
	}

	return nil
}

// mergeAnnotations merges annotations from template and cluster object.
// Annotations from clusterAnnotations are copied into templateAnnotations.
// For the same key in these two maps, value from template is preserved.
func mergeAnnotations(templateAnnotations, clusterAnnotations map[string]string) map[string]string {
	if templateAnnotations == nil {
		return clusterAnnotations
	}
	for k, v := range clusterAnnotations {
		if _, ok := templateAnnotations[k]; !ok {
			templateAnnotations[k] = v
		}
	}
	return templateAnnotations
}

func retainServiceFields(desiredObj, clusterObj *unstructured.Unstructured) error {
	// ClusterIP and NodePort are allocated to Service by cluster, so retain the same if any while updating

	// Retain clusterip
	clusterIP, ok, err := unstructured.NestedString(clusterObj.Object, "spec", "clusterIP")
	if err != nil {
		return errors.Wrap(err, "Error retrieving clusterIP from cluster service")
	}
	// !ok could indicate that a cluster ip was not assigned
	if ok && clusterIP != "" {
		err := unstructured.SetNestedField(desiredObj.Object, clusterIP, "spec", "clusterIP")
		if err != nil {
			return errors.Wrap(err, "Error setting clusterIP for service")
		}
	}

	// Retain nodeports
	clusterPorts, ok, err := unstructured.NestedSlice(clusterObj.Object, "spec", "ports")
	if err != nil {
		return errors.Wrap(err, "Error retrieving ports from cluster service")
	}
	if !ok {
		return nil
	}
	var desiredPorts []interface{}
	desiredPorts, ok, err = unstructured.NestedSlice(desiredObj.Object, "spec", "ports")
	if err != nil {
		return errors.Wrap(err, "Error retrieving ports from service")
	}
	if !ok {
		desiredPorts = []interface{}{}
	}
	for desiredIndex := range desiredPorts {
		for clusterIndex := range clusterPorts {
			fPort := desiredPorts[desiredIndex].(map[string]interface{})
			cPort := clusterPorts[clusterIndex].(map[string]interface{})
			if !(fPort["name"] == cPort["name"] && fPort["protocol"] == cPort["protocol"] && fPort["port"] == cPort["port"]) {
				continue
			}
			nodePort, ok := cPort["nodePort"]
			if ok {
				fPort["nodePort"] = nodePort
			}
		}
	}
	err = unstructured.SetNestedSlice(desiredObj.Object, desiredPorts, "spec", "ports")
	if err != nil {
		return errors.Wrap(err, "Error setting ports for service")
	}

	return nil
}

// retainServiceAccountFields retains the 'secrets' field of a service account
// if the desired representation does not include a value for the field.  This
// ensures that the sync controller doesn't continually clear a generated
// secret from a service account, prompting continual regeneration by the
// service account controller in the member cluster.
//
// TODO Clearing a manually-set secrets field will require resetting
// placement.  Is there a better way to do this?
func retainServiceAccountFields(desiredObj, clusterObj *unstructured.Unstructured) error {
	// Check whether the secrets field is populated in the desired object.
	desiredSecrets, ok, err := unstructured.NestedSlice(desiredObj.Object, common.SecretsField)
	if err != nil {
		return errors.Wrap(err, "Error retrieving secrets from desired service account")
	}
	if ok && len(desiredSecrets) > 0 {
		// Field is populated, so an update to the target resource does not
		// risk triggering a race with the service account controller.
		return nil
	}

	// Retrieve the secrets from the cluster object and retain them.
	secrets, ok, err := unstructured.NestedSlice(clusterObj.Object, common.SecretsField)
	if err != nil {
		return errors.Wrap(err, "Error retrieving secrets from service account")
	}
	if ok && len(secrets) > 0 {
		err := unstructured.SetNestedField(desiredObj.Object, secrets, common.SecretsField)
		if err != nil {
			return errors.Wrap(err, "Error setting secrets for service account")
		}
	}
	return nil
}

// retainJobFields retains .spec.selector and .spec.template.metadata.labels["job-name"]
// if .spec.manualSelector is not true.
func retainJobFields(desiredObj, clusterObj *unstructured.Unstructured) error {
	// no need to process specially if the job selector is not controller-uid
	if manualSelector, exists, err := unstructured.NestedBool(
		desiredObj.Object, "spec", "manualSelector"); err == nil && exists && manualSelector {
		return nil
	}

	// no need to consider the value of clusterObj.Spec.ManualSelector because selectors are immutable

	// otherwise, retain controller-uid in .spec.selector.matchLabels and .spec.template.labels

	selector, exists, err := unstructured.NestedFieldNoCopy(clusterObj.Object, "spec", "selector")
	if err == nil && exists {
		if err := unstructured.SetNestedField(desiredObj.Object, selector, "spec", "selector"); err != nil {
			return err
		}
	}

	labels, exists, err := unstructured.NestedFieldNoCopy(clusterObj.Object, "spec", "template", "metadata", "labels")
	if err == nil && exists {
		if err := unstructured.SetNestedField(desiredObj.Object, labels, "spec", "template", "metadata", "labels"); err != nil {
			return err
		}
	}

	return nil
}

func retainPersistentVolumeFields(desiredObj, clusterObj *unstructured.Unstructured) error {
	// We don't consider pre-binding use cases for now.
	// spec.claimRef is set by the in-cluster controller
	if claimRef, exists, err := unstructured.NestedFieldNoCopy(clusterObj.Object, "spec", "claimRef"); err == nil &&
		exists {
		if err := unstructured.SetNestedField(desiredObj.Object, claimRef, "spec", "claimRef"); err != nil {
			return err
		}
	}

	return nil
}

func retainPersistentVolumeClaimFields(desiredObj, clusterObj *unstructured.Unstructured) error {
	// If left empty in the source, spec.volumeName will be set by the in-cluster controller.
	// Otherwise, the field is immutable.
	// In both cases, it is safe to retain the value from the cluster object.
	if volumeName, exists, err := unstructured.NestedString(
		clusterObj.Object, "spec", "volumeName"); err == nil && exists {
		if err := unstructured.SetNestedField(desiredObj.Object, volumeName, "spec", "volumeName"); err != nil {
			return err
		}
	}

	return nil
}

func retainPodFields(desiredObj, clusterObj *unstructured.Unstructured) error {
	// A general guideline is to always drop and retain fields that are unable to be explicitly set by the user, and is
	// managed by the Kubernetes control plane instead. ephemeralContainers falls into this cateogry.
	if err := copyUnstructuredField(clusterObj, desiredObj, "spec", "ephemeralContainers"); err != nil {
		return err
	}

	// The following fields are fields that can be explicitly set by the user, but are defaulted by the Kubernetes
	// control plane (after creation) if left unset. For these fields, we retain the defaulted values in clusterObj if
	// the field was not explicitly set in desiredObj. Otherwise, we respect the user's choice.
	if serviceAccountName, exists, err := unstructured.NestedString(desiredObj.Object, "spec", "serviceAccountName"); err == nil &&
		(!exists || len(serviceAccountName) == 0) {
		if err := copyUnstructuredField(clusterObj, desiredObj, "spec", "serviceAccountName"); err != nil {
			return err
		}
	}

	if serviceAccount, exists, err := unstructured.NestedString(desiredObj.Object, "spec", "serviceAccount"); err == nil &&
		(!exists || len(serviceAccount) == 0) {
		if err := copyUnstructuredField(clusterObj, desiredObj, "spec", "serviceAccount"); err != nil {
			return err
		}
	}

	if nodeName, exists, err := unstructured.NestedString(desiredObj.Object, "spec", "nodeName"); err == nil &&
		(!exists || len(nodeName) == 0) {
		if err := copyUnstructuredField(clusterObj, desiredObj, "spec", "nodeName"); err != nil {
			return err
		}
	}

	if priority, exists, err := unstructured.NestedFieldNoCopy(desiredObj.Object, "spec", "priority"); err == nil &&
		(!exists || priority == nil) {
		if err := copyUnstructuredField(clusterObj, desiredObj, "spec", "priority"); err != nil {
			return err
		}
	}

	// The logic for retaining serviceaccount volumes and volumeMounts is slightly more involved.
	if !hasServiceAccountVolume(desiredObj) {
		if err := copyServiceAccountVolume(clusterObj, desiredObj); err != nil {
			return err
		}
	}

	desiredContainers, _, _ := unstructured.NestedSlice(desiredObj.Object, "spec", "containers")
	clusterContainers, _, _ := unstructured.NestedSlice(clusterObj.Object, "spec", "containers")
	if err := retainContainers(desiredContainers, clusterContainers); err != nil {
		return err
	}
	if err := unstructured.SetNestedSlice(desiredObj.Object, desiredContainers, "spec", "containers"); err != nil {
		return err
	}

	desiredInitContainers, _, _ := unstructured.NestedSlice(desiredObj.Object, "spec", "initContainers")
	clusterInitContainers, _, _ := unstructured.NestedSlice(clusterObj.Object, "spec", "initContainers")
	if err := retainContainers(desiredInitContainers, clusterInitContainers); err != nil {
		return err
	}
	if err := unstructured.SetNestedSlice(desiredObj.Object, desiredInitContainers, "spec", "initContainers"); err != nil {
		return err
	}

	// The tolerations field is also defaulted by an admission plugin. However, we do not need to explicitly retain
	// the default tolerations due to 2 reasons:
	// 1. The tolerations field is mutable so any updates will not reuslt in an error
	// 2. The same tolerations will be defaulted in any updates, so it will not result in an infinite reconcile loop
	//
	// For example:
	// 1. We create a new pod with no tolerations and the admission plugin injects toleration A.
	// 2. The controller receives the create event and sees that toleration A should not be present.
	// 3. The controller attempts to remove toleration A with an update.
	// 4. The update is defaulted with the same toleration A, resulting in a noop update and no new events will be emitted.
	// 5. Eventual consistency is achieved with no infinite reconciliation.

	return nil
}

// copyUnstructuredField copies the given field from srcObj to destObj if it exists in srcObj. An error is returned if
// the field cannot be set in destObj as one of the nesting levels is not a map[string]interface{}.
func copyUnstructuredField(srcObj, destObj *unstructured.Unstructured, fields ...string) error {
	value, exists, err := unstructured.NestedFieldNoCopy(srcObj.Object, fields...)
	if err != nil || !exists {
		return nil
	}

	return unstructured.SetNestedField(destObj.Object, value, fields...)
}

// hasServiceAccountVolume checks if the given pod contains a serviceaccount volume as defined by the serviceaccount
// admission plugin. This only works with the BoundServiceAccountTokenVolume feature enabled!!!
//
// Before the BoundServiceAccountTokenVolume feature was introduced, serviceaccount volumes can only be identified by
// checking if the volume has a secret volumeSource that references the secret containing the serviceaccount token. This
// would require us to send multiple requests to the apiserver or add new informers to the sync controller. As a result,
// we have decided not to support this.
func hasServiceAccountVolume(pod *unstructured.Unstructured) bool {
	volumes, exists, err := unstructured.NestedSlice(pod.Object, "spec", "volumes")
	if err != nil || !exists {
		return false
	}

	for _, v := range volumes {
		volume, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		name, exists, err := unstructured.NestedString(volume, "name")
		if err != nil || !exists {
			continue
		}

		// see serviceaccount admission plugin
		if strings.HasPrefix(name, ServiceAccountVolumeNamePrefix) {
			return true
		}
	}

	return false
}

// copyServiceAccountVolume copies the serviceaccount volume as defined by the serviceaccount admission plugin from
// srcPod to destPod if it exists in srcPod. This only works with the BoundServiceAccountTokenVolume feature enabled!!!
func copyServiceAccountVolume(srcPod, destPod *unstructured.Unstructured) error {
	srcVolumes, exists, err := unstructured.NestedSlice(srcPod.Object, "spec", "volumes")
	if err != nil || !exists {
		return nil
	}

	for i, v := range srcVolumes {
		volume, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		name, exists, err := unstructured.NestedString(volume, "name")
		if err != nil || !exists {
			continue
		}

		if strings.HasPrefix(name, ServiceAccountVolumeNamePrefix) {
			destVolumes, _, _ := unstructured.NestedSlice(destPod.Object, "spec", "volumes")
			if len(destVolumes) < i {
				return fmt.Errorf("failed to copy service account volume, slice length mismatch")
			}
			newVolumes := append(destVolumes[:i], volume)
			newVolumes = append(newVolumes, destVolumes[i:]...)

			return unstructured.SetNestedSlice(destPod.Object, newVolumes, "spec", "volumes")
		}
	}
	return nil
}

func retainContainers(desiredContainers, clusterContainers []interface{}) error {
	for _, dc := range desiredContainers {
		desiredContainer, ok := dc.(map[string]interface{})
		if !ok {
			continue
		}
		desiredContainerName, exists, err := unstructured.NestedString(desiredContainer, "name")
		if err != nil || !exists {
			continue
		}

		for _, cc := range clusterContainers {
			clusterContainer, ok := cc.(map[string]interface{})
			if !ok {
				continue
			}
			clusterContainerName, exists, err := unstructured.NestedString(clusterContainer, "name")
			if err != nil || !exists {
				continue
			}

			if clusterContainerName == desiredContainerName {
				if !hasServiceAccountVolumeMount(desiredContainer) {
					if err := copyServiceAccountVolumeMount(clusterContainer, desiredContainer); err != nil {
						return err
					}
				}
				break
			}
		}
	}
	return nil
}

// containsServiceAccountVolume checks if the given container contains a serviceaccount volume mount as defined by the
// serviceaccount admission plugin.
func hasServiceAccountVolumeMount(container map[string]interface{}) bool {
	volumeMounts, exists, err := unstructured.NestedSlice(container, "volumeMounts")
	if err != nil || !exists {
		return false
	}

	for _, m := range volumeMounts {
		volumeMount, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		mountPath, exists, err := unstructured.NestedString(volumeMount, "mountPath")
		if err != nil || !exists {
			continue
		}

		if mountPath == DefaultAPITokenMountPath {
			return true
		}
	}

	return false
}

// copyServiceAccountVolume copies the serviceaccount volume mount as defined by the serviceaccount admission plugin
// from srcContainer to destContainer if it exists in srcContainer.
func copyServiceAccountVolumeMount(srcContainer, destContainer map[string]interface{}) error {
	srcMounts, exists, err := unstructured.NestedSlice(srcContainer, "volumeMounts")
	if err != nil || !exists {
		return nil
	}

	for i, m := range srcMounts {
		mount, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		mountPath, exists, err := unstructured.NestedString(mount, "mountPath")
		if err != nil || !exists {
			continue
		}

		if mountPath == DefaultAPITokenMountPath {
			destMounts, _, _ := unstructured.NestedSlice(destContainer, "volumeMounts")
			if len(destMounts) < i {
				return fmt.Errorf("failed to copy service account volume mount, slice length mismatch")
			}

			newMounts := append(destMounts[:i], mount)
			newMounts = append(newMounts, destMounts[i:]...)

			return unstructured.SetNestedSlice(destContainer, newMounts, "volumeMounts")
		}
	}
	return nil
}

func checkRetainReplicas(fedObj *unstructured.Unstructured) (bool, error) {
	retainReplicas, ok, err := unstructured.NestedBool(fedObj.Object, common.SpecField, common.RetainReplicasField)
	if err != nil {
		return false, err
	}
	return ok && retainReplicas, nil
}

func retainReplicas(desiredObj, clusterObj, fedObj *unstructured.Unstructured, typeConfig *fedcorev1a1.FederatedTypeConfig) error {
	// Retain the replicas field if the federated object has been
	// configured to do so.  If the replicas field is intended to be
	// set by the in-cluster HPA controller, not retaining it will
	// thrash the scheduler.
	retain, err := checkRetainReplicas(fedObj)
	if err != nil {
		return err
	}
	if retain {
		replicas, err := utilunstructured.GetInt64FromPath(clusterObj, typeConfig.Spec.PathDefinition.ReplicasSpec, nil)
		if err != nil {
			return err
		}

		if replicas != nil {
			if err := utilunstructured.SetInt64FromPath(desiredObj, typeConfig.Spec.PathDefinition.ReplicasSpec, replicas, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func setLastReplicasetName(desiredObj, clusterObj *unstructured.Unstructured) error {
	if clusterObj == nil {
		return nil
	}
	revision, ok := desiredObj.GetAnnotations()[common.CurrentRevisionAnnotation]
	if !ok {
		return nil
	}
	lastDispatchedRevision, ok := clusterObj.GetAnnotations()[common.CurrentRevisionAnnotation]
	if ok && revision != lastDispatchedRevision {
		// update LastReplicasetName only when the revision must have been changed
		rsName, ok := clusterObj.GetAnnotations()[util.LatestReplicasetNameAnnotation]
		if !ok {
			// don't block the dispatch if the annotation is missing, validate the existence during plan initiation
			return nil
		}
		if _, err := annotationutil.AddAnnotation(desiredObj, common.LastReplicasetName, rsName); err != nil {
			return err
		}
	}
	return nil
}

func retainTemplate(
	desiredObj, clusterObj *unstructured.Unstructured,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	keepRolloutSettings bool,
) error {
	tpl, ok, err := unstructured.NestedMap(clusterObj.Object, common.SpecField, common.TemplateField)
	if err != nil {
		return err
	}
	if ok {
		if err := unstructured.SetNestedMap(desiredObj.Object, tpl, common.SpecField, common.TemplateField); err != nil {
			return err
		}
	} else {
		unstructured.RemoveNestedField(desiredObj.Object, common.SpecField, common.TemplateField)
	}

	revision, ok := clusterObj.GetAnnotations()[common.CurrentRevisionAnnotation]
	if ok {
		if _, err := annotationutil.AddAnnotation(desiredObj, common.CurrentRevisionAnnotation, revision); err != nil {
			return err
		}
	} else {
		if _, err := annotationutil.RemoveAnnotation(desiredObj, common.CurrentRevisionAnnotation); err != nil {
			return err
		}
	}

	if keepRolloutSettings {
		replicas, err := utilunstructured.GetInt64FromPath(clusterObj, typeConfig.Spec.PathDefinition.ReplicasSpec, nil)
		if err != nil {
			return err
		}

		if err := utilunstructured.SetInt64FromPath(desiredObj, typeConfig.Spec.PathDefinition.ReplicasSpec, replicas, nil); err != nil {
			return err
		}
	}

	return nil
}
