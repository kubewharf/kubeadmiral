//go:build exclude
/*
Copyright 2018 The Kubernetes Authors.

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

package version

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type clusterVersionAdapter struct{}

func (*clusterVersionAdapter) TypeName() string {
	return "ClusterPropagatedVersion"
}

func (*clusterVersionAdapter) NewListObject() client.ObjectList {
	return &fedcorev1a1.ClusterPropagatedVersionList{}
}

func (*clusterVersionAdapter) NewObject() client.Object {
	return &fedcorev1a1.ClusterPropagatedVersion{}
}

func (*clusterVersionAdapter) NewVersion(
	qualifiedName common.QualifiedName,
	ownerReference metav1.OwnerReference,
	status *fedcorev1a1.PropagatedVersionStatus,
) client.Object {
	return &fedcorev1a1.ClusterPropagatedVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            qualifiedName.Name,
			OwnerReferences: []metav1.OwnerReference{ownerReference},
		},
		Status: *status,
	}
}

func (*clusterVersionAdapter) GetStatus(obj client.Object) *fedcorev1a1.PropagatedVersionStatus {
	version := obj.(*fedcorev1a1.ClusterPropagatedVersion)
	status := version.Status
	return &status
}

func (*clusterVersionAdapter) SetStatus(obj client.Object, status *fedcorev1a1.PropagatedVersionStatus) {
	version := obj.(*fedcorev1a1.ClusterPropagatedVersion)
	version.Status = *status
}
