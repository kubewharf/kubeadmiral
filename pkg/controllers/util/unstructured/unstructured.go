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

package utilunstructured

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/pointer"
)

// GetInt64Path returns the field of an unstructured object, optionally wrapped under prefixFields.
func GetInt64Path(value *unstructured.Unstructured, path string, prefixFields []string) (*int64, error) {
	for _, pathComp := range strings.Split(path, ".") {
		if pathComp != "" {
			prefixFields = append(prefixFields, pathComp)
		}
	}

	if v, exists, err := unstructured.NestedInt64(value.Object, prefixFields...); err != nil {
		return nil, fmt.Errorf("cannot access %#v %v from %v %v: %w", prefixFields, path, value.GetKind(), value.GetName(), err)
	} else if exists {
		return pointer.Int64(v), nil
	} else {
		return nil, nil
	}
}

// SetInt64Path sets the field of an unstructured object, optionally wrapped under prefixFields.
func SetInt64Path(value *unstructured.Unstructured, path string, replicas *int64, prefixFields []string) error {
	for _, pathComp := range strings.Split(path, ".") {
		if pathComp != "" {
			prefixFields = append(prefixFields, pathComp)
		}
	}

	if replicas != nil {
		if err := unstructured.SetNestedField(value.Object, *replicas, prefixFields...); err != nil {
			return fmt.Errorf("cannot set %#v %v in %v %v: %w", prefixFields, path, value.GetKind(), value.GetName(), err)
		}
	} else {
		unstructured.RemoveNestedField(value.Object, prefixFields...)
	}

	return nil
}

func ToSlashPath(dotPath string) (output string) {
	for _, pathComp := range strings.Split(dotPath, ".") {
		if pathComp != "" {
			output += "/"
			output += pathComp
		}
	}
	return output
}
