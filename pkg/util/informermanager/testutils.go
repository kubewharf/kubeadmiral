package informermanager

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

var (
	daemonsetFTC = &fedcorev1a1.FederatedTypeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "daemonsets",
		},
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "apps",
				Version:    "v1",
				Kind:       "DaemonSet",
				PluralName: "daemonsets",
				Scope:      v1beta1.NamespaceScoped,
			},
		},
	}
	deploymentFTC = &fedcorev1a1.FederatedTypeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "deployments",
		},
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "apps",
				Version:    "v1",
				Kind:       "Deployment",
				PluralName: "deployments",
				Scope:      v1beta1.NamespaceScoped,
			},
		},
	}
	configmapFTC = &fedcorev1a1.FederatedTypeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "configmaps",
		},
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "",
				Version:    "v1",
				Kind:       "ConfigMap",
				PluralName: "configmaps",
				Scope:      v1beta1.NamespaceScoped,
			},
		},
	}
	secretFTC = &fedcorev1a1.FederatedTypeConfig{

		ObjectMeta: metav1.ObjectMeta{
			Name: "secrets",
		},
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "",
				Version:    "v1",
				Kind:       "Secret",
				PluralName: "secrets",
				Scope:      v1beta1.NamespaceScoped,
			},
		},
	}
)

type countingResourceEventHandler struct {
	lock sync.RWMutex

	generateCount int

	addEventCount    int
	updateEventCount int
	deleteEventCount int
}

func (h *countingResourceEventHandler) getAddEventCount() int {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.addEventCount
}

func (h *countingResourceEventHandler) getUpdateEventCount() int {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.updateEventCount
}

func (h *countingResourceEventHandler) getDeleteEventCount() int {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.deleteEventCount
}

func (h *countingResourceEventHandler) getGenerateCount() int {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.generateCount
}

func (h *countingResourceEventHandler) generateEventHandler(_ *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.generateCount++
	return h
}

func (h *countingResourceEventHandler) OnAdd(_ interface{}) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.addEventCount++
}

func (h *countingResourceEventHandler) OnDelete(_ interface{}) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.deleteEventCount++
}

func (h *countingResourceEventHandler) OnUpdate(_ interface{}, _ interface{}) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.updateEventCount++
}

var _ cache.ResourceEventHandler = &countingResourceEventHandler{}

func getDeployment(name, namespace string) *unstructured.Unstructured {
	dp := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	dpMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dp)
	if err != nil {
		panic(err)
	}

	return &unstructured.Unstructured{Object: dpMap}
}

func getConfigMap(name, namespace string) *unstructured.Unstructured {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	cmMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
	if err != nil {
		panic(err)
	}

	return &unstructured.Unstructured{Object: cmMap}
}

func getSecret(name, namespace string) *unstructured.Unstructured {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	secretMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		panic(err)
	}

	return &unstructured.Unstructured{Object: secretMap}
}

func getDaemonSet(name, namespace string) *unstructured.Unstructured {
	dm := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	dmMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dm)
	if err != nil {
		panic(err)
	}

	return &unstructured.Unstructured{Object: dmMap}
}
