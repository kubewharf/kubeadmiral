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

package sync

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type ConflictResolution string

type OrphanManagedResourcesBehavior string

const (
	ConflictResolutionAnnotation         = common.DefaultPrefix + "conflict-resolution"
	ConflictResolutionInternalAnnotation = common.InternalPrefix + "conflict-resolution"
	AdoptedAnnotation                    = common.DefaultPrefix + "adopted"
	// If this annotation is present on a federated resource, it controls the
	// manner in which resources in the member clusters are orphaned when the
	// federated resource is deleted.
	// If the annotation is not present (the default), resources in member
	// clusters will be deleted before the federated resource is deleted.
	OrphanManagedResourcesAnnotation         = common.DefaultPrefix + "orphan"
	OrphanManagedResourcesInternalAnnotation = common.InternalPrefix + "orphan"

	// Conflict resolution for preexisting resources
	ConflictResolutionAdopt ConflictResolution = "adopt"

	// Orphan all managed resources
	OrphanManagedResourcesAll OrphanManagedResourcesBehavior = "all"
	// Orphan only the adopted resources
	OrphanManagedResourcesAdopted OrphanManagedResourcesBehavior = "adopted"
	// Orphaning disabled, delete managed resources
	OrphanManagedResourcesNone OrphanManagedResourcesBehavior = ""
)

func ShouldAdoptPreexistingResources(obj *unstructured.Unstructured) bool {
	annotations := obj.GetAnnotations()

	value, exists := annotations[ConflictResolutionInternalAnnotation]
	if !exists {
		value = annotations[ConflictResolutionAnnotation]
	}

	return value == string(ConflictResolutionAdopt)
}

func HasAdoptedAnnotation(obj *unstructured.Unstructured) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	return annotations[AdoptedAnnotation] == common.AnnotationValueTrue
}

func RemoveAdoptedAnnotation(obj *unstructured.Unstructured) {
	annotations := obj.GetAnnotations()
	if annotations == nil || annotations[AdoptedAnnotation] != common.AnnotationValueTrue {
		return
	}
	delete(annotations, AdoptedAnnotation)
	obj.SetAnnotations(annotations)
}

func GetOrphaningBehavior(obj *unstructured.Unstructured) OrphanManagedResourcesBehavior {
	annotations := obj.GetAnnotations()

	value, exists := annotations[OrphanManagedResourcesInternalAnnotation]
	if !exists {
		value = annotations[OrphanManagedResourcesAnnotation]
	}

	switch value {
	case string(OrphanManagedResourcesAll), string(OrphanManagedResourcesAdopted):
		return (OrphanManagedResourcesBehavior)(value)
	default:
		return OrphanManagedResourcesNone
	}
}
