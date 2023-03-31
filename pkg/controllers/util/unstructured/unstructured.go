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

// GetInt64FromPath gets an int64 value at path of an unstructured object, optionally wrapped under prefixFields.
func GetInt64FromPath(value *unstructured.Unstructured, path string, prefixFields []string) (*int64, error) {
	if v, exists, err := unstructured.NestedInt64(value.Object, SplitDotPath(path, prefixFields)...); err != nil {
		return nil, fmt.Errorf("cannot access %v: %w", prefixFields, err)
	} else if exists {
		return pointer.Int64(v), nil
	} else {
		return nil, nil
	}
}

// GetInt64FromPath sets an int64 value at path of an unstructured object, optionally wrapped under prefixFields.
func SetInt64FromPath(value *unstructured.Unstructured, path string, replicas *int64, prefixFields []string) error {
	pathSegments := SplitDotPath(path, prefixFields)

	if replicas != nil {
		if err := unstructured.SetNestedField(value.Object, *replicas, pathSegments...); err != nil {
			return fmt.Errorf("cannot set %v: %w", prefixFields, err)
		}
	} else {
		unstructured.RemoveNestedField(value.Object, pathSegments...)
	}

	return nil
}

func SplitDotPath(dotPath string, prefixFields []string) []string {
	result := prefixFields
	for _, pathComp := range strings.Split(dotPath, ".") {
		if pathComp != "" {
			result = append(result, pathComp)
		}
	}
	return result
}

func ToSlashPath(dotPath string) (output string) {
	return "/" + strings.Join(SplitDotPath(dotPath, nil), "/")
}
