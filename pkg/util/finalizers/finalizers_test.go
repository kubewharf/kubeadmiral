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

package finalizers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func newObj(finalizers []string) metav1.Object {
	pod := corev1.Pod{}
	pod.ObjectMeta.Finalizers = finalizers
	return &pod
}

func TestHasFinalizer(t *testing.T) {
	testCases := []struct {
		obj       metav1.Object
		finalizer string
		result    bool
	}{
		{
			newObj([]string{}),
			"",
			false,
		},
		{
			newObj([]string{}),
			"someFinalizer",
			false,
		},
		{
			newObj([]string{"someFinalizer"}),
			"",
			false,
		},
		{
			newObj([]string{"someFinalizer"}),
			"anotherFinalizer",
			false,
		},
		{
			newObj([]string{"someFinalizer"}),
			"someFinalizer",
			true,
		},
		{
			newObj([]string{"anotherFinalizer", "someFinalizer"}),
			"someFinalizer",
			true,
		},
	}
	for index, test := range testCases {
		hasFinalizer := HasFinalizer(test.obj, test.finalizer)
		assert.Equal(
			t,
			hasFinalizer,
			test.result,
			fmt.Sprintf("Test case %d failed. Expected: %v, actual: %v", index, test.result, hasFinalizer),
		)
	}
}

func TestAddFinalizers(t *testing.T) {
	testCases := []struct {
		obj           metav1.Object
		finalizers    sets.Set[string]
		isUpdated     bool
		newFinalizers []string
	}{
		{
			newObj([]string{}),
			sets.New[string](),
			false,
			[]string{},
		},
		{
			newObj([]string{}),
			sets.New("someFinalizer"),
			true,
			[]string{"someFinalizer"},
		},
		{
			newObj([]string{"someFinalizer"}),
			sets.New[string](),
			false,
			[]string{"someFinalizer"},
		},
		{
			newObj([]string{"someFinalizer"}),
			sets.New("anotherFinalizer"),
			true,
			[]string{"anotherFinalizer", "someFinalizer"},
		},
		{
			newObj([]string{"someFinalizer"}),
			sets.New("someFinalizer"),
			false,
			[]string{"someFinalizer"},
		},
	}
	for index, test := range testCases {
		isUpdated := AddFinalizers(test.obj, test.finalizers)
		assert.Equal(
			t,
			isUpdated,
			test.isUpdated,
			fmt.Sprintf("Test case %d failed. Expected isUpdated: %v, actual: %v", index, test.isUpdated, isUpdated),
		)
		newFinalizers := test.obj.GetFinalizers()
		assert.Equal(
			t,
			test.newFinalizers,
			newFinalizers,
			fmt.Sprintf(
				"Test case %d failed. Expected finalizers: %v, actual: %v",
				index,
				test.newFinalizers,
				newFinalizers,
			),
		)
	}
}

func TestRemoveFinalizers(t *testing.T) {
	testCases := []struct {
		obj           metav1.Object
		finalizers    sets.Set[string]
		isUpdated     bool
		newFinalizers []string
	}{
		{
			newObj([]string{}),
			sets.New[string](),
			false,
			[]string{},
		},
		{
			newObj([]string{}),
			sets.New("someFinalizer"),
			false,
			[]string{},
		},
		{
			newObj([]string{"someFinalizer"}),
			sets.New[string](),
			false,
			[]string{"someFinalizer"},
		},
		{
			newObj([]string{"someFinalizer"}),
			sets.New("anotherFinalizer"),
			false,
			[]string{"someFinalizer"},
		},
		{
			newObj([]string{"someFinalizer", "anotherFinalizer"}),
			sets.New("someFinalizer"),
			true,
			[]string{"anotherFinalizer"},
		},
	}
	for index, test := range testCases {
		isUpdated := RemoveFinalizers(test.obj, test.finalizers)
		assert.Equal(
			t,
			isUpdated,
			test.isUpdated,
			fmt.Sprintf("Test case %d failed. Expected isUpdated: %v, actual: %v", index, test.isUpdated, isUpdated),
		)
		newFinalizers := test.obj.GetFinalizers()
		assert.Equal(
			t,
			test.newFinalizers,
			newFinalizers,
			fmt.Sprintf(
				"Test case %d failed. Expected finalizers: %v, actual: %v",
				index,
				test.newFinalizers,
				newFinalizers,
			),
		)
	}
}
