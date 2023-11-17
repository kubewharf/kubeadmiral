/*
Copyright 2017 The Kubernetes Authors.

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

// Helper functions for manipulating finalizers.
package finalizers

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// HasFinalizer returns true if the given object has the given finalizer in its ObjectMeta.
func HasFinalizer(obj metav1.Object, finalizer string) bool {
	finalizers := sets.New(obj.GetFinalizers()...)
	return finalizers.Has(finalizer)
}

// AddFinalizers adds the given finalizers to the given objects ObjectMeta.
// Returns true if the object was updated.
func AddFinalizers(obj metav1.Object, newFinalizers sets.Set[string]) bool {
	oldFinalizers := sets.New(obj.GetFinalizers()...)
	if oldFinalizers.IsSuperset(newFinalizers) {
		return false
	}
	allFinalizers := oldFinalizers.Union(newFinalizers)
	obj.SetFinalizers(sets.List(allFinalizers))
	return true
}

// RemoveFinalizers removes the given finalizers from the given objects ObjectMeta.
// Returns true if the object was updated.
func RemoveFinalizers(obj metav1.Object, finalizers sets.Set[string]) bool {
	oldFinalizers := sets.New(obj.GetFinalizers()...)
	if oldFinalizers.Intersection(finalizers).Len() == 0 {
		return false
	}
	newFinalizers := oldFinalizers.Difference(finalizers)
	obj.SetFinalizers(sets.List(newFinalizers))
	return true
}
