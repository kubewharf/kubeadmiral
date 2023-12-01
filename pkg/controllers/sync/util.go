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

package sync

import (
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func skipSync(federatedObject fedcorev1a1.GenericFederatedObject) bool {
	// if workloads have been propagated to member clusters,
	// sync is always not skipped.
	if len(federatedObject.GetStatus().Clusters) > 0 {
		return false
	}
	return federatedObject.GetAnnotations()[common.DryRunAnnotation] == common.AnnotationValueTrue
}
