//go:build exclude
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

package util

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const (
	ConflictResolutionAnnotation         = common.DefaultPrefix + "conflict-resolution"
	ConflictResolutionInternalAnnotation = common.InternalPrefix + "conflict-resolution"
)

type ConflictResolution string

const (
	// Conflict resolution for preexisting resources
	ConflictResolutionAdopt ConflictResolution = "adopt"
)

func ShouldAdoptPreexistingResources(obj *unstructured.Unstructured) bool {
	annotations := obj.GetAnnotations()

	value, exists := annotations[ConflictResolutionInternalAnnotation]
	if !exists {
		value = annotations[ConflictResolutionAnnotation]
	}

	return value == string(ConflictResolutionAdopt)
}
