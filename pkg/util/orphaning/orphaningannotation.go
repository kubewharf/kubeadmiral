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

package orphaning

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/util/adoption"
)

type OrphanManagedResourcesBehavior string

const (
	// If this annotation is present on a federated resource, it controls the
	// manner in which resources in the member clusters are orphaned when the
	// federated resource is deleted.
	// If the annotation is not present (the default), resources in member
	// clusters will be deleted before the federated resource is deleted.
	OrphanManagedResourcesAnnotation         = common.DefaultPrefix + "orphan"
	OrphanManagedResourcesInternalAnnotation = common.InternalPrefix + "orphan"

	// Orphan all managed resources
	OrphanManagedResourcesAll OrphanManagedResourcesBehavior = "all"
	// Orphan only the adopted resources
	OrphanManagedResourcesAdopted OrphanManagedResourcesBehavior = "adopted"
	// Orphaning disabled, delete managed resources
	OrphanManagedResourcesNone OrphanManagedResourcesBehavior = ""
)

func GetOrphaningBehavior(obj metav1.Object) OrphanManagedResourcesBehavior {
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

func ShouldBeOrphaned(fedObj, clusterObj metav1.Object) bool {
	orphaningBehavior := GetOrphaningBehavior(fedObj)
	return orphaningBehavior == OrphanManagedResourcesAll ||
		orphaningBehavior == OrphanManagedResourcesAdopted && adoption.HasAdoptedAnnotation(clusterObj)
}
