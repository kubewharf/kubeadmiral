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
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/sourcefeedback"
)

func templateForSourceObject(sourceObj *unstructured.Unstructured, annotations map[string]string) *unstructured.Unstructured {
	template := sourceObj.DeepCopy()
	template.SetSelfLink("")
	template.SetUID("")
	template.SetResourceVersion("")
	template.SetGeneration(0)
	template.SetCreationTimestamp(metav1.Time{})
	template.SetDeletionTimestamp(nil)
	template.SetAnnotations(annotations)
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
	fedObj.SetLabels(sourceObj.GetLabels())
	fedObj.SetOwnerReferences(
		[]metav1.OwnerReference{*metav1.NewControllerRef(sourceObj, sourceObj.GroupVersionKind())},
	)

	federatedAnnotations, templateAnnotations := classifyAnnotations(sourceObj.GetAnnotations())
	if federatedAnnotations == nil {
		federatedAnnotations = make(map[string]string, 1)
	}
	federatedAnnotations[common.FederatedObjectAnnotation] = "1"

	fedObj.SetAnnotations(federatedAnnotations)

	if err := unstructured.SetNestedMap(
		fedObj.Object,
		templateForSourceObject(sourceObj, templateAnnotations).Object,
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

	// sync labels
	currentLabels := fedObject.GetLabels()
	desiredLabels := sourceObject.GetLabels()
	if !equality.Semantic.DeepEqual(currentLabels, desiredLabels) {
		fedObject.SetLabels(desiredLabels)
		isUpdated = true
	}

	federatedAnnotations, templateAnnotations := classifyAnnotations(sourceObject.GetAnnotations())

	// sync annotations
	if federatedAnnotations == nil {
		federatedAnnotations = make(map[string]string, 1)
	}
	federatedAnnotations[common.FederatedObjectAnnotation] = "1"
	newAnnotations, annotationChanges := annotationutil.CopySubmap(
		federatedAnnotations,
		fedObject.GetAnnotations(),
		func(key string) bool {
			return classifyAnnotation(key)&annotationClassFederated > 0
		},
	)
	if annotationChanges > 0 {
		fedObject.SetAnnotations(newAnnotations)
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
	targetTemplate := templateForSourceObject(sourceObject, templateAnnotations).Object
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

// A bitmask specifying the target copy locations of an annotation.
type annotationClass uint8

const (
	// The annotation should be copied to the federated object.
	annotationClassFederated annotationClass = 1 << iota
	// The annotation should be copied to the template.
	annotationClassTemplate
)

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
)

// Splits annotations from a source object into federated annotations and template annotations.
func classifyAnnotations(annotations map[string]string) (
	federated map[string]string,
	template map[string]string,
) {
	result := map[annotationClass]map[string]string{}

	for key, value := range annotations {
		class := classifyAnnotation(key)

		for _, checked := range []annotationClass{annotationClassFederated, annotationClassTemplate} {
			if (class & checked) > 0 {
				if _, hasMap := result[checked]; !hasMap {
					result[checked] = map[string]string{}
				}

				result[checked][key] = value
			}
		}
	}

	return result[annotationClassFederated], result[annotationClassTemplate]
}

func classifyAnnotation(annotation string) annotationClass {
	if federatedAnnotations.Has(annotation) {
		return annotationClassFederated
	} else if ignoredAnnotations.Has(annotation) {
		return 0
	} else {
		return annotationClassTemplate
	}
}
