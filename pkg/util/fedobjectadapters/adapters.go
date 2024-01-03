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

package fedobjectadapters

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1client "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/typed/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
)

func ensureNilInterface(
	obj fedcorev1a1.GenericFederatedObject, err error,
) (fedcorev1a1.GenericFederatedObject, error) {
	if err != nil {
		// Returning a non-nil interface value with nil concrete type can be confusing.
		// We make sure the returned interface value is nil if there's an error.
		return nil, err
	}
	return obj, nil
}

func GetFromLister(
	fedObjectLister fedcorev1a1listers.FederatedObjectLister,
	clusterFedObjectLister fedcorev1a1listers.ClusterFederatedObjectLister,
	namespace, name string,
) (fedcorev1a1.GenericFederatedObject, error) {
	if namespace == "" {
		return ensureNilInterface(clusterFedObjectLister.Get(name))
	} else {
		return ensureNilInterface(fedObjectLister.FederatedObjects(namespace).Get(name))
	}
}

func Get(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	namespace, name string,
	opts metav1.GetOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if namespace == "" {
		return ensureNilInterface(
			fedv1a1Client.ClusterFederatedObjects().Get(ctx, name, opts),
		)
	} else {
		return ensureNilInterface(
			fedv1a1Client.FederatedObjects(namespace).Get(ctx, name, opts),
		)
	}
}

func Create(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	obj fedcorev1a1.GenericFederatedObject,
	opts metav1.CreateOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterFederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected ClusterFederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.ClusterFederatedObjects().Create(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.FederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected FederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.FederatedObjects(obj.GetNamespace()).Create(ctx, fedObject, opts),
		)
	}
}

func Update(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	obj fedcorev1a1.GenericFederatedObject,
	opts metav1.UpdateOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterFederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected ClusterFederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.ClusterFederatedObjects().Update(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.FederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected FederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.FederatedObjects(obj.GetNamespace()).Update(ctx, fedObject, opts),
		)
	}
}

func UpdateStatus(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	obj fedcorev1a1.GenericFederatedObject,
	opts metav1.UpdateOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterFederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected ClusterFederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.ClusterFederatedObjects().UpdateStatus(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.FederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected FederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.FederatedObjects(obj.GetNamespace()).UpdateStatus(ctx, fedObject, opts),
		)
	}
}

func Delete(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	namespace, name string,
	opts metav1.DeleteOptions,
) error {
	if namespace == "" {
		return fedv1a1Client.ClusterFederatedObjects().Delete(ctx, name, opts)
	} else {
		return fedv1a1Client.FederatedObjects(namespace).Delete(ctx, name, opts)
	}
}

func ListAllFedObjsForFTC(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObjectLister fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectLister fedcorev1a1informers.ClusterFederatedObjectInformer,
) ([]fedcorev1a1.GenericFederatedObject, error) {
	allObjects := []fedcorev1a1.GenericFederatedObject{}
	labelsSet := labels.Set{ftc.GetSourceTypeGVK().GroupVersion().String(): ftc.GetSourceTypeGVK().Kind}

	if ftc.GetSourceType().Namespaced {
		fedObjects, err := fedObjectLister.Lister().List(labels.SelectorFromSet(labelsSet))
		if err != nil {
			return nil, err
		}
		for _, obj := range fedObjects {
			allObjects = append(allObjects, obj)
		}
	} else {
		clusterFedObjects, err := clusterFedObjectLister.Lister().List(labels.SelectorFromSet(labelsSet))
		if err != nil {
			return nil, err
		}
		for _, obj := range clusterFedObjects {
			allObjects = append(allObjects, obj)
		}
	}

	return allObjects, nil
}
