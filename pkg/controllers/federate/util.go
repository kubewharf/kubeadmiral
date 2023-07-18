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

package federate

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/nsautoprop"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
)

type workerKey struct {
	name      string
	namespace string
	ftc       *fedcorev1a1.FederatedTypeConfig
}

func (k workerKey) String() string {
	return fmt.Sprintf("%s/%s", k.namespace, k.name)
}

func templateForSourceObject(
	sourceObj *unstructured.Unstructured,
	annotations, labels map[string]string,
) *unstructured.Unstructured {
	template := sourceObj.DeepCopy()
	template.SetSelfLink("")
	template.SetUID("")
	template.SetResourceVersion("")
	template.SetGeneration(0)
	template.SetCreationTimestamp(metav1.Time{})
	template.SetDeletionTimestamp(nil)
	template.SetAnnotations(annotations)
	template.SetLabels(labels)
	template.SetOwnerReferences(nil)
	template.SetFinalizers(nil)
	template.SetManagedFields(nil)
	unstructured.RemoveNestedField(template.Object, common.StatusField)
	return template
}

func newFederatedObjectForSourceObject(ftc *fedcorev1a1.FederatedTypeConfig, sourceObj *unstructured.Unstructured) (*fedcorev1a1.GenericFederatedObject, error) {
	fedObj := &fedcorev1a1.GenericFederatedObject{}
	fedName := naming.GenerateFederatedObjectName(sourceObj.GetName(), ftc.Name)

	fedObj.SetName(fedName)
	fedObj.SetNamespace(sourceObj.GetNamespace())
	fedObj.SetOwnerReferences(
		[]metav1.OwnerReference{*metav1.NewControllerRef(sourceObj, sourceObj.GroupVersionKind())},
	)

	// Classify labels into labels that should be copied onto the FederatedObject and labels that should be copied onto
	// the FederatedObject's template.

	federatedLabels, templateLabels := classifyLabels(sourceObj.GetLabels())

	// Classify annotations into annotations that should be copied onto the FederatedObject and labels that should be
	// copied onto the FederatedObject's template.

	federatedAnnotations, templateAnnotations := classifyAnnotations(sourceObj.GetAnnotations())
	if federatedAnnotations == nil {
		federatedAnnotations = make(map[string]string)
	}

	// Record the observed label and annotation keys in an annotation on the FederatedObject.

	observedLabelKeys := generateObservedKeys(sourceObj.GetLabels(), federatedLabels)
	observedAnnotationKeys := generateObservedKeys(sourceObj.GetAnnotations(), federatedAnnotations)
	federatedAnnotations[common.ObservedAnnotationKeysAnnotation] = observedAnnotationKeys
	federatedAnnotations[common.ObservedLabelKeysAnnotation] = observedLabelKeys

	// Generate the FederatedObject's template and update the FederatedObject.

	templateObject := templateForSourceObject(sourceObj, templateAnnotations, templateLabels).Object
	rawTemplate, err := json.Marshal(templateObject)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal template: %w", err)
	}
	fedObj.Spec.Template.Raw = rawTemplate

	// Generate the JSON patch required to convert the source object to the FederatedObject's template and store it as
	// an annotation in the FederatedObject.

	templateGeneratorMergePatch, err := CreateMergePatch(sourceObj, &unstructured.Unstructured{Object: templateObject})
	if err != nil {
		return nil, fmt.Errorf("failed to create merge patch for source object: %w", err)
	}
	federatedAnnotations[common.TemplateGeneratorMergePatchAnnotation] = string(templateGeneratorMergePatch)

	// Update the FederatedObject with the final annotation and label sets.

	fedObj.SetLabels(federatedLabels)
	fedObj.SetAnnotations(federatedAnnotations)

	return fedObj, nil
}

