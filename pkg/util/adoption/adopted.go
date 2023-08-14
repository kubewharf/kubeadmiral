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

package adoption

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

var AdoptedAnnotation = common.DefaultPrefix + "adopted"

func HasAdoptedAnnotation(obj metav1.Object) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	return annotations[AdoptedAnnotation] == common.AnnotationValueTrue
}

func AddAdoptedAnnotation(obj metav1.Object) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}

	if annotations[AdoptedAnnotation] == common.AnnotationValueTrue {
		return false
	}

	annotations[AdoptedAnnotation] = common.AnnotationValueTrue
	obj.SetAnnotations(annotations)
	return true
}

func RemoveAdoptedAnnotation(obj metav1.Object) {
	annotations := obj.GetAnnotations()
	if annotations == nil || annotations[AdoptedAnnotation] != common.AnnotationValueTrue {
		return
	}
	delete(annotations, AdoptedAnnotation)
	obj.SetAnnotations(annotations)
}
