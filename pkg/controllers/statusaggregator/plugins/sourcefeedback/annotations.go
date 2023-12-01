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

package sourcefeedback

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
)

var knownAnnotations = []string{
	common.AppliedMigrationConfigurationAnnotation,
	scheduler.SchedulingAnnotation,
}

func PopulateAnnotations(
	sourceObject *unstructured.Unstructured,
	fedObject fedcorev1a1.GenericFederatedObject,
) bool {
	sourceAnnotations := sourceObject.GetAnnotations()
	needUpdate := false
	for _, annotation := range knownAnnotations {
		newSourceAnnotations, newNeedUpdate := populateAnnotation(sourceAnnotations, fedObject.GetAnnotations(), annotation)
		sourceAnnotations = newSourceAnnotations
		needUpdate = needUpdate || newNeedUpdate
	}
	sourceObject.SetAnnotations(sourceAnnotations)
	return needUpdate
}

func populateAnnotation(
	sourceAnnotations map[string]string,
	fedAnnotations map[string]string,
	key string,
) (map[string]string, bool) {
	needUpdate := false
	fedValue, fedAnnotationExists := fedAnnotations[key]
	if sourceAnnotations == nil {
		if fedAnnotationExists {
			newAnnotation := make(map[string]string, 1)
			newAnnotation[key] = fedValue
			sourceAnnotations = newAnnotation
			needUpdate = true
		}
		return sourceAnnotations, needUpdate
	}
	sourceValue, sourceAnnotationExists := sourceAnnotations[key]

	// update annotations of source object if needed
	if !fedAnnotationExists && sourceAnnotationExists {
		delete(sourceAnnotations, key)
		needUpdate = true
	}

	if fedAnnotationExists && sourceValue != fedValue {
		sourceAnnotations[key] = fedValue
		needUpdate = true
	}

	return sourceAnnotations, needUpdate
}
