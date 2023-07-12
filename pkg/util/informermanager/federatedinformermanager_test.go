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
	t.Run("clients for existing clusters should be available eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}
		manager, _ := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that the clients for each cluster is eventually available

		for _, cluster := range defaultClusters {
			g.Eventually(func(g gomega.Gomega) {
				client, exists := manager.GetClusterClient(cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(client).ToNot(gomega.BeNil())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}

		// 3. Verify that the client for a non-existent cluster is not available

		client, exists := manager.GetClusterClient("cluster-4")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(client).To(gomega.BeNil())
	})

	t.Run("clients for new clusters should be available eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that client for cluster-1 does is not available initially.

		g.Consistently(func(g gomega.Gomega) {
			client, exists := manager.GetClusterClient("cluster-1")
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(client).To(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create a new cluster

		cluster, err := fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, getTestCluster("cluster-1"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that client for new cluster is eventually available

		g.Eventually(func(g gomega.Gomega) {
			client, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(client).ToNot(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("listers for existing FTCs and clusters should be available eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}
		manager, _ := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that listers for existing FTCs and clusters are eventually available

		for _, ftc := range defaultFTCs {
			apiresource := ftc.GetSourceType()
			gvr := schemautil.APIResourceToGVR(&apiresource)

			for _, cluster := range defaultClusters {
				g.Eventually(func(g gomega.Gomega) {
					lister, informerSynced, exists := manager.GetResourceLister(gvr, cluster.Name)

					g.Expect(exists).To(gomega.BeTrue())
					g.Expect(lister).ToNot(gomega.BeNil())
					g.Expect(informerSynced()).To(gomega.BeTrue())
				})
			}
		}

		// 3. Verify that the lister for non-existent FTCs or clusters are not available

		lister, informerSynced, exists := manager.GetResourceLister(common.DaemonSetGVR, "cluster-1")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())

		lister, informerSynced, exists = manager.GetResourceLister(common.DeploymentGVR, "cluster-4")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())

		lister, informerSynced, exists = manager.GetResourceLister(common.DaemonSetGVR, "cluster-4")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())
	})

	t.Run("listers for new FTCs should be available eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		ftc := daemonsetFTC
		apiresource := ftc.GetSourceType()
		gvr := schemautil.APIResourceToGVR(&apiresource)

		// 2. Verify that listers for daemonsets FTCs is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			for _, cluster := range defaultClusters {
				lister, informerSynced, exists := manager.GetResourceLister(common.DeploymentGVR, cluster.Name)
				g.Expect(exists).To(gomega.BeFalse())
				g.Expect(lister).To(gomega.BeNil())
				g.Expect(informerSynced).To(gomega.BeNil())
			}
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create the daemonset FTC

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that the lister for daemonsets is eventually available

		g.Eventually(func(g gomega.Gomega) {
			for _, cluster := range defaultClusters {
				lister, informerSynced, exists := manager.GetResourceLister(gvr, cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("listers for new clusters should be available eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		cluster := getTestCluster("cluster-1")

		// 2. Verify that listers for cluster-1 is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			for _, ftc := range defaultFTCs {
				apiresource := ftc.GetSourceType()
				gvr := schemautil.APIResourceToGVR(&apiresource)

				lister, informerSynced, exists := manager.GetResourceLister(gvr, cluster.Name)
				g.Expect(exists).To(gomega.BeFalse())
				g.Expect(lister).To(gomega.BeNil())
				g.Expect(informerSynced).To(gomega.BeNil())
			}
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create cluster-1

		_, err := fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, cluster, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that the lister for cluster-1 is eventually available

		g.Eventually(func(g gomega.Gomega) {
			for _, ftc := range defaultFTCs {
				apiresource := ftc.GetSourceType()
				gvr := schemautil.APIResourceToGVR(&apiresource)

				lister, informerSynced, exists := manager.GetResourceLister(gvr, cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("event handlers for existing FTCs and clusters should be registed eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		alwaysRegistered := newCountingResourceEventHandler()
		neverRegistered := newCountingResourceEventHandler()

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1, cm1, sc1},
			"cluster-2": {dp1, cm1, sc1},
			"cluster-3": {dp1, cm1, sc1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{
			{
				Predicate: alwaysRegisterPredicate,
				Generator: alwaysRegistered.GenerateEventHandler,
			},
			{
				Predicate: neverRegisterPredicate,
				Generator: neverRegistered.GenerateEventHandler,
			},
		}
		clusterHandlers := []*ClusterEventHandler{}
		manager, _ := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify alwaysRegistered is eventually registered for all existing FTCs and clusters.

		for range defaultClusters {
			alwaysRegistered.ExpectGenerateEvents(deploymentFTC.Name, 1)
			alwaysRegistered.ExpectGenerateEvents(configmapFTC.Name, 1)
			alwaysRegistered.ExpectGenerateEvents(secretFTC.Name, 1)
			alwaysRegistered.ExpectAddEvents(deploymentGVK, 1)
			alwaysRegistered.ExpectAddEvents(configmapGVK, 1)
			alwaysRegistered.ExpectAddEvents(secretGVK, 1)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 4. Verify that newly generated events are received by alwaysRegistered.

		dp1.SetAnnotations(map[string]string{"test": "test"})
		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			_, err := dynamicClient.Resource(common.SecretGVR).
				Namespace("default").
				Create(ctx, getTestSecret("sc-2", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectAddEvents(secretGVK, 1)

			_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectUpdateEvents(deploymentGVK, 1)

			err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectDeleteEvents(configmapGVK, 1)
		}

		alwaysRegistered.AssertEventually(g, time.Second*1)

		// 5. Verify that events for non-existent FTCs are not received

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			_, err := dynamicClient.Resource(common.DaemonSetGVR).
				Namespace("default").
				Create(ctx, getTestDaemonSet("dm-1", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 5. Verify neverRegsitered receives no events

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handlers for new FTCs should be registered eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dm1 := getTestDaemonSet("dm-1", "default")
		dm2 := getTestDaemonSet("dm-2", "default")
		dm3 := getTestDaemonSet("dm-3", "default")
		dm4 := getTestDaemonSet("dm-4", "default")

		alwaysRegistered := newCountingResourceEventHandler()
		neverRegistered := newCountingResourceEventHandler()

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dm1, dm2, dm3, dm4},
			"cluster-2": {dm1, dm2, dm3, dm4},
			"cluster-3": {dm1, dm2, dm3, dm4},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{
			{
				Predicate: alwaysRegisterPredicate,
				Generator: alwaysRegistered.GenerateEventHandler,
			},
			{
				Predicate: neverRegisterPredicate,
				Generator: neverRegistered.GenerateEventHandler,
			},
		}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that alwaysRegistered is not registered initially for daemonset

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 3. Create new FTC for daemonset

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, daemonsetFTC, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that alwaysRegistered is eventually registered for the new Daemonset FTC

		for range defaultClusters {
			alwaysRegistered.ExpectGenerateEvents(daemonsetFTC.Name, 1)
			alwaysRegistered.ExpectAddEvents(daemonsetGVK, 4)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 5. Verify that newly generated events are also received by alwaysRegistered

		dm1.SetAnnotations(map[string]string{"test": "test"})

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			_, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectUpdateEvents(daemonsetGVK, 1)

			g.Expect(exists).To(gomega.BeTrue())
			err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Delete(ctx, dm4.GetName(), metav1.DeleteOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectDeleteEvents(daemonsetGVK, 1)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 6. Verify that events for non-existent FTCs are not received by alwaysRegistered

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			_, err = dynamicClient.Resource(common.SecretGVR).
				Namespace("default").
				Create(ctx, getTestSecret("sc-1", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}
		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 7. Verify that unregisteredResourceEventHandler is not registered

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handlers for new clusters should be registered eventually", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dm1 := getTestDaemonSet("dm-1", "default")
		dm2 := getTestDaemonSet("dm-2", "default")
		dm3 := getTestDaemonSet("dm-3", "default")
		dm4 := getTestDaemonSet("dm-4", "default")

		alwaysRegistered := newCountingResourceEventHandler()
		neverRegistered := newCountingResourceEventHandler()

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{daemonsetFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dm1, dm2, dm3, dm4},
			"cluster-2": {dm1, dm2, dm3, dm4},
			"cluster-3": {dm1, dm2, dm3, dm4},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{
			{
				Predicate: alwaysRegisterPredicate,
				Generator: alwaysRegistered.GenerateEventHandler,
			},
			{
				Predicate: neverRegisterPredicate,
				Generator: neverRegistered.GenerateEventHandler,
			},
		}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that alwaysRegistered is not registered initially since there are no clusters

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 3. Create new clusters

		_, err := fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, getTestCluster("cluster-1"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		_, err = fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, getTestCluster("cluster-2"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		_, err = fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, getTestCluster("cluster-3"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that alwaysRegistered is eventually registered for the new Daemonset FTC

		for range defaultClusters {
			alwaysRegistered.ExpectGenerateEvents(daemonsetFTC.Name, 3)
			alwaysRegistered.ExpectAddEvents(daemonsetGVK, 4)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 5. Verify that newly generated events are also received by alwaysRegistered

		dm1.SetAnnotations(map[string]string{"test": "test"})

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			_, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectUpdateEvents(daemonsetGVK, 1)

			g.Expect(exists).To(gomega.BeTrue())
			_, err = dynamicClient.Resource(common.DaemonSetGVR).
				Namespace("default").
				Create(ctx, getTestDaemonSet("dm-5", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectAddEvents(daemonsetGVK, 1)

			g.Expect(exists).To(gomega.BeTrue())
			err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Delete(ctx, dm4.GetName(), metav1.DeleteOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			alwaysRegistered.ExpectDeleteEvents(daemonsetGVK, 1)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 6. Verify that events for non-existent FTCs are not received by alwaysRegistered

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			_, err = dynamicClient.Resource(common.SecretGVR).
				Namespace("default").
				Create(ctx, getTestSecret("sc-1", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 7. Verify that unregisteredResourceEventHandler is not registered

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handler should receive correct lastApplied and latest FTCs", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		var generation int64 = 1

		// assertionCh is used to achieve 2 things:
		// 1. It is used to pass assertions to the main goroutine.
		// 2. It is used as an implicit lock to ensure FTC events are not squashed by the InformerManager.
		assertionCh := make(chan func())

		ftc := deploymentFTC.DeepCopy()
		ftc.SetGeneration(generation)

		generator := &EventHandlerGenerator{
			Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool {
				if generation == 1 {
					assertionCh <- func() {
						g.Expect(lastApplied).To(gomega.BeNil())
					}
				} else {
					assertionCh <- func() {
						g.Expect(lastApplied.GetGeneration()).To(gomega.BeNumerically("==", generation-1))
						g.Expect(latest.GetGeneration()).To(gomega.BeNumerically("==", generation))
					}
				}

				return true
			},
			Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler { return nil },
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator}
		clusterHandlers := []*ClusterEventHandler{}
		_, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		for range defaultClusters {
			fn := <-assertionCh
			fn()
		}

		// 3. Generate FTC update events

		for i := 0; i < 5; i++ {
			generation++
			ftc.SetGeneration(generation)

			var err error
			ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			for range defaultClusters {
				fn := <-assertionCh
				fn()
			}
		}
	})

	t.Run("event handler should be registered on FTC update", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")

		ftc := deploymentFTC.DeepCopy()
		ftc.SetAnnotations(map[string]string{"predicate": "false", "generator": "true"})

		handler := newCountingResourceEventHandler()
		generator := newAnnotationBasedGenerator(handler)

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1},
			"cluster-2": {dp1},
			"cluster-3": {dp1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that handler is not registered initially.

		handler.AssertConsistently(g, time.Second*2)

		// 3. Update FTC to trigger registration

		ftc.SetAnnotations(map[string]string{"predicate": "true", "generator": "true"})
		ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that handler is registered and additional events are received

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))

		handler.AssertEventually(g, time.Second*2)

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			dp2, err := dynamicClient.Resource(common.DeploymentGVR).
				Namespace("default").
				Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler.ExpectAddEvents(deploymentGVK, 1)

			dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
			dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler.ExpectUpdateEvents(deploymentGVK, 1)

			err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler.ExpectDeleteEvents(deploymentGVK, 1)
		}

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on FTC update", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")

		ftc := deploymentFTC.DeepCopy()
		ftc.SetAnnotations(map[string]string{"predicate": "true", "generator": "true"})

		handler := newCountingResourceEventHandler()
		generator := newAnnotationBasedGenerator(handler)

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1},
			"cluster-2": {dp1},
			"cluster-3": {dp1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that handler is registered initially.

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))
		handler.AssertEventually(g, time.Second*2)

		// 3. Update FTC to trigger unregistration

		ftc.SetAnnotations(map[string]string{"predicate": "true", "generator": "false"})
		ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler is unregistered and new events are no longer received by handler.

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			dp2, err := dynamicClient.Resource(common.DeploymentGVR).
				Namespace("default").
				Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			dp2.SetAnnotations(map[string]string{"test": "test"})
			_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}

		handler.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handler should be re-registered on FTC update", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		ftc := deploymentFTC.DeepCopy()

		handler := newCountingResourceEventHandler()
		generator := &EventHandlerGenerator{
			Predicate: alwaysRegisterPredicate,
			Generator: handler.GenerateEventHandler,
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1},
			"cluster-2": {dp1},
			"cluster-3": {dp1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that handler is registered initially

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))
		handler.AssertEventually(g, time.Second*2)

		// 3. Trigger FTC updates and verify re-registration

		ftc.SetAnnotations(map[string]string{"test": "test"})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))
		handler.AssertEventually(g, time.Second*2)

		dp1.SetAnnotations(map[string]string{"test": "test"})

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler.ExpectUpdateEvents(deploymentGVK, 1)
		}

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should remain unchanged on FTC update", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		ftc := deploymentFTC.DeepCopy()

		handler := newCountingResourceEventHandler()
		generator := &EventHandlerGenerator{
			Predicate: registerOncePredicate,
			Generator: handler.GenerateEventHandler,
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1},
			"cluster-2": {dp1},
			"cluster-3": {dp1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that handler is registered initially

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))
		handler.AssertEventually(g, time.Second*2)

		// 3. Trigger FTC updates and verify no re-registration

		ftc.SetAnnotations(map[string]string{"test": "test"})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.AssertConsistently(g, time.Second*2)

		// 4. Verify events are still received by handler

		dp1.SetAnnotations(map[string]string{"test": "test"})

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler.ExpectUpdateEvents(deploymentGVK, 1)
		}

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on FTC delete", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		handler1 := newCountingResourceEventHandler()
		handler2 := newCountingResourceEventHandler()
		generator1 := &EventHandlerGenerator{
			Predicate: registerOncePredicate,
			Generator: handler1.GenerateEventHandler,
		}
		generator2 := &EventHandlerGenerator{
			Predicate: registerOncePredicate,
			Generator: handler2.GenerateEventHandler,
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1, cm1, sc1},
			"cluster-2": {dp1, cm1, sc1},
			"cluster-3": {dp1, cm1, sc1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator1, generator2}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs

		for range defaultClusters {
			handler1.ExpectGenerateEvents(deploymentFTC.Name, 1)
			handler1.ExpectGenerateEvents(configmapFTC.Name, 1)
			handler1.ExpectGenerateEvents(secretFTC.Name, 1)
			handler1.ExpectAddEvents(deploymentGVK, 1)
			handler1.ExpectAddEvents(configmapGVK, 1)
			handler1.ExpectAddEvents(secretGVK, 1)

			handler2.ExpectGenerateEvents(deploymentFTC.Name, 1)
			handler2.ExpectGenerateEvents(configmapFTC.Name, 1)
			handler2.ExpectGenerateEvents(secretFTC.Name, 1)
			handler2.ExpectAddEvents(deploymentGVK, 1)
			handler2.ExpectAddEvents(configmapGVK, 1)
			handler2.ExpectAddEvents(secretGVK, 1)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Delete the deployment FTC

		err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Delete(ctx, deploymentFTC.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for deployments and no additional events are received

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			dp2, err := dynamicClient.Resource(common.DeploymentGVR).
				Namespace("default").
				Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			dp2.SetAnnotations(map[string]string{"test": "test"})
			_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)

		// 5. Verify that handler1 and handler2 is not unregistered for other FTCs.

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			_, err = dynamicClient.Resource(common.SecretGVR).
				Namespace("default").
				Create(ctx, getTestSecret("sc-2", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler1.ExpectAddEvents(secretGVK, 1)
			handler2.ExpectAddEvents(secretGVK, 1)

			cm1.SetAnnotations(map[string]string{"test": "test"})
			_, err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Update(ctx, cm1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler1.ExpectUpdateEvents(configmapGVK, 1)
			handler2.ExpectUpdateEvents(configmapGVK, 1)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on cluster delete", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		handler1 := newCountingResourceEventHandler()
		handler2 := newCountingResourceEventHandler()
		generator1 := &EventHandlerGenerator{
			Predicate: registerOncePredicate,
			Generator: handler1.GenerateEventHandler,
		}
		generator2 := &EventHandlerGenerator{
			Predicate: registerOncePredicate,
			Generator: handler2.GenerateEventHandler,
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{
			"cluster-1": {dp1, cm1, sc1},
			"cluster-2": {dp1, cm1, sc1},
			"cluster-3": {dp1, cm1, sc1},
		}
		defaultClusters := []*fedcorev1a1.FederatedCluster{
			getTestCluster("cluster-1"),
			getTestCluster("cluster-2"),
			getTestCluster("cluster-3"),
		}
		generators := []*EventHandlerGenerator{generator1, generator2}
		clusterHandlers := []*ClusterEventHandler{}
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs and clusters

		for range defaultClusters {
			handler1.ExpectGenerateEvents(deploymentFTC.Name, 1)
			handler1.ExpectGenerateEvents(configmapFTC.Name, 1)
			handler1.ExpectGenerateEvents(secretFTC.Name, 1)
			handler1.ExpectAddEvents(deploymentGVK, 1)
			handler1.ExpectAddEvents(configmapGVK, 1)
			handler1.ExpectAddEvents(secretGVK, 1)

			handler2.ExpectGenerateEvents(deploymentFTC.Name, 1)
			handler2.ExpectGenerateEvents(configmapFTC.Name, 1)
			handler2.ExpectGenerateEvents(secretFTC.Name, 1)
			handler2.ExpectAddEvents(deploymentGVK, 1)
			handler2.ExpectAddEvents(configmapGVK, 1)
			handler2.ExpectAddEvents(secretGVK, 1)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Delete cluster-1

		// Get client before deletion
		dynamicClient, exists := manager.GetClusterClient("cluster-1")

		err := fedClient.CoreV1alpha1().FederatedClusters().Delete(ctx, "cluster-1", metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for cluster-1 and no additional events are received

		g.Expect(exists).To(gomega.BeTrue())

		dp2, err := dynamicClient.Resource(common.DeploymentGVR).
			Namespace("default").
			Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		dp2.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)

		// 5. Verify that handler1 and handler2 is not unregistered for other clusters.

		for _, cluster := range []string{"cluster-2", "cluster-3"} {
			dynamicClient, exists := manager.GetClusterClient(cluster)
			g.Expect(exists).To(gomega.BeTrue())

			_, err = dynamicClient.Resource(common.SecretGVR).
				Namespace("default").
				Create(ctx, getTestSecret("sc-2", "default"), metav1.CreateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler1.ExpectAddEvents(secretGVK, 1)
			handler2.ExpectAddEvents(secretGVK, 1)

			cm1.SetAnnotations(map[string]string{"test": "test"})
			_, err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Update(ctx, cm1, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())
			handler1.ExpectUpdateEvents(configmapGVK, 1)
			handler2.ExpectUpdateEvents(configmapGVK, 1)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)
	})

	t.Run("ClusterEventHandlers should receive correct old and new clusters", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		var generation int64 = 1
		var callBackCount int64 = 0
		var expectedCallbackCount int64 = 1

		// assertionCh is used to achieve 2 things:
		// 1. It is used to pass assertions to the main goroutine.
		// 2. It is used as an implicit lock to ensure FTC events are not squashed by the InformerManager.
		assertionCh := make(chan func())

		cluster := getTestCluster("cluster-1")
		cluster.SetAnnotations(map[string]string{"predicate": "true"})
		cluster.SetGeneration(1)

		clusterHandler := &ClusterEventHandler{
			Predicate: func(oldCluster *fedcorev1a1.FederatedCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				if generation == 1 {
					assertionCh <- func() {
						g.Expect(oldCluster).To(gomega.BeNil())
						g.Expect(newCluster.GetGeneration()).To(gomega.BeNumerically("==", 1))
					}
				} else {
					assertionCh <- func() {
						g.Expect(oldCluster.GetGeneration()).To(gomega.BeNumerically("==", generation-1))
						g.Expect(newCluster.GetGeneration()).To(gomega.BeNumerically("==", generation))
					}
				}

				return newCluster.GetAnnotations()["predicate"] == "true"
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				callBackCount++
			},
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{clusterHandler}
		_, fedClient := bootstrapFederatedInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, defaultClusters, generators, clusterHandlers)
		
		// 2. Create cluster

		cluster, err := fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, cluster, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		fn := <-assertionCh
		fn()
		g.Expect(callBackCount).To(gomega.Equal(expectedCallbackCount))

		// 3. Generate cluster update events

		for i := 0; i < 5; i++ {
			generation++
			cluster.SetGeneration(generation)

			if i % 2 == 0 {
				cluster.SetAnnotations(map[string]string{"predicate": "false"})
			} else {
				cluster.SetAnnotations(map[string]string{"predicate": "true"})
				expectedCallbackCount++
			}

			var err error
			cluster, err = fedClient.CoreV1alpha1().FederatedClusters().Update(ctx, cluster, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			fn = <-assertionCh
			fn()
			g.Expect(callBackCount).To(gomega.Equal(expectedCallbackCount))
		}
	})
}

func bootstrapFederatedInformerManagerWithFakeClients(
	g *gomega.WithT,
	ctx context.Context,
	ftcs []*fedcorev1a1.FederatedTypeConfig,
	objects map[string][]*unstructured.Unstructured,
	clusters []*fedcorev1a1.FederatedCluster,
	eventHandlerGenerators []*EventHandlerGenerator,
	clusterEventHandlers []*ClusterEventHandler,
) (FederatedInformerManager, fedclient.Interface) {
	scheme := runtime.NewScheme()

	corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	fedcorev1a1.AddToScheme(scheme)

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
				dynamicObjects := []runtime.Object{}

				clusterObjects := objects[cluster.Name]
				if clusterObjects != nil {
					for _, object := range clusterObjects {
						dynamicObjects = append(dynamicObjects, runtime.Object(object))
					}
				}

				return dynamicfake.NewSimpleDynamicClient(scheme, dynamicObjects...), nil
			},
		},
		factory.Core().V1alpha1().FederatedTypeConfigs(),
		factory.Core().V1alpha1().FederatedClusters(),
	)

	for _, generator := range eventHandlerGenerators {
		informerManager.AddEventHandlerGenerator(generator)
	}
	for _, handler := range clusterEventHandlers {
		informerManager.AddClusterEventHandler(handler)
	}

	factory.Start(ctx.Done())
	informerManager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), informerManager.HasSynced) {
		g.Fail("Timed out waiting for FederatedInformerManager cache sync")
	}

	return informerManager, fedClient
}
