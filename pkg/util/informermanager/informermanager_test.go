package informermanager

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestListerAvailableForExistingFTCs(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	manager, _, _ := boostrapInformerManagerWithFakeClients(defaultFTCs, []*unstructured.Unstructured{})

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

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

	// sanity check: gvr without corresponding ftc should not exist
	gvr := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "daemonsets",
	}
	lister, informerSynced, exists := manager.GetResourceLister(gvr)
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(lister).To(gomega.BeNil())
	g.Expect(informerSynced).To(gomega.BeNil())
}

func TestListerAvailableForNewFTC(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	manager, _, fedClient := boostrapInformerManagerWithFakeClients([]*fedcorev1a1.FederatedTypeConfig{}, []*unstructured.Unstructured{})

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	ftc := daemonsetFTC
	_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	apiresource := ftc.GetSourceType()
	gvr := schemautil.APIResourceToGVR(&apiresource)

	g.Eventually(func(g gomega.Gomega) {
		lister, informerSynced, exists := manager.GetResourceLister(gvr)
		g.Expect(exists).To(gomega.BeTrue())
		g.Expect(lister).ToNot(gomega.BeNil())
		g.Expect(informerSynced()).To(gomega.BeTrue())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// sanity check: gvr without corresponding ftc should not exist
	gvr = schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "statefulsets",
	}
	lister, informerSynced, exists := manager.GetResourceLister(gvr)
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(lister).To(gomega.BeNil())
	g.Expect(informerSynced).To(gomega.BeNil())
}

func TestEventHandlerRegistrationForExistingFTCs(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	dp1 := getDeployment("dp-1", "default")
	cm1 := getConfigMap("cm-1", "default")
	sc1 := getSecret("sc-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := []*unstructured.Unstructured{dp1, cm1, sc1}
	manager, dynamicClient, _ := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	registeredResourceEventHandler := &countingResourceEventHandler{}
	unregisteredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: registeredResourceEventHandler.generateEventHandler,
	})
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool { return false },
		Generator: unregisteredResourceEventHandler.generateEventHandler,
	})

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 1. If Predicate returns true, EventHandler should be generated and registered.

	// check event handlers generated and initial events are received

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// check if additional events can be received

	// +1 add
	sc2 := getSecret("sc-2", "default")
	sc2, err := dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 update
	dp1.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp1, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 delete
	err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// santiy check: events for gvr without corresponding FTC should not be received
	dm1 := getDaemonSet("dm-1", "default")
	_, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Create(ctx, dm1, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)+1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	})

	// 2. If Predicate returns false, EventHandler should not be generated and registered.

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(unregisteredResourceEventHandler.getGenerateCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getAddEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

func TestEventHandlerRegistrationForNewFTC(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	dm1 := getDaemonSet("dm-1", "default")
	dm2 := getDaemonSet("dm-2", "default")
	dm3 := getDaemonSet("dm-3", "default")
	dm4 := getDaemonSet("dm-4", "default")

	defaultObjects := []*unstructured.Unstructured{dm1, dm2, dm3, dm4}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients([]*fedcorev1a1.FederatedTypeConfig{}, defaultObjects)

	registeredResourceEventHandler := &countingResourceEventHandler{}
	unregisteredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: registeredResourceEventHandler.generateEventHandler,
	})
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool { return false },
		Generator: unregisteredResourceEventHandler.generateEventHandler,
	})

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	ftc := daemonsetFTC
	_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 1. If Predicate returns true, a new EventHandler should be generated and registered.

	// check event handlers generated and initial events are received

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// check if additional events can be received

	// +2 update
	dm1.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dm1, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm1, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	dm2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dm2, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 delete
	err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Delete(ctx, dm4.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// santiy check: events for gvr without corresponding FTC should not be received
	sc1 := getSecret("sc-1", "default")
	_, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc1, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 2))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	})

	// 2. If Predicate returns false, no new EventHandlers should be generated and registered.

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(unregisteredResourceEventHandler.getGenerateCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getAddEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

func TestEventHandlerRegistrationOnFTCUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ftc := deploymentFTC.DeepCopy()
	ftc.SetAnnotations(map[string]string{"predicate": "false"})

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, []*unstructured.Unstructured{})

	registeredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool {
			anno := ftc.GetAnnotations()
			return anno != nil && anno["predicate"] == "true"
		},
		Generator: registeredResourceEventHandler.generateEventHandler,
	})

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// sanity check: no event handler should have been generated and no events should have been received
	g.Consistently(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 1. If Predicate returns true and there is no existing EventHandler, a new EventHandler should be generated and
	// registered.

	ftc.SetAnnotations(map[string]string{"predicate": "true"})
	ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	// sleep for a second to allow the InformerManager to process the ftc update
	<-time.After(time.Second)

	dp1 := getDeployment("dp-1", "default")
	dp1, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp1, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 2. If Predicate returns true and there is an existing EventHandler, a new EventHandler should not be generated but
	// the existing EventHandler should remain registered.

	ftc.SetAnnotations(map[string]string{"predicate": "true", "update-trigger": "1"})
	ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	// sleep for a second to allow the InformerManager to process the ftc update
	<-time.After(time.Second)

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	dp1.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp1, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 3. If Predicate returns false and there is an existing EventHandler, the existing EventHandler should be
	// unregistered.

	ftc.SetAnnotations(map[string]string{"predicate": "false"})
	ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	// sleep for a second to allow the InformerManager to process the ftc update
	<-time.After(time.Second)

	// events should no longer be received for deployments
	dp2 := getDeployment("dp-2", "default")
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 4. If Predicate returns false and there is no existing EventHandler, no new EventHandlers should be generated and
	// registered.

	ftc.SetAnnotations(map[string]string{"predicate": "false", "update-trigger": "1"})
	ftc, err = fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	// sleep for a second to allow the InformerManager to process the ftc update
	<-time.After(time.Second)

	err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

func TestEventHandlerRegistrationOnFTCDelete(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	dp1 := getDeployment("dp-1", "default")
	cm1 := getConfigMap("cm-1", "default")
	sc1 := getSecret("sc-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := []*unstructured.Unstructured{dp1, cm1, sc1}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	registeredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: registeredResourceEventHandler.generateEventHandler,
	})

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// All existing EventHandlers for the FTC should be unregistered or stop receiving events.

	// sanity check: event handlers generated and initial events are received

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// delete deployment ftc
	err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Delete(ctx, deploymentFTC.Name, metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// sleep for a second to allow the InformerManager to process the ftc deletion
	<-time.After(time.Second)

	// events should no longer be received for deployments
	dp2 := getDeployment("dp-2", "default")
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// sanity check: events should still be received for the other remaining ftcs' source types

	// +1 add
	sc2 := getSecret("sc-2", "default")
	sc2, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 delete
	err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)+1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

func TestEventHandlerRegistrationAfterInformerShutdown(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	dp1 := getDeployment("dp-1", "default")
	cm1 := getConfigMap("cm-1", "default")
	sc1 := getSecret("sc-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := []*unstructured.Unstructured{dp1, cm1, sc1}
	manager, dynamicClient, _ := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	registeredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(ftc *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: registeredResourceEventHandler.generateEventHandler,
	})

	ctx, managerCancel := context.WithCancel(context.Background())
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// All existing EventHandlers for the FTC should be unregistered or stop receiving events.

	// sanity check: event handlers generated and initial events are received

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// stop manager
	managerCancel()
	// sleep for a second to allow the InformerManager to process the shutdown
	<-time.After(time.Second)

	// events should no longer be received for any ftc's source type
	dp2 := getDeployment("dp-2", "default")
	dp2, err := dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	cm2 := getConfigMap("cm-2", "default")
	cm2, err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Create(ctx, cm2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	sc2 := getConfigMap("sc-2", "default")
	sc2, err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Create(ctx, sc2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

func boostrapInformerManagerWithFakeClients(
	ftcs []*fedcorev1a1.FederatedTypeConfig,
	objects []*unstructured.Unstructured,
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

	factory.Start(context.TODO().Done())

	return informerManager, dynamicClient, fedClient
}

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
