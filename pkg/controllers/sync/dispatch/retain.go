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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/util/unstructured"
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
	desiredObj, clusterObj *unstructured.Unstructured,
) error {
	// Pass the same ResourceVersion as in the cluster object for update operation, otherwise operation will fail.
	desiredObj.SetResourceVersion(clusterObj.GetResourceVersion())

	// Retain finalizers and merge annotations since they will typically be set by
	// controllers in a member cluster.  It is still possible to set the fields
	// via overrides.
	desiredObj.SetFinalizers(clusterObj.GetFinalizers())
	mergeAnnotations(desiredObj, clusterObj)
	mergeLabels(desiredObj, clusterObj)

	switch targetGvk {
	case common.ServiceGVK:
		if err := retainServiceFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case common.ServiceAccountGVK:
		if err := retainServiceAccountFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case common.JobGVK:
		if err := retainJobFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case common.PersistentVolumeGVK:
		if err := retainPersistentVolumeFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case common.PersistentVolumeClaimGVK:
		if err := retainPersistentVolumeClaimFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case common.PodGVK:
		if err := retainPodFields(desiredObj, clusterObj); err != nil {
			return err
		}
	case schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Workflow"}:
		// TODO: this is a temporary hack to support Argo Workflow.
		// We should come up with an extensible framework to support CRDs in the future.
		if err := retainArgoWorkflow(desiredObj, clusterObj); err != nil {
			return err
		}
	}

	return nil
}

func recordPropagatedLabelsAndAnnotations(obj *unstructured.Unstructured) {
	// Record the propagated annotation/label keys, so we can diff it against the cluster object during retention
	// to determine whether an annotation/label has been deleted from the template.
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 2)
	}

	annotations[common.PropagatedLabelKeys] = strings.Join(sets.List(sets.KeySet(obj.GetLabels())), ",")
	annotations[common.PropagatedAnnotationKeys] = strings.Join(sets.List(sets.KeySet(obj.GetAnnotations())), ",")

	obj.SetAnnotations(annotations)
}

func mergeAnnotations(desiredObj, clusterObj *unstructured.Unstructured) {
	mergedAnnotations := mergeStringMaps(
		desiredObj.GetAnnotations(),
		clusterObj.GetAnnotations(),
		sets.New(strings.Split(clusterObj.GetAnnotations()[common.PropagatedAnnotationKeys], ",")...),
	)
	desiredObj.SetAnnotations(mergedAnnotations)
}

func mergeLabels(desiredObj, clusterObj *unstructured.Unstructured) {
	mergedLabels := mergeStringMaps(
		desiredObj.GetLabels(),
		clusterObj.GetLabels(),
		sets.New(strings.Split(clusterObj.GetAnnotations()[common.PropagatedLabelKeys], ",")...),
	)
	desiredObj.SetLabels(mergedLabels)
}

// mergeStringMaps merges string maps (e.g. annotations or labels) from template and observed cluster objects.
// For the same key in these two maps, value from templateMap takes precedence.
// Keys deleted from templateMap are computed by taking the asymmetric set difference between lastTemplateKeys and templateMap.
func mergeStringMaps(
	templateMap, observedMap map[string]string,
	lastTemplateKeys sets.Set[string],
) map[string]string {
	if templateMap == nil {
		templateMap = make(map[string]string, len(observedMap))
	}

	deletedKeys := lastTemplateKeys.Difference(sets.KeySet(templateMap))

	for k, v := range observedMap {
		// k=v is previously propagated from template, but now removed from template
		if deletedKeys.Has(k) {
			continue
		}

		if _, ok := templateMap[k]; !ok {
			templateMap[k] = v
		}
	}

	return templateMap
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
	// A general guideline is to always drop and retain fields that are unable to be set by the user and are managed by
	// the Kubernetes control plane instead. ephemeralContainers falls into this category.
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

	if _, _, exists := findServiceAccountVolume(desiredObj); !exists {
		if volume, idx, exists := findServiceAccountVolume(clusterObj); exists {
			// If the service account volume exists in clusterObj but not in the desiredObj, it was injected by the
			// service account admission plugin. We retain the service account volume at the same index in desiredObj
			// (if we do not preserve the ordering of the volumes slice, the update will fail).
			desiredVolumes, _, _ := unstructured.NestedSlice(desiredObj.Object, "spec", "volumes") // this is a deepcopy
			if len(desiredVolumes) < idx {
				return fmt.Errorf("failed to copy service account volume, slice length mismatch")
			}

			desiredVolumes = append(desiredVolumes, nil)
			copy(desiredVolumes[idx+1:], desiredVolumes[idx:])
			desiredVolumes[idx] = volume

			if err := unstructured.SetNestedSlice(desiredObj.Object, desiredVolumes, "spec", "volumes"); err != nil {
				return err
			}
		}
	}

	desiredContainers, _, _ := unstructured.NestedSlice(desiredObj.Object, "spec", "containers") // this is a deepcopy
	clusterContainers, _, _ := unstructured.NestedSlice(clusterObj.Object, "spec", "containers") // this is a deepcopy
	if err := retainContainers(desiredContainers, clusterContainers); err != nil {
		return err
	}
	if err := unstructured.SetNestedSlice(desiredObj.Object, desiredContainers, "spec", "containers"); err != nil {
		return err
	}

	desiredInitContainers, _, _ := unstructured.NestedSlice(desiredObj.Object, "spec", "initContainers") // this is a deepcopy
	clusterInitContainers, _, _ := unstructured.NestedSlice(clusterObj.Object, "spec", "initContainers") // this is a deepcopy
	if err := retainContainers(desiredInitContainers, clusterInitContainers); err != nil {
		return err
	}
	if err := unstructured.SetNestedSlice(desiredObj.Object, desiredInitContainers, "spec", "initContainers"); err != nil {
		return err
	}

	// The tolerations field is also defaulted by an admission plugin. However, we do not need to explicitly retain
	// the default tolerations due to 2 reasons:
	// 1. The tolerations field is mutable so any updates will not result in an error
	// 2. The tolerations field will be defaulted in all create and update requests
	//
	// For example:
	// 1. We create a new pod with no tolerations and the admission plugin injects toleration A.
	// 2. The controller receives the create event and sees that toleration A should not be present.
	// 3. The controller attempts to remove toleration A with an update.
	// 4. The update is defaulted with the same toleration A, resulting in a noop update where no new watch events will be emitted.
	// 5. There is no error or infinite reconciliation.

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

// findServiceAccountVolume checks if the given pod contains a serviceaccount volume as defined by the serviceaccount
// admission plugin and returns the volume along with the index at which it is located.
//
// NOTE: This ONLY WORKS with the BoundServiceAccountTokenVolume feature enabled.
//
// Before the BoundServiceAccountTokenVolume feature was introduced, serviceaccount volumes can only be identified by
// checking if the volume has a secret volumeSource that references the secret containing the serviceaccount token. This
// would require us to send multiple requests to the apiserver or add new informers to the sync controller. We have
// decided not to support this.
func findServiceAccountVolume(pod *unstructured.Unstructured) (volume map[string]interface{}, idx int, exists bool) {
	volumes, exists, err := unstructured.NestedSlice(pod.Object, "spec", "volumes")
	if err != nil || !exists {
		return nil, 0, false
	}

	for i, v := range volumes {
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
			return volume, i, true
		}
	}

	return nil, 0, false
}

