package informermanager

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicclient "k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/fake"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
)

func TestFederatedInformerManagerClientAvailableForExistingClusters(t *testing.T) {

}

func TestFederatedInformerManagerClientAvailableForNewCluster(t *testing.T) {

}

func TestFederatedInformerManagerListerAvailableForExistingFTCsAndClusters(t *testing.T) {

}

func TestFederatedInformerManagerListerAvailableForNewFTC(t *testing.T) {

}

func TestFederatedInformerManagerListerAvailableForNewCluster(t *testing.T) {

}

func TestFederatedInformerManagerEventHandlerRegistrationForExistingFTCsAndClusters(t *testing.T) {

}

func TestFederatedInformerManagerEventHandlerRegistrationForNewFTC(t *testing.T) {

}

func TestFederatedInformerManagerEventHandlerRegistrationOnFTCUpdate(t *testing.T) {

}

func TestFederatedInformerManagerEventHandlerRegistrationOnFTCDelete(t *testing.T) {

}

func TestFederatedInformerManagerEventHandlerRegistrationForNewCluster(t *testing.T) {

}

func TestFederatedInformerManagerEventHandlerRegistrationOnClusterDelete(t *testing.T) {

}

func TestFederatedInformerManagerClusterEventHandlerForExistingClusters(t *testing.T) {

}

func TestFederatedInformerManagerClusterEventHandlerForNewCluster(t *testing.T) {

}

func TestFederatedInformerManagerClusterEventHandlerOnClusterUpdate(t *testing.T) {

}

func TestFederatedInformerManagerClusterEventHandlerOnClusterDelete(t *testing.T) {

}

func boostrapFederatedInformerManagerWithFakeClients(
	clusters []*fedcorev1a1.FederatedCluster,
	ftcs []*fedcorev1a1.FederatedTypeConfig,
	objects []*unstructured.Unstructured,
) (FederatedInformerManager, dynamicclient.Interface, fedclient.Interface) {
	scheme := runtime.NewScheme()

	corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	fedcorev1a1.AddToScheme(scheme)

	dynamicObjects := []runtime.Object{}
	for _, object := range objects {
		dynamicObjects = append(dynamicObjects, runtime.Object(object))
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme, dynamicObjects...)

	fedObjects := []runtime.Object{}
	for _, ftc := range ftcs {
		fedObjects = append(fedObjects, runtime.Object(ftc))
	}
	fedClient := fake.NewSimpleClientset(fedObjects...)

	factory := fedinformers.NewSharedInformerFactory(fedClient, 0)
	informerManager := NewFederatedInformerManager(
		nil,
		factory.Core().V1alpha1().FederatedTypeConfigs(),
		factory.Core().V1alpha1().FederatedClusters(),
	)

	factory.Start(context.TODO().Done())

	return informerManager, dynamicClient, fedClient
}
