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
	dynamicclient "k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/fake"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func TestInformerManager(t *testing.T) {
	t.Parallel()
	ctx := klog.NewContext(context.Background(), ktesting.NewLogger(t, ktesting.NewConfig(ktesting.Verbosity(2))))

	t.Run("GVK mappings for existing FTCs should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, _, _ := bootstrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators, handlers)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that the GVK mapping for each FTC is eventually available

		for _, ftc := range defaultFTCs {
			gvk := ftc.GetSourceTypeGVK()

			g.Eventually(func(g gomega.Gomega) {
				resourceFTC, exists := manager.GetResourceFTC(gvk)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(resourceFTC).To(gomega.Equal(ftc))
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}

		// 3. Verify that the GVK mapping for a non-existent FTC is not available

		ftc, exists := manager.GetResourceFTC(daemonsetGVK)
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(ftc).To(gomega.BeNil())
	})

	t.Run("GVK mapping for new FTC should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, _, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		ftc := daemonsetFTC
		gvk := ftc.GetSourceTypeGVK()

		// 2. Verify that the GVK mapping for daemonsets is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			resourceFTC, exists := manager.GetResourceFTC(gvk)
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(resourceFTC).To(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create the daemonset FTC.

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that the GVK mapping for daemonsets is eventually available

		g.Eventually(func(g gomega.Gomega) {
			resourceFTC, exists := manager.GetResourceFTC(gvk)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(resourceFTC).To(gomega.Equal(ftc))
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("listers for existing FTCs should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, _, _ := bootstrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators, handlers)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that the listers for each FTC is eventually available

		for _, ftc := range defaultFTCs {
			gvk := ftc.GetSourceTypeGVK()

			g.Eventually(func(g gomega.Gomega) {
				lister, informerSynced, exists := manager.GetResourceLister(gvk)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}

		// 3. Verify that the lister for a non-existent FTC is not available

		lister, informerSynced, exists := manager.GetResourceLister(daemonsetGVK)
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())
	})

	t.Run("listers for new FTC should be available eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, _, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		ftc := daemonsetFTC
		gvk := ftc.GetSourceTypeGVK()

		// 2. Verify that the lister for daemonsets is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			lister, informerSynced, exists := manager.GetResourceLister(gvk)
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(lister).To(gomega.BeNil())
			g.Expect(informerSynced).To(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create the daemonset FTC.

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that the lister for daemonsets is eventually available

		g.Eventually(func(g gomega.Gomega) {
			lister, informerSynced, exists := manager.GetResourceLister(gvk)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(lister).ToNot(gomega.BeNil())
			g.Expect(informerSynced()).To(gomega.BeTrue())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("event handlers for existing FTCs should be registered eventually", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		alwaysRegistered := newCountingResourceEventHandler()
		neverRegistered := newCountingResourceEventHandler()

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := []*unstructured.Unstructured{dp1, cm1, sc1}
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
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, _ := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify alwaysRegistered is eventually registered for all existing FTCs.

		for _, ftc := range defaultFTCs {
			alwaysRegistered.ExpectGenerateEvents(ftc.Name, 1)
		}

		for _, obj := range defaultObjs {
			gvk := mustParseObject(obj)
			alwaysRegistered.ExpectAddEvents(gvk, 1)
		}

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 3. Verify newly generated events are received by alwaysRegistered

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
			getTestDeployment("dp-2", "default"),
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

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 4. Verify that events for non-existent FTCs are not received

		generateEvents(
			ctx,
			g,
			getTestDaemonSet("dm-1", "default"),
			dynamicClient.Resource(common.DaemonSetGVR),
		)

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
		defaultObjs := []*unstructured.Unstructured{dm1, dm2, dm3, dm4}
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
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that alwaysRegistered is not registered initially for daemonset

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 3. Create new FTC for daemonset

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, daemonsetFTC, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that alwaysRegistered is eventually registered for the new Daemonset FTC

		alwaysRegistered.ExpectGenerateEvents(daemonsetFTC.Name, 1)
		alwaysRegistered.ExpectAddEvents(daemonsetGVK, 4)
		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 5. Verify that newly generated events are also received by alwaysRegistered

		generateEvents(
			ctx,
			g,
			getTestDaemonSet("dm-5", "default"),
			dynamicClient.Resource(common.DaemonSetGVR),
			alwaysRegistered,
		)

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 6. Verify that events for non-existent FTCs are not received by alwaysRegistered

		generateEvents(
			ctx,
			g,
			getTestSecret("sc-1", "default"),
			dynamicClient.Resource(common.SecretGVR),
		)

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 7. Verify that unregisteredResourceEventHandler is not registered

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("EventHandlerGenerators should receive correct lastApplied and latest FTCs", func(t *testing.T) {
		t.Parallel()
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		generation := &atomic.Int64{}
		generation.Store(1)

		// assertionCh is used to achieve 3 things:
		// 1. It is used to pass assertions to the main goroutine.
		// 2. It is used as an implicit lock to ensure FTC events are not squashed by the InformerManager.
		// 3. It is used to ensure that the last event has been processed before the main goroutine sends an update.
		assertionCh := make(chan func())

		ftc := deploymentFTC.DeepCopy()
		ftc.SetGeneration(generation.Load())

		generator := &EventHandlerGenerator{
			Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool {
				curGeneration := generation.Load()
				if curGeneration == 1 {
					assertionCh <- func() {
						g.Expect(lastApplied).To(gomega.BeNil())
						g.Expect(latest.GetGeneration()).To(gomega.BeNumerically("==", 1))
					}
				} else {
					assertionCh <- func() {
						g.Expect(lastApplied.GetGeneration()).To(gomega.BeNumerically("==", curGeneration-1))
						g.Expect(latest.GetGeneration()).To(gomega.BeNumerically("==", curGeneration))
					}
				}

				return true
			},
			Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler { return nil },
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{generator}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, _, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		fn := <-assertionCh
		fn()

		// 3. Generate FTC update events

		for i := 0; i < 5; i++ {
			generation.Add(1)
			ftc.SetGeneration(generation.Load())

			var err error
			ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			fn = <-assertionCh
			fn()
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
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that handler is not registered initially.

		handler.AssertConsistently(g, time.Second*2)

		// 3. Update FTC to trigger registration

		ftc.SetAnnotations(map[string]string{"predicate": predicateTrue, "generator": predicateTrue})
		ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that handler is registered and additional events are received

		handler.ExpectGenerateEvents(ftc.Name, 1)
		handler.ExpectAddEvents(deploymentGVK, 1)

		handler.AssertEventually(g, time.Second*2)

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
			handler,
		)

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
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that handler is registered initially.

		handler.ExpectGenerateEvents(ftc.Name, 1)
		handler.ExpectAddEvents(deploymentGVK, 1)
		handler.AssertEventually(g, time.Second*2)

		// 3. Update FTC to trigger unregistration

		ftc.SetAnnotations(map[string]string{"predicate": predicateTrue, "generator": predicateFalse})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler is unregistered and new events are no longer received by handler.

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
		)

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
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		// 2. Verify that handler is registered initially

		handler.ExpectGenerateEvents(ftc.Name, 1)
		handler.ExpectAddEvents(deploymentGVK, 1)
		handler.AssertEventually(g, time.Second*2)

		// 3. Trigger FTC updates and verify re-registration

		ftc.SetAnnotations(map[string]string{"test": "test"})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.ExpectGenerateEvents(ftc.Name, 1)
		handler.ExpectAddEvents(deploymentGVK, 1)
		handler.AssertEventually(g, time.Second*2)

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
			handler,
		)

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unchanged on FTC update", func(t *testing.T) {
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
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that handler is registered initially

		handler.ExpectGenerateEvents(ftc.Name, 1)
		handler.ExpectAddEvents(deploymentGVK, 1)
		handler.AssertEventually(g, time.Second*2)

		// 3. Trigger FTC updates and verify no re-registration

		ftc.SetAnnotations(map[string]string{"test": "test"})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.AssertConsistently(g, time.Second*2)

		// 4. Verify events are still received by handler

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
			handler,
		)

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
		defaultObjs := []*unstructured.Unstructured{dp1, cm1, sc1}
		generators := []*EventHandlerGenerator{generator1, generator2}
		handlers := []FTCUpdateHandler{}

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs

		for _, ftc := range defaultFTCs {
			handler1.ExpectGenerateEvents(ftc.Name, 1)
			handler2.ExpectGenerateEvents(ftc.Name, 1)
		}

		for _, obj := range defaultObjs {
			gvk := mustParseObject(obj)
			handler1.ExpectAddEvents(gvk, 1)
			handler2.ExpectAddEvents(gvk, 1)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Delete the deployment FTC

		err := fedClient.CoreV1alpha1().
			FederatedTypeConfigs().
			Delete(ctx, deploymentFTC.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for deployments and no additional events are received

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
		)

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)

		// 5. Verify that handler1 and handler2 is not unregistered for other FTCs.

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
			getTestSecret("cm-2", "default"),
			dynamicClient.Resource(common.ConfigMapGVR),
			handler1,
			handler2,
		)

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)
	})

	t.Run("event handlers should be unregistered on manager shutdown", func(t *testing.T) {
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
		defaultObjs := []*unstructured.Unstructured{dp1, cm1, sc1}
		generators := []*EventHandlerGenerator{generator1, generator2}
		handlers := []FTCUpdateHandler{}

		managerCtx, managerCancel := context.WithCancel(ctx)

		ctx, cancel := context.WithCancel(ctx)
		manager, dynamicClient, _ := bootstrapInformerManagerWithFakeClients(
			g,
			managerCtx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		ctxWithTimeout, timeoutCancel := context.WithTimeout(ctx, time.Second)
		defer timeoutCancel()
		if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
			g.Fail("Timed out waiting for InformerManager cache sync")
		}

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs

		for _, ftc := range defaultFTCs {
			handler1.ExpectGenerateEvents(ftc.Name, 1)
			handler2.ExpectGenerateEvents(ftc.Name, 1)
		}

		for _, obj := range defaultObjs {
			gvk := mustParseObject(obj)
			handler1.ExpectAddEvents(gvk, 1)
			handler2.ExpectAddEvents(gvk, 1)
		}

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Shutdown the manager

		managerCancel()
		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for all FTCs and no more events are received

		generateEvents(
			ctx,
			g,
			getTestDeployment("dp-2", "default"),
			dynamicClient.Resource(common.DeploymentGVR),
		)

		generateEvents(
			ctx,
			g,
			getTestDeployment("sc-2", "default"),
			dynamicClient.Resource(common.SecretGVR),
		)

		generateEvents(
			ctx,
			g,
			getTestDeployment("cm-2", "default"),
			dynamicClient.Resource(common.ConfigMapGVR),
		)

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)
	})

	t.Run("ftc update event handlers should be called on ftc events", func(t *testing.T) {
		g := gomega.NewWithT(t)

		// 1. Bootstrap environment

		generation := &atomic.Int64{}
		generation.Store(1)

		// assertionCh is used to achieve 3 things:
		// 1. It is used to pass assertions to the main goroutine.
		// 2. It is used as an implicit lock to ensure FTC events are not squashed by the InformerManager.
		// 3. It is used to ensure that the last event has been processed before the main goroutine sends an update.
		assertionCh := make(chan func())

		ftc := deploymentFTC.DeepCopy()
		ftc.SetGeneration(generation.Load())

		handler := func(lastObserved, latest *fedcorev1a1.FederatedTypeConfig) {
			curGeneration := generation.Load()
			if curGeneration == 1 {
				assertionCh <- func() {
					g.Expect(lastObserved).To(gomega.BeNil())
					g.Expect(latest.GetGeneration()).To(gomega.BeNumerically("==", 1))
				}
			} else {
				assertionCh <- func() {
					g.Expect(lastObserved.GetGeneration()).To(gomega.BeNumerically("==", curGeneration-1))
					g.Expect(latest.GetGeneration()).To(gomega.BeNumerically("==", curGeneration))
				}
			}
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		handlers := []FTCUpdateHandler{handler}

		ctx, cancel := context.WithCancel(ctx)
		manager, _, fedClient := bootstrapInformerManagerWithFakeClients(
			g,
			ctx,
			defaultFTCs,
			defaultObjs,
			generators,
			handlers,
		)
		defer func() {
			cancel()
			_ = wait.PollInfinite(time.Millisecond, func() (done bool, err error) { return manager.IsShutdown(), nil })
		}()

		fn := <-assertionCh
		fn()

		// 3. Generate FTC update events

		for i := 0; i < 5; i++ {
			generation.Add(1)
			ftc.SetGeneration(generation.Load())

			var err error
			ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			fn = <-assertionCh
			fn()
		}
	})
}

func bootstrapInformerManagerWithFakeClients(
	g *gomega.WithT,
	ctx context.Context,
	ftcs []*fedcorev1a1.FederatedTypeConfig,
	objects []*unstructured.Unstructured,
	eventHandlerGenerators []*EventHandlerGenerator,
	ftcUpdateHandlers []FTCUpdateHandler,
) (InformerManager, dynamicclient.Interface, fedclient.Interface) {
	scheme := runtime.NewScheme()

	err := corev1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	err = appsv1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	err = fedcorev1a1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	dynamicObjects := []runtime.Object{}
	for _, object := range objects {
		dynamicObjects = append(dynamicObjects, runtime.Object(object.DeepCopy()))
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme, dynamicObjects...)

	fedObjects := []runtime.Object{}
	for _, ftc := range ftcs {
		fedObjects = append(fedObjects, runtime.Object(ftc.DeepCopy()))
	}
	fedClient := fake.NewSimpleClientset(fedObjects...)

	factory := fedinformers.NewSharedInformerFactory(fedClient, 0)
	informerManager := NewInformerManager(dynamicClient, factory.Core().V1alpha1().FederatedTypeConfigs(), nil)

	for _, generator := range eventHandlerGenerators {
		err := informerManager.AddEventHandlerGenerator(generator)
		g.Expect(err).ToNot(gomega.HaveOccurred())
	}

	for _, handler := range ftcUpdateHandlers {
		err := informerManager.AddFTCUpdateHandler(handler)
		g.Expect(err).ToNot(gomega.HaveOccurred())
	}

	factory.Start(ctx.Done())
	informerManager.Start(ctx)

	return informerManager, dynamicClient, fedClient
}
