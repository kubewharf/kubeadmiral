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

package collectedstatusadapters

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1client "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/typed/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
)

func ensureNilInterface(
	obj fedcorev1a1.GenericCollectedStatusObject, err error,
) (fedcorev1a1.GenericCollectedStatusObject, error) {
	if err != nil {
		// Returning a non-nil interface value with nil concrete type can be confusing.
		// We make sure the returned interface value is nil if there's an error.
		return nil, err
	}
	return obj, nil
}

func GetFromLister(
	collectedStatusLister fedcorev1a1listers.CollectedStatusLister,
	clusterCollectedStatusLister fedcorev1a1listers.ClusterCollectedStatusLister,
	namespace, name string,
) (fedcorev1a1.GenericCollectedStatusObject, error) {
	if namespace == "" {
		return ensureNilInterface(clusterCollectedStatusLister.Get(name))
	} else {
		return ensureNilInterface(collectedStatusLister.CollectedStatuses(namespace).Get(name))
	}
}

func Create(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	obj fedcorev1a1.GenericCollectedStatusObject,
	opts metav1.CreateOptions,
) (fedcorev1a1.GenericCollectedStatusObject, error) {
	if obj.GetNamespace() == "" {
		clusterCollectedStatus, ok := obj.(*fedcorev1a1.ClusterCollectedStatus)
		if !ok {
			return nil, fmt.Errorf("expected ClusterCollectedStatus but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.ClusterCollectedStatuses().Create(ctx, clusterCollectedStatus, opts),
		)
	} else {
		collectedStatus, ok := obj.(*fedcorev1a1.CollectedStatus)
		if !ok {
			return nil, fmt.Errorf("expected CollectedStatus but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.CollectedStatuses(obj.GetNamespace()).Create(ctx, collectedStatus, opts),
		)
	}
}

func Update(
	ctx context.Context,
	fedv1a1Client fedcorev1a1client.CoreV1alpha1Interface,
	obj fedcorev1a1.GenericCollectedStatusObject,
	opts metav1.UpdateOptions,
) (fedcorev1a1.GenericCollectedStatusObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterCollectedStatus)
		if !ok {
			return nil, fmt.Errorf("expected ClusterCollectedStatus but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.ClusterCollectedStatuses().Update(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.CollectedStatus)
		if !ok {
			return nil, fmt.Errorf("expected CollectedStatus but got %T", obj)
		}
		return ensureNilInterface(
			fedv1a1Client.CollectedStatuses(obj.GetNamespace()).Update(ctx, fedObject, opts),
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
		return fedv1a1Client.ClusterCollectedStatuses().Delete(ctx, name, opts)
	} else {
		return fedv1a1Client.CollectedStatuses(namespace).Delete(ctx, name, opts)
	}
}
