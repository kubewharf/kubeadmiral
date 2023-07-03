/*
Copyright 2015 The Kubernetes Authors.

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

/*
This file is lifted from k8s.io/kubernetes/pkg/controller/controller_utils.go
*/

//nolint:all
package controller

import (
	"context"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
)

// ReplicaSetsByCreationTimestamp sorts a list of ReplicaSet by creation timestamp, using their names as a tie breaker.
type ReplicaSetsByCreationTimestamp []*apps.ReplicaSet

func (o ReplicaSetsByCreationTimestamp) Len() int      { return len(o) }
func (o ReplicaSetsByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// GetSpecifiedOwner returns the object of the owner matches the gvk.
func GetSpecifiedOwnerFromSourceObj(
	ctx context.Context,
	client generic.Client,
	sourceObj client.Object,
	gvk schema.GroupVersionKind,
) (obj *unstructured.Unstructured, found bool, err error) {
	var owner *metav1.OwnerReference
	for _, o := range sourceObj.GetOwnerReferences() {
		if o.APIVersion == gvk.GroupVersion().String() && o.Kind == gvk.Kind {
			owner = &o
			break
		}
	}
	if owner == nil || client == nil {
		return nil, false, nil
	}

	obj = &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	err = client.Get(ctx, obj, sourceObj.GetNamespace(), owner.Name)
	if err != nil {
		return nil, false, err
	}

	return obj, true, nil
}
