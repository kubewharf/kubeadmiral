package informermanager

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/gomega"
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
)

func TestInformerManager(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Run("listers for existing FTCs should be available eventually", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		manager, _, _ := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that the listers for each FTC is eventually available

		for _, ftc := range defaultFTCs {
			apiresource := ftc.GetSourceType()
			gvr := schemautil.APIResourceToGVR(&apiresource)

			g.Eventually(func(g gomega.Gomega) {
				lister, informerSynced, exists := manager.GetResourceLister(gvr)
				g.Expect(exists).To(gomega.BeTrue())
				g.Expect(lister).ToNot(gomega.BeNil())
				g.Expect(informerSynced()).To(gomega.BeTrue())
			}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
		}

		// 3. Verify that the lister for a non-existent FTC is not available

		lister, informerSynced, exists := manager.GetResourceLister(common.DaemonSetGVR)
		g.Expect(exists).To(gomega.BeFalse())
		g.Expect(lister).To(gomega.BeNil())
		g.Expect(informerSynced).To(gomega.BeNil())
	})

	t.Run("listers for new FTC should be available eventually", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{}
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{}
		manager, _, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		ftc := daemonsetFTC
		apiresource := ftc.GetSourceType()
		gvr := schemautil.APIResourceToGVR(&apiresource)

		// 2. Verify that the lister for daemonsets is not available at the start

		g.Consistently(func(g gomega.Gomega) {
			lister, informerSynced, exists := manager.GetResourceLister(gvr)
			g.Expect(exists).To(gomega.BeFalse())
			g.Expect(lister).To(gomega.BeNil())
			g.Expect(informerSynced).To(gomega.BeNil())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

		// 3. Create the daemonset FTC.

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify the the lister for daemonsets is eventually available

		g.Eventually(func(g gomega.Gomega) {
			lister, informerSynced, exists := manager.GetResourceLister(gvr)
			g.Expect(exists).To(gomega.BeTrue())
			g.Expect(lister).ToNot(gomega.BeNil())
			g.Expect(informerSynced()).To(gomega.BeTrue())
		}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
	})

	t.Run("event handlers for existing FTCs should be registered eventually", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		alwaysRegistered := &countingResourceEventHandler{}
		neverRegistered := &countingResourceEventHandler{}

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

		_, dynamicClient, _ := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify alwaysRegistered is eventually registered for all existing FTCs.

		alwaysRegistered.ExpectGenerateEvents(3)
		alwaysRegistered.ExpectAddEvents(3)
		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 3. Verify newly generated events are received by alwaysRegistered

		_, err := dynamicClient.Resource(common.SecretGVR).
			Namespace("default").
			Create(ctx, getTestSecret("sc-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		alwaysRegistered.ExpectAddEvents(1)

		dp1.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		alwaysRegistered.ExpectUpdateEvents(1)

		err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		alwaysRegistered.ExpectDeleteEvents(1)

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 4. Verify that events for non-existent FTCs are not received

		_, err = dynamicClient.Resource(common.DaemonSetGVR).
			Namespace("default").
			Create(ctx, getTestDaemonSet("dm-1", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 5. Verify neverRegsitered receives no events

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handlers for new FTCs should be registered eventually", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dm1 := getTestDaemonSet("dm-1", "default")
		dm2 := getTestDaemonSet("dm-2", "default")
		dm3 := getTestDaemonSet("dm-3", "default")
		dm4 := getTestDaemonSet("dm-4", "default")

		alwaysRegistered := &countingResourceEventHandler{}
		neverRegistered := &countingResourceEventHandler{}

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

		_, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Create new FTC for daemonset

		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, daemonsetFTC, metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 3. Verify that alwaysRegistered is eventually registered for the new Daemonset FTC

		alwaysRegistered.ExpectGenerateEvents(1)
		alwaysRegistered.ExpectAddEvents(4)
		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 4. Verify that newly generated events are also received by alwaysRegistered

		dm1.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		alwaysRegistered.ExpectUpdateEvents(1)

		err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Delete(ctx, dm4.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		alwaysRegistered.ExpectDeleteEvents(1)

		alwaysRegistered.AssertEventually(g, time.Second*2)

		// 4. Verify that events for non-existent FTCs are not received by alwaysRegistered

		_, err = dynamicClient.Resource(common.SecretGVR).
			Namespace("default").
			Create(ctx, getTestSecret("sc-1", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		alwaysRegistered.AssertConsistently(g, time.Second*2)

		// 5. Verify that unregisteredResourceEventHandler is not registered

		neverRegistered.AssertConsistently(g, time.Second*2)
	})

	t.Run("EventHandlerGenerators should receive correct lastApplied and latest FTCs", func(t *testing.T) {
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
		defaultObjs := []*unstructured.Unstructured{}
		generators := []*EventHandlerGenerator{generator}
		_, _, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		fn := <-assertionCh
		fn()

		// 3. Generate FTC update events
		for i := 0; i < 5; i++ {
			generation++
			ftc.SetGeneration(generation)

			var err error
			ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
			g.Expect(err).ToNot(gomega.HaveOccurred())

			fn = <-assertionCh
			fn()
		}
	})

	t.Run("event handler should be registered on FTC update", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")

		ftc := deploymentFTC.DeepCopy()
		ftc.SetAnnotations(map[string]string{"predicate": "false", "generator": "true"})

		handler := &countingResourceEventHandler{}
		generator := newAnnotationBasedGenerator(handler)

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}

		_, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that handler is not registered initially.

		handler.AssertConsistently(g, time.Second*2)

		// 3. Update FTC to trigger registration

		ftc.SetAnnotations(map[string]string{"predicate": "true", "generator": "true"})
		ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 4. Verify that handler is registered and additional events are received

		handler.ExpectGenerateEvents(1)
		handler.ExpectAddEvents(1)

		handler.AssertEventually(g, time.Second*2)

		dp2, err := dynamicClient.Resource(common.DeploymentGVR).
			Namespace("default").
			Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		handler.ExpectAddEvents(1)

		dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
		dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		handler.ExpectUpdateEvents(1)

		err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		handler.ExpectDeleteEvents(1)

		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregistered on FTC update", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")

		ftc := deploymentFTC.DeepCopy()
		ftc.SetAnnotations(map[string]string{"predicate": "true", "generator": "true"})

		handler := &countingResourceEventHandler{}
		generator := newAnnotationBasedGenerator(handler)

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}

		_, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that handler is registered initially.

		handler.ExpectGenerateEvents(1)
		handler.ExpectAddEvents(1)
		handler.AssertEventually(g, time.Second*2)

		// 3. Update FTC to trigger unregistration

		ftc.SetAnnotations(map[string]string{"predicate": "true", "generator": "false"})
		ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler is unregistered and new events are no longer received by handler.

		dp2, err := dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		dp2.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.AssertConsistently(g, time.Second*2)
	})

	t.Run("event handler should be re-registered on FTC update", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		ftc := deploymentFTC.DeepCopy()

		handler := &countingResourceEventHandler{}
		generator := &EventHandlerGenerator{
			Predicate: alwaysRegisterPredicate,
			Generator: handler.GenerateEventHandler,
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}

		_, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that handler is registered initially
		
		handler.ExpectGenerateEvents(1)
		handler.ExpectAddEvents(1)
		handler.AssertEventually(g, time.Second*2)

		// 3. Trigger FTC updates and verify re-registration

		ftc.SetAnnotations(map[string]string{"test": "test"})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.ExpectGenerateEvents(1)
		handler.ExpectAddEvents(1)
		handler.AssertEventually(g, time.Second*2)

		dp1.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.ExpectUpdateEvents(1)
		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unchanged on FTC update", func(t * testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		ftc := deploymentFTC.DeepCopy()

		handler := &countingResourceEventHandler{}
		generator := &EventHandlerGenerator{
			Predicate: registerOncePredicate,
			Generator: handler.GenerateEventHandler,
		}

		defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
		defaultObjs := []*unstructured.Unstructured{dp1}
		generators := []*EventHandlerGenerator{generator}

		_, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that handler is registered initially
		
		handler.ExpectGenerateEvents(1)
		handler.ExpectAddEvents(1)
		handler.AssertEventually(g, time.Second*2)

		// 3. Trigger FTC updates and verify no re-registration

		ftc.SetAnnotations(map[string]string{"test": "test"})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.AssertConsistently(g, time.Second*2)

		// 4. Verify events are still received by handler

		dp1.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler.ExpectUpdateEvents(1)
		handler.AssertEventually(g, time.Second*2)
	})

	t.Run("event handler should be unregisterd on FTC delete", func(t *testing.T){
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		handler1 := &countingResourceEventHandler{}
		handler2 := &countingResourceEventHandler{}
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

		_, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(g, ctx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs
		
		handler1.ExpectGenerateEvents(3)
		handler1.ExpectAddEvents(3)
		handler1.AssertEventually(g, time.Second*2)

		handler2.ExpectGenerateEvents(3)
		handler2.ExpectAddEvents(3)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Delete the deployment FTC

		err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Delete(ctx, deploymentFTC.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for deployments and no additional events are received

		dp2, err := dynamicClient.Resource(common.DeploymentGVR). Namespace("default").Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		dp2.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)

		// 5. Verify that handler1 and handler2 is not unregistered for other FTCs.

		_, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, getTestSecret("sc-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		handler1.ExpectAddEvents(1)
		handler2.ExpectAddEvents(1)

		cm1.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Update(ctx, cm1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())
		handler1.ExpectUpdateEvents(1)
		handler2.ExpectUpdateEvents(1)

		handler1.AssertEventually(g, time.Second*2)
		handler2.AssertEventually(g, time.Second*2)
	})

	t.Run("event handlers should be unregistered on manager shutdown", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 1. Bootstrap environment

		dp1 := getTestDeployment("dp-1", "default")
		cm1 := getTestConfigMap("cm-1", "default")
		sc1 := getTestSecret("sc-1", "default")

		handler1 := &countingResourceEventHandler{}
		handler2 := &countingResourceEventHandler{}
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

		managerCtx, managerCancel := context.WithCancel(ctx)
		_, dynamicClient, _ := boostrapInformerManagerWithFakeClients(g, managerCtx, defaultFTCs, defaultObjs, generators)

		// 2. Verify that handler1 and handler2 is registered initially for all FTCs
		
		handler1.ExpectGenerateEvents(3)
		handler1.ExpectAddEvents(3)
		handler1.AssertEventually(g, time.Second*2)

		handler2.ExpectGenerateEvents(3)
		handler2.ExpectAddEvents(3)
		handler2.AssertEventually(g, time.Second*2)

		// 3. Shutdown the manager

		managerCancel()
		<-time.After(time.Second)

		// 4. Verify that handler1 and handler2 is unregistered for all FTCs and no more events are received

		_, err := dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, getTestDeployment("dp-2", "default"), metav1.CreateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		sc1.SetAnnotations(map[string]string{"test": "test"})
		_, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Update(ctx, sc1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		handler1.AssertConsistently(g, time.Second*2)
		handler2.AssertConsistently(g, time.Second*2)
	})
}

func boostrapInformerManagerWithFakeClients(
	g *gomega.WithT,
	ctx context.Context,
	ftcs []*fedcorev1a1.FederatedTypeConfig,
	objects []*unstructured.Unstructured,
	eventHandlerGenerators []*EventHandlerGenerator,
) (InformerManager, dynamicclient.Interface, fedclient.Interface) {
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
	informerManager := NewInformerManager(dynamicClient, factory.Core().V1alpha1().FederatedTypeConfigs())

	for _, generator := range eventHandlerGenerators {
		informerManager.AddEventHandlerGenerator(generator)
	}

	factory.Start(ctx.Done())
	informerManager.Start(ctx)

	stopCh := make(chan struct{})
	go func() {
		<-time.After(time.Second * 3)
		close(stopCh)
	}()

	if !cache.WaitForCacheSync(stopCh, informerManager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	return informerManager, dynamicClient, fedClient
}
