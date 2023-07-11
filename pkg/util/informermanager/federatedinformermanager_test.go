package informermanager

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicclient "k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/fake"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/onsi/gomega"
)

func TestFederatedInformerManager(t *testing.T) {
	_ = gomega.NewWithT(t)

	t.Run("clients for existing clusters should be available eventually", func(t *testing.T) {

	})

	t.Run("clients for new clusters should be available eventually", func(t *testing.T) {

	})

	t.Run("listers for existing FTCs and clusters should be available eventually", func(t *testing.T) {

	})

	t.Run("listers for new FTCs should be available eventually", func(t *testing.T) {

	})

	t.Run("listers for new clusteres should be available eventually", func(t *testing.T) {

	})

	t.Run("event handlers for existing FTCs should be registed eventually", func(t *testing.T) {

	})

	t.Run("event handlers for new FTCs should be registered eventually", func(t *testing.T) {

	})

	t.Run("event handlers for new clusters should be registered eventually", func(t *testing.T) {

	})

	t.Run("event handler should receive correct lastApplied and latest FTCs", func(t *testing.T) {

	})

	t.Run("event handler should be registered on FTC update", func(t *testing.T) {

	})

	t.Run("event handler should be unregistered on FTC update", func(t *testing.T) {

	})

	t.Run("event handler should be re-registered on FTC update", func(t *testing.T) {

	})

	t.Run("event handler should remain unchanged on FTC update", func(t *testing.T) {

	})

	t.Run("event handler should be unregistered on FTC deletion", func(t *testing.T) {

	})

	t.Run("event handler should be unregistered on cluster deletion", func(t *testing.T) {

	})
}

// Verifies that clients for existing clusters are eventually available after the FederatedInformerManager is started.
func TestFederatedInformerManagerClientAvailableForExistingClusters(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with 3 clusters.

	cluster1 := getTestCluster("cluster-1")
	cluster2 := getTestCluster("cluster-2")
	cluster3 := getTestCluster("cluster-3")

	defaultClusters := []*fedcorev1a1.FederatedCluster{cluster1, cluster2, cluster3}
	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
	defaultObjects := map[string]*unstructured.Unstructured{}

	manager, _, _ := boostrapFederatedInformerManagerWithFakeClients(defaultClusters, defaultFTCs, defaultObjects)

	// 2. Start the manager

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for FederatedInformerManager cache sync")
	}

	// 3. Verify that clients for the clusters are eventually available

	for _, cluster := range defaultClusters {
		g.Eventually(func(g gomega.Gomega) {
			client, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(client).ToNot(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	}

	// 4. Sanity check: the client for a non-existent cluster should not be available

	client, exists := manager.GetClusterClient("cluster-4")
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(client).To(gomega.BeNil())
}

// Verifies that clients for new clusters created after the FederatedInformerManager is started are eventually
// available.
func TestFederatedInformerManagerClientAvailableForNewCluster(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with no initial clusters.

	defaultClusters := []*fedcorev1a1.FederatedCluster{}
	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
	defaultObjects := map[string]*unstructured.Unstructured{}

	manager, _, fedClient := boostrapFederatedInformerManagerWithFakeClients(defaultClusters, defaultFTCs, defaultObjects)

	// 2. Start the manager

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for FederatedInformerManager cache sync")
	}

	// 3. Sanity check: the client for a non-existent cluster should not be available

	client, exists := manager.GetClusterClient("cluster-1")
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(client).To(gomega.BeNil())

	// 4. Create a new cluster

	cluster1 := getTestCluster("cluster-1")
	fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, cluster1, metav1.CreateOptions{})


	// 5. Verify that clients for the clusters are eventually available

	g.Eventually(func(g gomega.Gomega) {
		client, exists := manager.GetClusterClient(cluster1.Name)
		g.Expect(exists).To(gomega.BeTrue())
		g.Expect(client).ToNot(gomega.BeNil())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

// Verifies that the listers for the SourceType GVR of existing FTCs and member clusters are eventually available after
// the FederatedInformerManager is started.
func TestFederatedInformerManagerListerAvailableForExistingFTCsAndClusters(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with 3 clusters and FTCs for deployments, configmaps and secrets.

	cluster1 := getTestCluster("cluster-1")
	cluster2 := getTestCluster("cluster-2")
	cluster3 := getTestCluster("cluster-3")

	defaultClusters := []*fedcorev1a1.FederatedCluster{cluster1, cluster2, cluster3}
	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := map[string]*unstructured.Unstructured{}

	manager, _, _ := boostrapFederatedInformerManagerWithFakeClients(defaultClusters, defaultFTCs, defaultObjects)

	// 2. Start the manager

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for FederatedInformerManager cache sync")
	}

	// 3. Verify that listers for the clusters and each FTC's SourceType GVR are eventually available.

	for _, cluster := range defaultClusters {
		for _, ftc := range defaultFTCs {
			apiresource := ftc.GetSourceType()
			gvr := schemautil.APIResourceToGVR(&apiresource)

			g.Eventually(func(g gomega.Gomega) {
				lister, informerSynced, exists := manager.GetResourceLister(gvr, cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}
	}

	// 4. Sanity check: the listers for a non-existent cluster and ftc should not be available

	lister, informerSynced, exists := manager.GetResourceLister(common.DaemonSetGVR, defaultClusters[0].Name)
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(lister).To(gomega.BeNil())
	g.Expect(informerSynced).To(gomega.BeNil())

	lister, informerSynced, exists = manager.GetResourceLister(common.DeploymentGVR, "cluster-4")
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(lister).To(gomega.BeNil())
	g.Expect(informerSynced).To(gomega.BeNil())
}

// Verifies that the lister for the SourceType GVR of a new FTC created after the FederatedInformerManager is started
// eventually becomes available.
func TestFederatedInformerManagerListerAvailableForNewFTC(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with 3 clusters.

	cluster1 := getTestCluster("cluster-1")
	cluster2 := getTestCluster("cluster-2")
	cluster3 := getTestCluster("cluster-3")

	defaultClusters := []*fedcorev1a1.FederatedCluster{cluster1, cluster2, cluster3}
	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
	defaultObjects := map[string]*unstructured.Unstructured{}

	manager, _, _ := boostrapFederatedInformerManagerWithFakeClients(defaultClusters, defaultFTCs, defaultObjects)

	// 2. Start the manager

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for FederatedInformerManager cache sync")
	}

	// 3. Sanity check:
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
	objects map[string]*unstructured.Unstructured,
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
	for _, cluster := range clusters {
		fedObjects = append(fedObjects, runtime.Object(cluster))
	}
	for _, ftc := range ftcs {
		fedObjects = append(fedObjects, runtime.Object(ftc))
	}
	fedClient := fake.NewSimpleClientset(fedObjects...)

	factory := fedinformers.NewSharedInformerFactory(fedClient, 0)
	informerManager := NewFederatedInformerManager(
		ClusterClientGetter{
			ConnectionHash: DefaultClusterConnectionHash,
			ClientGetter: func(cluster *fedcorev1a1.FederatedCluster) (dynamicclient.Interface, error) {
				return dynamicfake.NewSimpleDynamicClient(scheme), nil
			},
		},
		factory.Core().V1alpha1().FederatedTypeConfigs(),
		factory.Core().V1alpha1().FederatedClusters(),
	)

	// this is required for the factory to start the underlying ftc informer
	factory.Core().V1alpha1().FederatedTypeConfigs().Informer()

	factory.Start(context.TODO().Done())

	return informerManager, dynamicClient, fedClient
}
