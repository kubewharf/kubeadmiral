package fedobjectadapters

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1client "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/typed/core/v1alpha1"
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

func Create(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	obj fedcorev1a1.GenericFederatedObject,
	opts metav1.CreateOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterFederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected ClusterFederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			clusterFedObjectClient.ClusterFederatedObjects().Create(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.FederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected FederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedObjectClient.FederatedObjects(obj.GetNamespace()).Create(ctx, fedObject, opts),
		)
	}
}

func Update(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	obj fedcorev1a1.GenericFederatedObject,
	opts metav1.UpdateOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterFederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected ClusterFederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			clusterFedObjectClient.ClusterFederatedObjects().Update(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.FederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected FederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedObjectClient.FederatedObjects(obj.GetNamespace()).Update(ctx, fedObject, opts),
		)
	}
}

func UpdateStatus(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	obj fedcorev1a1.GenericFederatedObject,
	opts metav1.UpdateOptions,
) (fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		clusterFedObject, ok := obj.(*fedcorev1a1.ClusterFederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected ClusterFederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			clusterFedObjectClient.ClusterFederatedObjects().UpdateStatus(ctx, clusterFedObject, opts),
		)
	} else {
		fedObject, ok := obj.(*fedcorev1a1.FederatedObject)
		if !ok {
			return nil, fmt.Errorf("expected FederatedObject but got %T", obj)
		}
		return ensureNilInterface(
			fedObjectClient.FederatedObjects(obj.GetNamespace()).UpdateStatus(ctx, fedObject, opts),
		)
	}
}

func Delete(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	namespace, name string,
	opts metav1.DeleteOptions,
) error {
	if namespace == "" {
		return clusterFedObjectClient.ClusterFederatedObjects().Delete(ctx, name, opts)
	} else {
		return fedObjectClient.FederatedObjects(namespace).Delete(ctx, name, opts)
	}
}
