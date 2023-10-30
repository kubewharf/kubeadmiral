package federatedhpa

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func (r Resource) QualifiedName() common.QualifiedName {
	return common.QualifiedName{
		Namespace: r.namespace,
		Name:      r.name,
	}
}

func ObjectToResource(object metav1.Object) Resource {
	uns := object.(*unstructured.Unstructured)
	return Resource{
		name:      uns.GetName(),
		namespace: uns.GetNamespace(),
		gvk:       uns.GroupVersionKind(),
	}
}

func (f *FederatedHPAController) isHPAType(fo metav1.Object) bool {
	federatedObject := fo.(*fedcorev1a1.FederatedObject)
	_, ok := f.scaleTargetRefMapping[federatedObject.GroupVersionKind()]
	return ok
}

func (f *FederatedHPAController) getTargetResource(key Resource) (*unstructured.Unstructured, error) {
	return nil, nil
}
