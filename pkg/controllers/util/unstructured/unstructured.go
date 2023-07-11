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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
)

// GetInt64FromPath returns value at path (optionally under prefixFields) in the unstructured object as a metav1.LabelSelector.
// The field value must be a string or a map[string]interface{}, both of which must be convertible into a metav1.LabelSelector.
func GetLabelSelectorFromPath(obj *unstructured.Unstructured, path string, prefixFields []string) (*metav1.LabelSelector, error) {
	unsLabelSelector, exists, err := unstructured.NestedFieldNoCopy(
		obj.Object,
		SplitDotPath(path, prefixFields)...,
	)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	switch unsLabelSelector := unsLabelSelector.(type) {
	case map[string]interface{}:
		labelSelector := metav1.LabelSelector{}
		if err := pkgruntime.DefaultUnstructuredConverter.FromUnstructured(unsLabelSelector, &labelSelector); err != nil {
			return nil, fmt.Errorf("field value cannot be unmarshalled into metav1.LabelSelector: %w", err)
		}
		return &labelSelector, nil
	case string:
		if labelSelector, err := metav1.ParseToLabelSelector(unsLabelSelector); err != nil {
			return nil, fmt.Errorf("field value cannot be unmarshalled into metav1.LabelSelector: %w", err)
		} else {
			return labelSelector, nil
		}
	default:
		return nil, fmt.Errorf("field value is not a string or a map[string]interface{}")
	}
}

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
