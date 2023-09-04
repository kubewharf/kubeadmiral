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

package cascadingdeletion

import (
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

// AnnotationCascadingDelete on a federated cluster means that
// resources managed by KubeAdmiral in the cluster should be cleaned
// up before deletion can occur.
var AnnotationCascadingDelete = common.DefaultPrefix + "cascading-delete"

func IsCascadingDeleteEnabled(cluster *fedcorev1a1.FederatedCluster) bool {
	annotations := cluster.GetAnnotations()
	if annotations == nil {
		return false
	}

	if _, exists := annotations[AnnotationCascadingDelete]; exists {
		return true
	}

	return false
}
