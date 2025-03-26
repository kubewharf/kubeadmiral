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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newObj(annotation map[string]string) metav1.Object {
	pod := corev1.Pod{}
	pod.ObjectMeta.Annotations = annotation
	return &pod
}

func TestHasAnnotationKey(t *testing.T) {
	testCases := []struct {
		obj        metav1.Object
		annotation string
		result     bool
	}{
		{
			newObj(map[string]string{}),
			"someAnnotation",
			false,
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"",
			false,
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"anotherAnnotation",
			false,
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"someAnnotation",
			true,
		},
		{
			newObj(map[string]string{"someAnnotation": "", "anotherAnnotation": ""}),
			"someAnnotation",
			true,
		},
	}
	for index, test := range testCases {
		hasAnnotationKey := HasAnnotationKey(test.obj, test.annotation)
		assert.Equal(
			t,
			hasAnnotationKey,
			test.result,
			fmt.Sprintf("Test case %d failed. Expected: %v, actual: %v", index, test.result, hasAnnotationKey),
		)
	}
}

func TestHasAnnotationKeyValue(t *testing.T) {
	testCases := []struct {
		obj    metav1.Object
		key    string
		value  string
		result bool
	}{
		{
			newObj(map[string]string{}),
			"someAnnotation",
			"",
			false,
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"anotherAnnotation",
			"",
			false,
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"someAnnotation",
			"",
			true,
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"someAnnotation",
			"someValue",
			false,
		},
		{
			newObj(map[string]string{"someAnnotation": "someValue"}),
			"someAnnotation",
			"someValue",
			true,
		},
		{
			newObj(map[string]string{"someAnnotation": "someValue", "anotherAnnotation": ""}),
			"someAnnotation",
			"someValue",
			true,
		},
	}
	for index, test := range testCases {
		hasAnnotationKeyValue := HasAnnotationKeyValue(test.obj, test.key, test.value)
		assert.Equal(
			t,
			hasAnnotationKeyValue,
			test.result,
			fmt.Sprintf("Test case %d failed. Expected: %v, actual: %v", index, test.result, hasAnnotationKeyValue),
		)
	}
}

func TestAddAnnotation(t *testing.T) {
	testCases := []struct {
		obj            metav1.Object
		key            string
		value          string
		isUpdated      bool
		newAnnotations map[string]string
	}{
		{
			newObj(map[string]string{}),
			"someAnnotation",
			"",
			true,
			map[string]string{"someAnnotation": ""},
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"someAnnotation",
			"",
			false,
			map[string]string{"someAnnotation": ""},
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"someAnnotation",
			"someValue",
			true,
			map[string]string{"someAnnotation": "someValue"},
		},
		{
			newObj(map[string]string{"someAnnotation": "", "anotherAnnotation": "anotherValue"}),
			"someAnnotation",
			"",
			false,
			map[string]string{"someAnnotation": "", "anotherAnnotation": "anotherValue"},
		},
		{
			newObj(map[string]string{"someAnnotation": "", "anotherAnnotation": "anotherValue"}),
			"someAnnotation",
			"someValue",
			true,
			map[string]string{"someAnnotation": "someValue", "anotherAnnotation": "anotherValue"},
		},
	}
	for index, test := range testCases {
		isUpdated := AddAnnotation(test.obj, test.key, test.value)
		assert.Equal(
			t,
			isUpdated,
			test.isUpdated,
			fmt.Sprintf("Test case %d failed. Expected isUpdated: %v, actual: %v", index, test.isUpdated, isUpdated),
		)
		newAnnotations := test.obj.GetAnnotations()
		assert.Equal(
			t,
			test.newAnnotations,
			newAnnotations,
			fmt.Sprintf(
				"Test case %d failed. Expected finalizers: %v, actual: %v",
				index,
				test.newAnnotations,
				newAnnotations,
			),
		)
	}
}

func TestRemoveAnnotation(t *testing.T) {
	testCases := []struct {
		obj            metav1.Object
		key            string
		isUpdated      bool
		newAnnotations map[string]string
	}{
		{
			newObj(map[string]string{}),
			"someAnnotation",
			false,
			map[string]string{},
		},
		{
			newObj(map[string]string{"someAnnotation": ""}),
			"someAnnotation",
			true,
			map[string]string{},
		},
		{
			newObj(map[string]string{"someAnnotation": "", "anotherAnnotation": "anotherValue"}),
			"someAnnotation",
			true,
			map[string]string{"anotherAnnotation": "anotherValue"},
		},
	}
	for index, test := range testCases {
		isUpdated := RemoveAnnotation(test.obj, test.key)
		assert.Equal(
			t,
			isUpdated,
			test.isUpdated,
			fmt.Sprintf("Test case %d failed. Expected isUpdated: %v, actual: %v", index, test.isUpdated, isUpdated),
		)
		newAnnotations := test.obj.GetAnnotations()
		assert.Equal(
			t,
			test.newAnnotations,
			newAnnotations,
			fmt.Sprintf(
				"Test case %d failed. Expected finalizers: %v, actual: %v",
				index,
				test.newAnnotations,
				newAnnotations,
			),
		)
	}
}
