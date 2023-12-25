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

package federatedhpa

import (
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGetGVKToScaleTargetRef(t *testing.T) {
	// Create a mock FederatedHPAController instance
	f := &FederatedHPAController{
		gvkToScaleTargetRef: map[schema.GroupVersionKind]string{
			{
				Group:   "group1",
				Version: "v1",
				Kind:    "Kind1",
			}: "target1",
			{
				Group:   "group2",
				Version: "v1",
				Kind:    "Kind2",
			}: "target2",
		},
		gvkToScaleTargetRefLock: sync.RWMutex{},
	}

	// Test case 1: Existing GVK
	val, exists := f.getGVKToScaleTargetRef(schema.GroupVersionKind{
		Group:   "group1",
		Version: "v1",
		Kind:    "Kind1",
	})
	if !exists {
		t.Errorf("Expected exists=true, but got exists=false")
	}
	if val != "target1" {
		t.Errorf("Expected val=target1, but got val=%s", val)
	}

	// Test case 2: Non-existing GVK
	val, exists = f.getGVKToScaleTargetRef(schema.GroupVersionKind{
		Group:   "group3",
		Version: "v1",
		Kind:    "Kind3",
	})
	if exists {
		t.Errorf("Expected exists=false, but got exists=true")
	}
	if val != "" {
		t.Errorf("Expected val='', but got val=%s", val)
	}
}

func TestSetGVKToScaleTargetRef(t *testing.T) {
	// Create a mock FederatedHPAController instance
	f := &FederatedHPAController{
		gvkToScaleTargetRef:     map[schema.GroupVersionKind]string{},
		gvkToScaleTargetRefLock: sync.RWMutex{},
	}

	// Test case: Set GVK to target
	gvk := schema.GroupVersionKind{
		Group:   "group1",
		Version: "v1",
		Kind:    "Kind1",
	}
	target := "target1"
	f.setGVKToScaleTargetRef(gvk, target)

	// Verify the result
	val, exists := f.getGVKToScaleTargetRef(gvk)
	if !exists {
		t.Errorf("Expected exists=true, but got exists=false")
	}
	if val != target {
		t.Errorf("Expected val=%s, but got val=%s", target, val)
	}
}

func TestDeleteGVKToScaleTargetRef(t *testing.T) {
	// Create a mock FederatedHPAController instance
	f := &FederatedHPAController{
		gvkToScaleTargetRef: map[schema.GroupVersionKind]string{
			{
				Group:   "group1",
				Version: "v1",
				Kind:    "Kind1",
			}: "target1",
		},
		gvkToScaleTargetRefLock: sync.RWMutex{},
	}

	// Test case: Delete existing GVK
	gvk := schema.GroupVersionKind{
		Group:   "group1",
		Version: "v1",
		Kind:    "Kind1",
	}
	f.deleteGVKToScaleTargetRef(gvk)

	// Verify the result
	_, exists := f.getGVKToScaleTargetRef(gvk)
	if exists {
		t.Errorf("Expected exists=false, but got exists=true")
	}
}