func updateFederatedObjectForSourceObject(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	sourceObject *unstructured.Unstructured,
	fedObject *fedcorev1a1.GenericFederatedObject,
) (bool, error) {
	isUpdated := false

	// Set federated object's owner references to source object

	currentOwner := fedObject.GetOwnerReferences()
	desiredOwner := []metav1.OwnerReference{*metav1.NewControllerRef(sourceObject, sourceObject.GroupVersionKind())}
	if !reflect.DeepEqual(currentOwner, desiredOwner) {
		fedObject.SetOwnerReferences(desiredOwner)
		isUpdated = true
	}

	// Classify labels into labels that should be copied onto the FederatedObject and labels that should be copied onto
	// the FederatedObject's template and update the FederatedObject's template.

	federatedLabels, templateLabels := classifyLabels(sourceObject.GetLabels())
	if !equality.Semantic.DeepEqual(federatedLabels, fedObject.GetLabels()) {
		fedObject.SetLabels(federatedLabels)
		isUpdated = true
	}

	// Classify annotations into annotations that should be copied onto the FederatedObject and labels that should be
	// copied onto the FederatedObject's template.

	federatedAnnotations, templateAnnotations := classifyAnnotations(sourceObject.GetAnnotations())

	// Generate the FederatedObject's template and compare it to the template in the FederatedObject, updating the
	// FederatedObject if necessary.

	targetTemplate := templateForSourceObject(sourceObject, templateAnnotations, templateLabels)
	foundTemplate := &unstructured.Unstructured{}
	if err := json.Unmarshal(fedObject.Spec.Template.Raw, foundTemplate); err != nil {
		return false, fmt.Errorf("failed to unmarshal template from federated object: %w", err)
	}
	if !reflect.DeepEqual(foundTemplate.Object, targetTemplate.Object) {
		rawTargetTemplate, err := json.Marshal(targetTemplate)
		if err != nil {
			return false, fmt.Errorf("failed to marshal template: %w", err)
		}

		fedObject.Spec.Template.Raw = rawTargetTemplate
		isUpdated = true
	}

	// Merge annotations because other controllers may have added annotations to the federated object.

	newAnnotations, annotationChanges := annotationutil.CopySubmap(
		federatedAnnotations,
		fedObject.GetAnnotations(),
		func(key string) bool {
			federated, _ := classifyAnnotation(key)
			return federated
		},
	)

	// Record the observed label and annotation keys in an annotation on the FederatedObject.

	observedAnnotationKeys := generateObservedKeys(sourceObject.GetAnnotations(), federatedAnnotations)
	observedLabelKeys := generateObservedKeys(sourceObject.GetLabels(), federatedLabels)


	// Generate the JSON patch required to convert the source object to the FederatedObject's template and store it as
	// an annotation in the FederatedObject.

	templateGeneratorMergePatch, err := CreateMergePatch(sourceObject, targetTemplate)
	if err != nil {
		return false, fmt.Errorf("failed to create merge patch for source object: %w", err)
	}

	for key, desiredValue := range map[string]string{
		common.ObservedAnnotationKeysAnnotation:      observedAnnotationKeys,
		common.ObservedLabelKeysAnnotation:           observedLabelKeys,
		common.TemplateGeneratorMergePatchAnnotation: string(templateGeneratorMergePatch),
	} {
		existingValue, exist := newAnnotations[key]
		if !exist || existingValue != desiredValue {
			newAnnotations[key] = desiredValue
			annotationChanges++
		}
	}

	if annotationChanges > 0 {
		fedObject.SetAnnotations(newAnnotations)
		isUpdated = true
	}

	if isUpdated {
		_, err = pendingcontrollers.SetPendingControllers(fedObject, typeConfig.GetControllers())
		if err != nil {
			return false, fmt.Errorf("failed to set pending controllers for federated object: %w", err)
		}
	}

	return isUpdated, nil
}

