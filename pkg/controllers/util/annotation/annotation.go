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
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	SyncSuccessTimestamp       = "syncSuccessTimestamp"
	LastGeneration             = "lastGeneration"
	LastSyncSucceessGeneration = "lastSyncSucceessGeneration"
)

// HasAnnotationKey returns true if the given object has the given annotation key in its ObjectMeta.
func HasAnnotationKey(obj runtime.Object, key string) (bool, error) {
	if IsNilPointer(obj) {
		return false, fmt.Errorf("object(%T) is nil pointer", obj)
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}
	annotations := accessor.GetAnnotations()
	if annotations == nil {
		return false, nil
	}
	_, ok := annotations[key]
	return ok, nil
}

// HasAnnotationKeyValue returns true if the given object has the given annotation key and value in its ObjectMeta.
func HasAnnotationKeyValue(obj runtime.Object, key, value string) (bool, error) {
	if IsNilPointer(obj) {
		return false, fmt.Errorf("object(%T) is nil pointer", obj)
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}
	annotations := accessor.GetAnnotations()
	if annotations == nil {
		return false, nil
	}
	val, ok := annotations[key]
	return ok && value == val, nil
}

// AddAnnotation adds the given annotation key and value to the given objects ObjectMeta,
// and overwrites the annotation value if it already exists.
// Returns true if the object was updated.
func AddAnnotation(obj runtime.Object, key, value string) (bool, error) {
	if IsNilPointer(obj) {
		return false, fmt.Errorf("object(%T) is nil pointer", obj)
	}

	if key == "" {
		return false, fmt.Errorf("key is a empty string.")
	}

	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}
	has, err := HasAnnotationKeyValue(obj, key, value)
	if has && err == nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	annotations := accessor.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[key] = value
	accessor.SetAnnotations(annotations)
	return true, nil
}

// RemoveAnnotation removes the given annotation key from the given objects ObjectMeta.
// Returns true if the object was updated.
func RemoveAnnotation(obj runtime.Object, key string) (bool, error) {
	if IsNilPointer(obj) {
		return false, fmt.Errorf("object(%T) is nil pointer", obj)
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}
	has, err := HasAnnotationKey(obj, key)
	if !has && err == nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	annotations := accessor.GetAnnotations()
	if annotations == nil {
		return false, nil
	}

	delete(annotations, key)
	accessor.SetAnnotations(annotations)
	return true, nil
}

// IsNilPointer returns true if i is nil pointer or value of i is nil.
func IsNilPointer(i interface{}) bool {
	if i == nil || (reflect.ValueOf(i).Kind() == reflect.Ptr && reflect.ValueOf(i).IsNil()) {
		return true
	}
	return false
}
