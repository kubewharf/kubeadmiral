package fedobjectadapters

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1client "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/typed/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
)

func convertToGeneric[Type *fedcorev1a1.ClusterFederatedObject | *fedcorev1a1.FederatedObject](
	obj Type, err error,
) (*fedcorev1a1.GenericFederatedObject, error) {
	return (*fedcorev1a1.GenericFederatedObject)(obj), err
}

func GetFromLister(
	fedObjectLister fedcorev1a1listers.FederatedObjectLister,
	clusterFedObjectLister fedcorev1a1listers.ClusterFederatedObjectLister,
	namespace, name string,
) (*fedcorev1a1.GenericFederatedObject, error) {
	if namespace == "" {
		return convertToGeneric(clusterFedObjectLister.Get(name))
	} else {
		return convertToGeneric(fedObjectLister.FederatedObjects(namespace).Get(name))
	}
}

func Create(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	obj *fedcorev1a1.GenericFederatedObject,
	opts metav1.CreateOptions,
) (*fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		return convertToGeneric(
			clusterFedObjectClient.ClusterFederatedObjects().Create(ctx, (*fedcorev1a1.ClusterFederatedObject)(obj), opts),
		)
	} else {
		return convertToGeneric(
			fedObjectClient.FederatedObjects(obj.GetNamespace()).Create(ctx, (*fedcorev1a1.FederatedObject)(obj), opts),
		)
	}
}

func Update(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	obj *fedcorev1a1.GenericFederatedObject,
	opts metav1.UpdateOptions,
) (*fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		return convertToGeneric(
			clusterFedObjectClient.ClusterFederatedObjects().Update(ctx, (*fedcorev1a1.ClusterFederatedObject)(obj), opts),
		)
	} else {
		return convertToGeneric(
			fedObjectClient.FederatedObjects(obj.GetNamespace()).Update(ctx, (*fedcorev1a1.FederatedObject)(obj), opts),
		)
	}
}

func UpdateStatus(
	ctx context.Context,
	fedObjectClient fedcorev1a1client.FederatedObjectsGetter,
	clusterFedObjectClient fedcorev1a1client.ClusterFederatedObjectsGetter,
	obj *fedcorev1a1.GenericFederatedObject,
	opts metav1.UpdateOptions,
) (*fedcorev1a1.GenericFederatedObject, error) {
	if obj.GetNamespace() == "" {
		return convertToGeneric(
			clusterFedObjectClient.ClusterFederatedObjects().UpdateStatus(ctx, (*fedcorev1a1.ClusterFederatedObject)(obj), opts),
		)
	} else {
		return convertToGeneric(
			fedObjectClient.FederatedObjects(obj.GetNamespace()).UpdateStatus(ctx, (*fedcorev1a1.FederatedObject)(obj), opts),
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