var (
	// List of annotations that should be copied to the federated object instead of the template from the source
	federatedAnnotationSet = sets.New(
		scheduler.SchedulingModeAnnotation,
		scheduler.StickyClusterAnnotation,
		util.ConflictResolutionAnnotation,
		nsautoprop.NoAutoPropagationAnnotation,
		util.OrphanManagedResourcesAnnotation,
		scheduler.TolerationsAnnotations,
		scheduler.PlacementsAnnotations,
		scheduler.ClusterSelectorAnnotations,
		scheduler.AffinityAnnotations,
		scheduler.MaxClustersAnnotations,
		common.NoSchedulingAnnotation,
		scheduler.FollowsObjectAnnotation,
		common.FollowersAnnotation,
		RetainReplicasAnnotation,
	)

	// TODO: Do we need to specify the internal annotations here?
	// List of annotations that should be ignored on the source object
	ignoredAnnotationSet = sets.New(
		util.LatestReplicasetDigestsAnnotation,
		sourcefeedback.SchedulingAnnotation,
		sourcefeedback.SyncingAnnotation,
		sourcefeedback.StatusAnnotation,
		util.ConflictResolutionInternalAnnotation,
		util.OrphanManagedResourcesInternalAnnotation,
		common.EnableFollowerSchedulingAnnotation,
	)

	federatedLabelSet = sets.New(
		scheduler.PropagationPolicyNameLabel,
		scheduler.ClusterPropagationPolicyNameLabel,
		override.OverridePolicyNameLabel,
		override.ClusterOverridePolicyNameLabel,
	)
)

func classifyStringMap(
	src map[string]string,
	matcher func(key string) (federated, template bool),
) (federatedMap, templateMap map[string]string) {
	federatedMap = make(map[string]string, len(src))
	templateMap = make(map[string]string, len(src))

	for key, value := range src {
		federated, template := matcher(key)
		if federated {
			federatedMap[key] = value
		}
		if template {
			templateMap[key] = value
		}
	}

	return federatedMap, templateMap
}

// Splits annotations from a source object into federated annotations and template annotations.
func classifyAnnotations(annotations map[string]string) (
	federatedAnnotations map[string]string,
	templateAnnotations map[string]string,
) {
	federatedAnnotations, templateAnnotations = classifyStringMap(annotations, classifyAnnotation)
	federatedAnnotations[common.FederatedObjectAnnotation] = "1"
	return federatedAnnotations, templateAnnotations
}

func classifyAnnotation(annotation string) (federated, template bool) {
	if ignoredAnnotationSet.Has(annotation) {
		return false, false
	}

	if federatedAnnotationSet.Has(annotation) {
		return true, false
	} else {
		return false, true
	}
}

func classifyLabels(labels map[string]string) (
	federatedLabels map[string]string,
	templateLabels map[string]string,
) {
	return classifyStringMap(labels, classifyLabel)
}

func classifyLabel(labelKey string) (federated, template bool) {
	if federatedLabelSet.Has(labelKey) {
		return true, false
	} else {
		return false, true
	}
}

func generateObservedKeys(sourceMap map[string]string, federatedMap map[string]string) string {
	if len(sourceMap) == 0 {
		return ""
	}

	var observedFederatedKeys, observedNonFederatedKeys []string
	for key := range sourceMap {
		if _, exist := federatedMap[key]; exist {
			observedFederatedKeys = append(observedFederatedKeys, key)
		} else {
			observedNonFederatedKeys = append(observedNonFederatedKeys, key)
		}
	}

	sort.Strings(observedFederatedKeys)
	sort.Strings(observedNonFederatedKeys)
	return strings.Join(
		[]string{strings.Join(observedFederatedKeys, ","), strings.Join(observedNonFederatedKeys, ",")},
		"|",
	)
}

// CreateMergePatch will return a merge patch document capable of converting
// the source object to the target object.
func CreateMergePatch(sourceObject interface{}, targetObject interface{}) ([]byte, error) {
	sourceJSON, err := json.Marshal(sourceObject)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal source object: %w", err)
	}

	targetJSON, err := json.Marshal(targetObject)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal target object: %w", err)
	}
	patchBytes, err := jsonpatch.CreateMergePatch(sourceJSON, targetJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to create a merge patch: %w", err)
	}

	return patchBytes, nil
}
