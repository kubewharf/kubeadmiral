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

package informermanager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicclient "k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedfake "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/fake"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

//nolint:gocyclo
func TestFederatedInformerManager(t *testing.T) {
	t.Parallel()
	ctx := klog.NewContext(context.Background(), ktesting.NewLogger(t, ktesting.NewConfig(ktesting.Verbosity(2))))

	t.Run("clients for existing clusters should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, _ := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that the clients for each cluster is eventually available

		for _, cluster := range defaultClusters {
			g.Eventually(func(g gomega.Gomega) {
				client, exists := manager.GetClusterDynamicClient(cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(client).ToNot(gomega.BeNil())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}

		// 3. Verify that the client for a non-existent cluster is not available

		client, exists := manager.GetClusterDynamicClient("cluster-4")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(client).To(gomega.BeNil())
	})

	t.Run("clients for new clusters should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that client for cluster-1 does is not available initially.

		g.Consistently(func(g gomega.Gomega) {
			client, exists := manager.GetClusterDynamicClient("cluster-1")
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(client).To(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create a new cluster

		cluster, err := fedClient.CoreV1alpha1().FederatedClusters().Create(
			ctx,
			getTestCluster("cluster-1"),
			metav1.CreateOptions{},
		)
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that client for new cluster is eventually available

		g.Eventually(func(g gomega.Gomega) {
			client, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(client).ToNot(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("listers for existing FTCs and clusters should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, _ := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that listers for existing FTCs and clusters are eventually available

		for _, ftc := range defaultFTCs {
			gvk := ftc.GetSourceTypeGVK()

			for _, cluster := range defaultClusters {
				g.Eventually(func(g gomega.Gomega) {
					lister, informerSynced, exists := manager.GetResourceLister(gvk, cluster.Name)

					g.Expect(exists).To(gomega.BeTrue())
					g.Expect(lister).ToNot(gomega.BeNil())
					g.Expect(informerSynced()).To(gomega.BeTrue())
				}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
			}
		}

		for _, cluster := range defaultClusters {
			g.Eventually(func(g gomega.Gomega) {
				lister, informerSynced, exists := manager.GetPodLister(cluster.Name)

				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

			g.Eventually(func(g gomega.Gomega) {
				lister, informerSynced, exists := manager.GetNodeLister(cluster.Name)

				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}

		// 3. Verify that the lister for non-existent FTCs or clusters are not available

		lister, informerSynced, exists := manager.GetResourceLister(daemonsetGVK, "cluster-1")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())

		lister, informerSynced, exists = manager.GetResourceLister(deploymentGVK, "cluster-4")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())

		lister, informerSynced, exists = manager.GetResourceLister(daemonsetGVK, "cluster-4")
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())
	})

	t.Run("listers for new FTCs should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		ftc := daemonsetFTC
		gvk := ftc.GetSourceTypeGVK()

		// 2. Verify that listers for daemonsets FTCs is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			for _, cluster := range defaultClusters {
				lister, informerSynced, exists := manager.GetResourceLister(deploymentGVK, cluster.Name)
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
				lister, informerSynced, exists := manager.GetResourceLister(gvk, cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("listers for new clusters should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		cluster := getTestCluster("cluster-1")

		// 2. Verify that listers for cluster-1 is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			for _, ftc := range defaultFTCs {
				gvk := ftc.GetSourceTypeGVK()

				lister, informerSynced, exists := manager.GetResourceLister(gvk, cluster.Name)
				g.Expect(exists).To(gomega.BeFalse())
				g.Expect(lister).To(gomega.BeNil())
				g.Expect(informerSynced).To(gomega.BeNil())
			}

			podLister, informerSynced, exists := manager.GetPodLister(cluster.Name)
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(podLister).To(gomega.BeNil())
			g.Expect(informerSynced).To(gomega.BeNil())

			nodeLister, informerSynced, exists := manager.GetNodeLister(cluster.Name)
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(nodeLister).To(gomega.BeNil())
			g.Expect(informerSynced).To(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create cluster-1

		_, err := fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, cluster, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that the lister for cluster-1 is eventually available

		g.Eventually(func(g gomega.Gomega) {
			for _, ftc := range defaultFTCs {
				gvk := ftc.GetSourceTypeGVK()

				lister, informerSynced, exists := manager.GetResourceLister(gvk, cluster.Name)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}

			podLister, informerSynced, exists := manager.GetPodLister(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(podLister).ToNot(gomega.BeNil())
			g.Expect(informerSynced()).To(gomega.BeTrue())

			nodeLister, informerSynced, exists := manager.GetNodeLister(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(nodeLister).ToNot(gomega.BeNil())
			g.Expect(informerSynced()).To(gomega.BeTrue())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("event handlers for existing FTCs and clusters should be registered eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, _ := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify alwaysRegistered is eventually registered for all existing FTCs and clusters.

		for _, cluster := range defaultClusters {
			for _, ftc := range defaultFTCs {
				alwaysRegistered.ExpectGenerateEvents(ftc.Name, 1)
			}

			for _, obj := range defaultObjs[cluster.Name] {
				gvk := mustParseObject(obj)
				alwaysRegistered.ExpectAddEvents(gvk, 1)
			}
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 4. Verify that newly generated events are received by alwaysRegistered.

		dp1.SetAnnotations(map[string]string{"test": "test"})
		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestSecret("sc-2", "default"),
				dynamicClient.Resource(common.SecretGVR),
				alwaysRegistered,
			)

			generateEvents(
				ctx,
				g,
				getTestSecret("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
				alwaysRegistered,
			)

			generateEvents(
				ctx,
				g,
				getTestConfigMap("cm-2", "default"),
				dynamicClient.Resource(common.ConfigMapGVR),
				alwaysRegistered,
			)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 5. Verify that events for non-existent FTCs are not received

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDaemonSet("dm-1", "default"),
				dynamicClient.Resource(common.DaemonSetGVR),
			)
		}

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 5. Verify neverRegistered receives no events

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handlers for new FTCs should be registered eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

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

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDaemonSet("dm-5", "default"),
				dynamicClient.Resource(common.DaemonSetGVR),
				alwaysRegistered,
			)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 6. Verify that events for non-existent FTCs are not received by alwaysRegistered

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestSecret("sc-1", "default"),
				dynamicClient.Resource(common.SecretGVR),
			)
		}

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 7. Verify that unregisteredResourceEventHandler is not registered

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handlers for new clusters should be registered eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that alwaysRegistered is not registered initially since there are no clusters

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 3. Create new clusters

		_, err := fedClient.CoreV1alpha1().FederatedClusters().Create(
			ctx,
			getTestCluster("cluster-1"),
			metav1.CreateOptions{},
		)
		g.Expect(err).ToNot(gomega.HaveOccurred())

		_, err = fedClient.CoreV1alpha1().FederatedClusters().Create(
			ctx,
			getTestCluster("cluster-2"),
			metav1.CreateOptions{},
		)
		g.Expect(err).ToNot(gomega.HaveOccurred())

		_, err = fedClient.CoreV1alpha1().FederatedClusters().Create(
			ctx,
			getTestCluster("cluster-3"),
			metav1.CreateOptions{},
		)
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that alwaysRegistered is eventually registered for the new Daemonset FTC

		for range defaultClusters {
			alwaysRegistered.ExpectGenerateEvents(daemonsetFTC.Name, 3)
			alwaysRegistered.ExpectAddEvents(daemonsetGVK, 4)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 5. Verify that newly generated events are also received by alwaysRegistered

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDaemonSet("dm-5", "default"),
				dynamicClient.Resource(common.DaemonSetGVR),
				alwaysRegistered,
			)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 6. Verify that events for non-existent FTCs are not received by alwaysRegistered

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestSecret("sc-1", "default"),
				dynamicClient.Resource(common.SecretGVR),
			)
		}

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 7. Verify that unregisteredResourceEventHandler is not registered

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handler should receive correct lastApplied and latest FTCs", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

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

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")

		ftc := deploymentFTC.DeepCopy()
		ftc.SetAnnotations(map[string]string{"predicate": predicateFalse, "generator": predicateTrue})

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that handler is not registered initially.

		handler.AssertConsistently(g, time.Second*2)

		// 3. Update FTC to trigger registration

		ftc.SetAnnotations(map[string]string{"predicate": predicateTrue, "generator": predicateTrue})
		ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that handler is registered and additional events are received

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))

		handler.AssertEventually(g, time.Second*2)

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDeployment("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
				handler,
			)
		}

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on FTC update", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")

		ftc := deploymentFTC.DeepCopy()
		ftc.SetAnnotations(map[string]string{"predicate": predicateTrue, "generator": predicateTrue})

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that handler is registered initially.

		handler.ExpectGenerateEvents(ftc.Name, len(defaultClusters))
		handler.ExpectAddEvents(deploymentGVK, len(defaultClusters))
		handler.AssertEventually(g, time.Second*2)

		// 3. Update FTC to trigger unregistration

		ftc.SetAnnotations(map[string]string{"predicate": predicateTrue, "generator": predicateFalse})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler is unregistered and new events are no longer received by handler.

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDeployment("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
			)
		}

		handler.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handler should be re-registered on FTC update", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

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
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDeployment("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
				handler,
			)
		}

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should remain unchanged on FTC update", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

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
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDeployment("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
				handler,
			)
		}

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on FTC delete", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs and clusters

		for _, cluster := range defaultClusters {
			for _, ftc := range defaultFTCs {
				handler1.ExpectGenerateEvents(ftc.Name, 1)
				handler2.ExpectGenerateEvents(ftc.Name, 1)
			}

			for _, obj := range defaultObjs[cluster.Name] {
				gvk := mustParseObject(obj)
				handler1.ExpectAddEvents(gvk, 1)
				handler2.ExpectAddEvents(gvk, 1)
			}
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Delete the deployment FTC

		err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Delete(
			ctx,
			deploymentFTC.GetName(),
			metav1.DeleteOptions{},
		)
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for deployments and no additional events are received

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDeployment("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
			)
		}

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)

		// 5. Verify that handler1 and handler2 is not unregistered for other FTCs.

		for _, cluster := range defaultClusters {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster.Name)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestSecret("sc-2", "default"),
				dynamicClient.Resource(common.SecretGVR),
				handler1,
				handler2,
			)

			generateEvents(
				ctx,
				g,
				getTestConfigMap("cm-2", "default"),
				dynamicClient.Resource(common.ConfigMapGVR),
				handler1,
				handler2,
			)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on cluster delete", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

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

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for FederatedInformerManager cache sync")
		}

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs and clusters

		for _, cluster := range defaultClusters {
			for _, ftc := range defaultFTCs {
				handler1.ExpectGenerateEvents(ftc.Name, 1)
				handler2.ExpectGenerateEvents(ftc.Name, 1)
			}

			for _, obj := range defaultObjs[cluster.Name] {
				gvk := mustParseObject(obj)
				handler1.ExpectAddEvents(gvk, 1)
				handler2.ExpectAddEvents(gvk, 1)
			}
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Delete cluster-1

		// Get client before deletion
		dynamicClient, exists := manager.GetClusterDynamicClient("cluster-1")

		err := fedClient.CoreV1alpha1().FederatedClusters().Delete(ctx, "cluster-1", metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for cluster-1 and no additional events are received

		g.Expect(exists).To(gomega.BeTrue())

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
		)

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)

		// 5. Verify that handler1 and handler2 is not unregistered for other clusters.

		for _, cluster := range []string{"cluster-2", "cluster-3"} {
			dynamicClient, exists := manager.GetClusterDynamicClient(cluster)
			g.Expect(exists).To(gomega.BeTrue())

			generateEvents(
				ctx,
				g,
				getTestDeployment("dp-2", "default"),
				dynamicClient.Resource(common.DeploymentGVR),
				handler1,
				handler2,
			)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)
	})

	t.Run("ClusterEventHandlers should receive correct old and new clusters", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		generation := &atomic.Int64{}
		generation.Store(1)

		expectedCallbackCount := &atomic.Int64{}
		expectedCallbackCount.Store(1)

		callBackCount := &atomic.Int64{}

		// assertionCh is used to achieve 3 things:
		// 1. It is used to pass assertions to the main goroutine.
		// 2. It is used as an implicit lock to ensure FTC events are not squashed by the InformerManager.
		// 3. It is used to ensure that the last event has been processed before the main goroutine sends an update.
		assertionCh := make(chan func())

		cluster := getTestCluster("cluster-1")
		cluster.SetAnnotations(map[string]string{"predicate": predicateTrue})
		cluster.SetGeneration(1)

		clusterHandler := &ClusterEventHandler{
			Predicate: func(oldCluster *fedcorev1a1.FederatedCluster, newCluster *fedcorev1a1.FederatedCluster) bool {
				curGeneration := generation.Load()
				if curGeneration == 1 {
					assertionCh <- func() {
						g.Expect(oldCluster).To(gomega.BeNil())
						g.Expect(newCluster.GetGeneration()).To(gomega.BeNumerically("==", 1))
					}
				} else {
					assertionCh <- func() {
						g.Expect(oldCluster.GetGeneration()).To(gomega.BeNumerically("==", curGeneration-1))
						g.Expect(newCluster.GetGeneration()).To(gomega.BeNumerically("==", curGeneration))
					}
				}

				return newCluster.GetAnnotations()["predicate"] == predicateTrue
			},
			Callback: func(cluster *fedcorev1a1.FederatedCluster) {
				callBackCount.Add(1)
			},
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := map[string][]*unstructured.Unstructured{}
		defaultClusters := []*fedcorev1a1.FederatedCluster{}
		generators := []*EventHandlerGenerator{}
		clusterHandlers := []*ClusterEventHandler{clusterHandler}

		ctx, cancel := context.WithCancel(ctx)
		manager, fedClient := bootstrapFederatedInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			defaultClusters,
			generators,
			clusterHandlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		// 2. Create cluster

		cluster, err := fedClient.CoreV1alpha1().FederatedClusters().Create(ctx, cluster, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		fn := <-assertionCh
		fn()
		g.Expect(callBackCount.Load()).To(gomega.Equal(expectedCallbackCount.Load()))

		// 3. Generate cluster update events

		for i := 0; i < 5; i++ {
			generation.Add(1)
			cluster.SetGeneration(generation.Load())

			if i%2 == 0 {
				cluster.SetAnnotations(map[string]string{"predicate": predicateFalse})
			} else {
				cluster.SetAnnotations(map[string]string{"predicate": predicateTrue})
				expectedCallbackCount.Add(1)
			}

			var err error
			cluster, err = fedClient.CoreV1alpha1().FederatedClusters().Update(ctx, cluster, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			fn = <-assertionCh
			fn()
			g.Expect(callBackCount.Load()).To(gomega.Equal(expectedCallbackCount.Load()))
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

	err := corev1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	err = appsv1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	err = fedcorev1a1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	fedObjects := []runtime.Object{}
	for _, cluster := range clusters {
		fedObjects = append(fedObjects, runtime.Object(cluster.DeepCopy()))
	}
	for _, ftc := range ftcs {
		fedObjects = append(fedObjects, runtime.Object(ftc.DeepCopy()))
	}
	fedClient := fedfake.NewSimpleClientset(fedObjects...)

	factory := fedinformers.NewSharedInformerFactory(fedClient, 0)

	dynamicObjects := map[string][]runtime.Object{}
	for cluster, unsObjects := range objects {
		dynamicObjects[cluster] = make([]runtime.Object, len(unsObjects))
		for i, unsObject := range unsObjects {
			dynamicObjects[cluster][i] = runtime.Object(unsObject.DeepCopy())
		}
	}

	informerManager := NewFederatedInformerManager(
		ClusterClientHelper{
			ConnectionHash: DefaultClusterConnectionHash,
			RestConfigGetter: func(cluster *fedcorev1a1.FederatedCluster) (*rest.Config, error) {
				return nil, nil
			},
		},
		factory.Core().V1alpha1().FederatedTypeConfigs(),
		factory.Core().V1alpha1().FederatedClusters(),
	)
	informerManager.(*federatedInformerManager).dynamicClientGetter = func(
		cluster *fedcorev1a1.FederatedCluster,
		config *rest.Config,
	) (dynamicclient.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(scheme, dynamicObjects[cluster.Name]...), nil
	}
	informerManager.(*federatedInformerManager).kubeClientGetter = func(
		cluster *fedcorev1a1.FederatedCluster,
		config *rest.Config,
	) (kubernetes.Interface, error) {
		return fake.NewSimpleClientset(), nil
	}
	informerManager.(*federatedInformerManager).discoveryClientGetter = func(
		cluster *fedcorev1a1.FederatedCluster,
		config *rest.Config,
	) (discovery.DiscoveryInterface, error) {
		return &fakediscovery.FakeDiscovery{}, nil
	}

	for _, generator := range eventHandlerGenerators {
		err := informerManager.AddEventHandlerGenerator(generator)
		g.Expect(err).ToNot(gomega.HaveOccurred())
	}
	for _, handler := range clusterEventHandlers {
		err := informerManager.AddClusterEventHandlers(handler)
		g.Expect(err).ToNot(gomega.HaveOccurred())
	}

	factory.Start(ctx.Done())
	informerManager.Start(ctx)

	return informerManager, fedClient
}
