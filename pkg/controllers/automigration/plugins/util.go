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

package plugins

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetSpecifiedOwnerFromObj returns the owner of the given object matches the APIResource.
func GetSpecifiedOwnerFromObj(
	ctx context.Context,
	client dynamic.Interface,
	obj client.Object,
	ownerAPIResource metav1.APIResource,
) (ownerObj *unstructured.Unstructured, found bool, err error) {
	gv := schema.GroupVersion{
		Group:   ownerAPIResource.Group,
		Version: ownerAPIResource.Version,
	}
	var owner *metav1.OwnerReference
	ownerReferences := obj.GetOwnerReferences()
	for i, o := range ownerReferences {
		if o.APIVersion == gv.String() && o.Kind == ownerAPIResource.Kind {
			owner = &ownerReferences[i]
			break
		}
	}
	if owner == nil || client == nil {
		return nil, false, nil
	}

	ownerObj, err = client.Resource(schema.GroupVersionResource{
		Group:    ownerAPIResource.Group,
		Version:  ownerAPIResource.Version,
		Resource: ownerAPIResource.Name,
	}).Namespace(obj.GetNamespace()).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return ownerObj, true, nil
}
