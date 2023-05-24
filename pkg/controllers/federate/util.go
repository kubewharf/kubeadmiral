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
	"fmt"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/nsautoprop"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
)

func templateForSourceObject(sourceObj *unstructured.Unstructured, annotations, labels map[string]string) *unstructured.Unstructured {
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

func newFederatedObjectForSourceObject(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	sourceObj *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	fedType := typeConfig.GetFederatedType()
	fedObj := &unstructured.Unstructured{
		Object: make(map[string]interface{}),
	}
	fedObj.SetAPIVersion(schema.GroupVersion{Group: fedType.Group, Version: fedType.Version}.String())
	fedObj.SetKind(fedType.Kind)
	fedObj.SetName(sourceObj.GetName())
	fedObj.SetNamespace(sourceObj.GetNamespace())
	fedObj.SetOwnerReferences(
		[]metav1.OwnerReference{*metav1.NewControllerRef(sourceObj, sourceObj.GroupVersionKind())},
	)

	federatedAnnotations, templateAnnotations := classifyAnnotations(sourceObj.GetAnnotations())
	fedObj.SetAnnotations(federatedAnnotations)

	federatedLabels, templateLabels := classifyLabels(sourceObj.GetLabels())
	fedObj.SetLabels(federatedLabels)

	if err := unstructured.SetNestedMap(
		fedObj.Object,
		templateForSourceObject(sourceObj, templateAnnotations, templateLabels).Object,
		common.SpecField,
		common.TemplateField,
	); err != nil {
		return nil, err
	}

	// For deployment fields
	if sourceObj.GroupVersionKind() == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind) {
		_, err := ensureDeploymentFields(sourceObj, fedObj)
		if err != nil {
			return nil, err
		}
	}
	return fedObj, nil
}

func updateFederatedObjectForSourceObject(
	fedObject *unstructured.Unstructured,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	sourceObject *unstructured.Unstructured,
) (bool, error) {
	isUpdated := false

	// set federated object's owner references to source object
	currentOwner := fedObject.GetOwnerReferences()
	desiredOwner := []metav1.OwnerReference{*metav1.NewControllerRef(sourceObject, sourceObject.GroupVersionKind())}
	if !reflect.DeepEqual(currentOwner, desiredOwner) {
		fedObject.SetOwnerReferences(desiredOwner)
		isUpdated = true
	}

	federatedAnnotations, templateAnnotations := classifyAnnotations(sourceObject.GetAnnotations())
	// Merge annotations because other controllers may have added annotations to the federated object.
	newAnnotations, annotationChanges := annotationutil.CopySubmap(
		federatedAnnotations,
		fedObject.GetAnnotations(),
		func(key string) bool {
			federated, _ := classifyAnnotation(key)
			return federated
		},
	)
	if annotationChanges > 0 {
		fedObject.SetAnnotations(newAnnotations)
		isUpdated = true
	}

	federatedLabels, templateLabels := classifyLabels(sourceObject.GetLabels())
	if !equality.Semantic.DeepEqual(federatedLabels, fedObject.GetLabels()) {
		fedObject.SetLabels(federatedLabels)
		isUpdated = true
	}

	// sync template
	fedObjectTemplate, foundTemplate, err := unstructured.NestedMap(
		fedObject.Object,
		common.SpecField,
		common.TemplateField,
	)
	if err != nil {
		return false, fmt.Errorf("failed to parse template from federated object: %w", err)
	}
	targetTemplate := templateForSourceObject(sourceObject, templateAnnotations, templateLabels).Object
	if !foundTemplate || !reflect.DeepEqual(fedObjectTemplate, targetTemplate) {
		if err := unstructured.SetNestedMap(fedObject.Object, targetTemplate, common.SpecField, common.TemplateField); err != nil {
			return false, fmt.Errorf("failed to set federated object template: %w", err)
		}
		isUpdated = true
	}

	// handle special deployment fields
	if sourceObject.GroupVersionKind() == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind) {
		deploymentFieldsUpdated, err := ensureDeploymentFields(sourceObject, fedObject)
		if err != nil {
			return false, fmt.Errorf("failed to ensure deployment fields: %w", err)
		}
		isUpdated = isUpdated || deploymentFieldsUpdated
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
	federatedAnnotations = sets.New(
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
	)

	// TODO: Do we need to specify the internal annotations here?
	// List of annotations that should be ignored on the source object
	ignoredAnnotations = sets.New(
		RetainReplicasAnnotation,
		util.LatestReplicasetDigestsAnnotation,
		sourcefeedback.SchedulingAnnotation,
		sourcefeedback.SyncingAnnotation,
		sourcefeedback.StatusAnnotation,
		util.ConflictResolutionInternalAnnotation,
		util.OrphanManagedResourcesInternalAnnotation,
		common.EnableFollowerSchedulingAnnotation,
	)

	federatedLabels = sets.New(
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
	if ignoredAnnotations.Has(annotation) {
		return false, false
	}

	if federatedAnnotations.Has(annotation) {
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
	if federatedLabels.Has(labelKey) {
		return true, false
	} else {
		return false, true
	}
}
