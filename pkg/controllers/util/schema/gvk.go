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

package schema

import (
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func IsServiceGvk(gvk schema.GroupVersionKind) bool {
	return gvk.Group == "" && gvk.Kind == common.ServiceKind
}

func IsServiceAccountGvk(gvk schema.GroupVersionKind) bool {
	return gvk.Group == "" && gvk.Kind == common.ServiceAccountKind
}

func IsJobGvk(gvk schema.GroupVersionKind) bool {
	return gvk.Group == batchv1.GroupName && gvk.Kind == common.JobKind
}

func IsPersistentVolumeGvk(gvk schema.GroupVersionKind) bool {
	return gvk.Group == "" && gvk.Kind == common.PersistentVolumeKind
}

func IsPersistentVolumeClaimGvk(gvk schema.GroupVersionKind) bool {
	return gvk.Group == "" && gvk.Kind == common.PersistentVolumeClaimKind
}
