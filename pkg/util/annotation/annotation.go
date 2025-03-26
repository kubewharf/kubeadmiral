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

package annotation

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SyncSuccessTimestamp      = "syncSuccessTimestamp"
	LastGeneration            = "lastGeneration"
	LastSyncSuccessGeneration = "lastSyncSuccessGeneration"
)

// HasAnnotationKey returns true if the given object has the given annotation key in its ObjectMeta.
func HasAnnotationKey(obj metav1.Object, key string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, ok := annotations[key]
	return ok
}

// HasAnnotationKeyValue returns true if the given object has the given annotation key and value in its ObjectMeta.
func HasAnnotationKeyValue(obj metav1.Object, key, value string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	val, ok := annotations[key]
	return ok && value == val
}

// AddAnnotation adds the given annotation key and value to the given objects ObjectMeta,
// and overwrites the annotation value if it already exists.
// Returns true if the object was updated.
func AddAnnotation(obj metav1.Object, key, value string) bool {
	has := HasAnnotationKeyValue(obj, key, value)
	if has {
		return false
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[key] = value
	obj.SetAnnotations(annotations)
	return true
}

// RemoveAnnotation removes the given annotation key from the given objects ObjectMeta.
// Returns true if the object was updated.
func RemoveAnnotation(obj metav1.Object, key string) bool {
	has := HasAnnotationKey(obj, key)
	if !has {
		return false
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}

	delete(annotations, key)
	obj.SetAnnotations(annotations)
	return true
}