func retainContainers(desiredContainers, clusterContainers []interface{}) error {
	for _, dc := range desiredContainers {
		desiredContainer, ok := dc.(map[string]interface{})
		if !ok {
			continue
		}

		desiredName, exists, err := unstructured.NestedString(desiredContainer, "name")
		if err != nil || !exists {
			continue
		}

		// find the corresponding container in clusterObj
		for _, cc := range clusterContainers {
			clusterContainer, ok := cc.(map[string]interface{})
			if !ok {
				continue
			}

			name, exists, err := unstructured.NestedString(clusterContainer, "name")
			if err != nil || !exists {
				continue
			}

			if name == desiredName {
				if err := retainContainer(desiredContainer, clusterContainer); err != nil {
					return err
				}
				break
			}
		}
	}

	return nil
}

func retainContainer(desiredContainer, clusterContainer map[string]interface{}) error {
	if _, _, exists := findServiceAccountVolumeMount(desiredContainer); !exists {
		if volumeMnt, idx, exists := findServiceAccountVolumeMount(clusterContainer); exists {
			// The logic for retaining service account volume mounts is the same as retaining service account volumes.
			// If the service account volume mount exists in the clusterContainer but not in the desiredContainer, it
			// was injected by the service account admission plugin.
			desiredVolumeMnts, _, _ := unstructured.NestedSlice(desiredContainer, "volumeMounts") // this is a deepcopy

			// We retain the service account volume mount at the same index in desiredContainer
			if len(desiredVolumeMnts) < idx {
				return fmt.Errorf("failed to copy service account volume mount, slice length mismatch")
			}
			desiredVolumeMnts = append(desiredVolumeMnts, nil)
			copy(desiredVolumeMnts[idx+1:], desiredVolumeMnts[idx:])
			desiredVolumeMnts[idx] = volumeMnt

			if err := unstructured.SetNestedSlice(desiredContainer, desiredVolumeMnts, "volumeMounts"); err != nil {
				return err
			}
		}
	}

	return nil
}

// findServiceAccountVolumeMount checks if the given container contains a serviceaccount volume mount as defined by the
// serviceaccount admission plugin and returns the volume mount along with the index at which it is located.
func findServiceAccountVolumeMount(container map[string]interface{}) (volumeMount map[string]interface{}, idx int, exists bool) {
	volumeMounts, exists, err := unstructured.NestedSlice(container, "volumeMounts")
	if err != nil || !exists {
		return nil, 0, false
	}

	for i, m := range volumeMounts {
		volumeMount, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		mountPath, exists, err := unstructured.NestedString(volumeMount, "mountPath")
		if err != nil || !exists {
			continue
		}

		if mountPath == DefaultAPITokenMountPath {
			return volumeMount, i, true
		}
	}

	return nil, 0, false
}

func checkRetainReplicas(fedObj metav1.Object) bool {
	return fedObj.GetAnnotations()[common.RetainReplicasAnnotation] == common.AnnotationValueTrue
}

func retainReplicas(desiredObj, clusterObj *unstructured.Unstructured, fedObj metav1.Object, typeConfig *fedcorev1a1.FederatedTypeConfig) error {
	// Retain the replicas field if the federated object has been
	// configured to do so.  If the replicas field is intended to be
	// set by the in-cluster HPA controller, not retaining it will
	// thrash the scheduler.
	retain := checkRetainReplicas(fedObj)
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

func retainArgoWorkflow(desiredObj, clusterObj *unstructured.Unstructured) error {
	// Usually status is a subresource and will not be modified with an update request, i.e. it is implicitly retained.
	// If the status field is not a subresource, we need to explicitly retain it.
	if status, exists, err := unstructured.NestedFieldNoCopy(clusterObj.Object, common.StatusField); err != nil {
		return err
	} else if exists {
		if err := unstructured.SetNestedField(desiredObj.Object, status, common.StatusField); err != nil {
			return err
		}
	}

	return nil
}
