package informermanager

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// Verifies that the listers for the SourceType GVR of existing FTCs in the cluster are eventually available after the
// InformerManager is started.
func TestInformerManagerListerAvailableForExistingFTCs(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with FTCs for deployments, configmaps and secrets.

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	manager, _, _ := boostrapInformerManagerWithFakeClients(defaultFTCs, []*unstructured.Unstructured{})

	// 2. Start the manager

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 3. Verify that the listers for each FTC's SourceType GVR is eventually available.

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

	// 4. Sanity check: the lister for a GVR without a corresponding FTC should not exist

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

// Verifies that the listers for the SourceType of FTCs created after the InformerManager is started eventually becomes
// available.
func TestInformerManagerListerAvailableForNewFTC(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with no FTCs to begin with.

	manager, _, fedClient := boostrapInformerManagerWithFakeClients([]*fedcorev1a1.FederatedTypeConfig{}, []*unstructured.Unstructured{})

	// 2. Start the InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 3. Initialize daemonset FTC that will be created later.

	ftc := daemonsetFTC
	apiresource := ftc.GetSourceType()
	gvr := schemautil.APIResourceToGVR(&apiresource)

	// 4. Santiy check: verify that the lister for daemonsets is initially not available

	lister, informerSynced, exists := manager.GetResourceLister(gvr)
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(lister).To(gomega.BeNil())
	g.Expect(informerSynced).To(gomega.BeNil())

	// 5. Create the daemonset FTC.

	_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 6. Verify the the lister for the SourceType of the newly created daemonset FTC is eventually available.

	g.Eventually(func(g gomega.Gomega) {
		lister, informerSynced, exists := manager.GetResourceLister(gvr)
		g.Expect(exists).To(gomega.BeTrue())
		g.Expect(lister).ToNot(gomega.BeNil())
		g.Expect(informerSynced()).To(gomega.BeTrue())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 7. Sanity check: the lister for a GVR without a corresponding FTC should not exist

	lister, informerSynced, exists = manager.GetResourceLister(common.DeploymentGVR)
	g.Expect(exists).To(gomega.BeFalse())
	g.Expect(lister).To(gomega.BeNil())
	g.Expect(informerSynced).To(gomega.BeNil())
}

// Verifies that event handlers from EventHandlerGenerators are registered for existing FTCs after the
// InformerManager is started.
func TestInformerManagerEventHandlerRegistrationForExistingFTCs(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with FTCs for deplyoments, configmaps and secrets. Also create an existing
	// deployment, configmap and secret.

	dp1 := getDeployment("dp-1", "default")
	cm1 := getConfigMap("cm-1", "default")
	sc1 := getSecret("sc-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := []*unstructured.Unstructured{dp1, cm1, sc1}
	manager, dynamicClient, _ := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager. registeredResourceEventHandler SHOULD be registered to ALL
	// FTCs (based on its Predicate), unregisteredResourceEventHandler SHOULD NOT be registered for ANY FTCs (based on
	// its Predicate).

	registeredResourceEventHandler := &countingResourceEventHandler{}
	unregisteredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: registeredResourceEventHandler.generateEventHandler,
	})
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return false },
		Generator: unregisteredResourceEventHandler.generateEventHandler,
	})

	// 3. Start the InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Verify that the registeredResourceEventHandler is eventually registered for ALL FTCs and that the add events
	// for the existing objects are ALL RECEIVED.

	g.Eventually(func(g gomega.Gomega) {
		// The generate function should be called once for each FTC.
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		// The number of add events should be equal to the number of current existing objects.
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Verify that additional events continue to be received by registeredResourceEventHandler.

	// 5a. Generate +1 add event for secrets.

	sc2 := getSecret("sc-2", "default")
	sc2, err := dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 5b. Generate +1 update event for deployments.

	dp1.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp1, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 5c. Generate +1 delete event for configmaps.

	err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 5d. Santiy check: events for GVR without a corresponding FTC should not be received.

	dm1 := getDaemonSet("dm-1", "default")
	_, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Create(ctx, dm1, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)+1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	})

	// 6. Verify that unregisteredResourceEventHandler is not generated and receives 0 events.

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(unregisteredResourceEventHandler.getGenerateCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getAddEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

// Verifies that event handlers from EventHandlerGenerators are registered for new FTCs created after the
// InformerManager is started.
func TestInformerManagerEventHandlerRegistrationForNewFTC(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with no FTCs to begin with, but with 4 existing daemonsets.

	dm1 := getDaemonSet("dm-1", "default")
	dm2 := getDaemonSet("dm-2", "default")
	dm3 := getDaemonSet("dm-3", "default")
	dm4 := getDaemonSet("dm-4", "default")

	defaultObjects := []*unstructured.Unstructured{dm1, dm2, dm3, dm4}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients([]*fedcorev1a1.FederatedTypeConfig{}, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager. registeredResourceEventHandler SHOULD be registered to ALL
	// FTCs (based on its Predicate), unregisteredResourceEventHandler SHOULD NOT be registered for ANY FTCs (based on
	// its Predicate).

	registeredResourceEventHandler := &countingResourceEventHandler{}
	unregisteredResourceEventHandler := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: registeredResourceEventHandler.generateEventHandler,
	})
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return false },
		Generator: unregisteredResourceEventHandler.generateEventHandler,
	})

	// 3. Start InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Create a new FTC for daemonsets.

	ftc := daemonsetFTC
	_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Create(ctx, ftc, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 5. Verify that the registeredResourceEventHandler is eventually registered for the new daemonset FTC and that the
	// add events for the existing objects are ALL RECEIVED.

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 6. Verify that additional events continue to be received by registeredResourceEventHandler.

	// 6a. Generate +2 update events for daemonsets.

	dm1.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dm1, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm1, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	dm2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dm2, err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Update(ctx, dm2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 6b. Generate +1 delete event for daemonsets.

	err = dynamicClient.Resource(common.DaemonSetGVR).Namespace("default").Delete(ctx, dm4.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 6c. Santiy check: events for GVRs without a corresponding FTC should not be received.

	sc1 := getSecret("sc-1", "default")
	_, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc1, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(registeredResourceEventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(registeredResourceEventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 2))
		g.Expect(registeredResourceEventHandler.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	})

	// 7. Verify that unregisteredResourceEventHandler is not generated and receives 0 events.

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(unregisteredResourceEventHandler.getGenerateCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getAddEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(unregisteredResourceEventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

// Verifies that the EventHandlerGenerators receive the correct lastApplied and latest FTCs.
func TestInformerManagerEventHandlerGeneratorsReceiveCorrectFTCs(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environemnt with a single Deployment FTC.

	generation := 1

	ftc := deploymentFTC.DeepCopy()
	ftc.SetAnnotations(map[string]string{"generation": strconv.Itoa(generation)})
	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}

	manager, _, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, []*unstructured.Unstructured{})

	// 2. Add EventHandlerGenerators to the InformerManager, the EventHandlerGenerator verifies that the "generation"
	// annotation matches the generation variable

	// lock is used to ensure that the FTC events are not squashed by the InformerManager and that each event is
	// processed.
	lock := make(chan struct{})

	eventHandler := &countingResourceEventHandler{}
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied *fedcorev1a1.FederatedTypeConfig, latest *fedcorev1a1.FederatedTypeConfig) bool {
			switch {
			case generation == 1:
				// if newly created, expect lastApplied to be nil
				g.Expect(lastApplied).To(gomega.BeNil())
			default:
				g.Expect(strconv.Atoi(lastApplied.GetAnnotations()["generation"])).To(gomega.BeNumerically("==", generation-1))
				g.Expect(strconv.Atoi(latest.GetAnnotations()["generation"])).To(gomega.BeNumerically("==", generation))
			}

			<-lock

			return true
		},
		Generator: eventHandler.generateEventHandler,
	})

	// 3. Start InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	lock <- struct{}{}

	// 4. Trigger FTC updates

	for i := 0; i < 5; i++ {
		generation++
		ftc.SetAnnotations(map[string]string{"generation": strconv.Itoa(generation)})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		lock <- struct{}{}
	}
}

// Verifies that the event handler from EventHandlerGenerator is generated and registered on a corresponding FTC update
// where Predicate returns true and Generator returns an event handler.
func TestInformerManagerEventHandlerRegisteredOnFTCUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environemnt with a single Deployment FTC and a single deployment.

	dp1 := getDeployment("dp-1", "default")

	ftc := deploymentFTC.DeepCopy()
	ftc.SetAnnotations(map[string]string{"predicate": "false"})

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
	defaultObjects := []*unstructured.Unstructured{dp1}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager, eventHandler SHOULD be generated when the "predicate"
	// annotation of the FTC is "true".

	eventHandler := &countingResourceEventHandler{}
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied *fedcorev1a1.FederatedTypeConfig, latest *fedcorev1a1.FederatedTypeConfig) bool {
			annotations := latest.GetAnnotations()
			return annotations["predicate"] == "true"
		},
		Generator: eventHandler.generateEventHandler,
	})

	// 3. Start InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Sanity check: eventHandler should not be registered initially

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(eventHandler.getGenerateCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getAddEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Trigger a registration by updating the "predicate" annotation to "true"

	ftc.SetAnnotations(map[string]string{"predicate": "true"})
	ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 6. Generate events for deployments

	// +1 add event

	dp2 := getDeployment("dp-2", "default")
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 update event

	dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 delete event

	err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 7. Verify that events are eventually received by eventHandler

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 2))
		g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

// Verifies that the event handler from EventHandlerGenerator is unregistered on a corresponding FTC update
// where Predicate returns true and Generator returns a nil event handler.
func TestInformerManagerEventHandlerUnregisteredOnFTCUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environemnt with a single Deployment FTC and a single deployment.

	dp1 := getDeployment("dp-1", "default")

	ftc := deploymentFTC.DeepCopy()
	ftc.SetAnnotations(map[string]string{"generator": "true"})

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
	defaultObjects := []*unstructured.Unstructured{dp1}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager, Predicate always returns true and Generator returns
	// eventHandler if the "generator" annotation == "true". Otherwise Generator returns nil.

	eventHandler := &countingResourceEventHandler{}
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied *fedcorev1a1.FederatedTypeConfig, latest *fedcorev1a1.FederatedTypeConfig) bool {
			return true
		},
		Generator: func(ftc *fedcorev1a1.FederatedTypeConfig) cache.ResourceEventHandler {
			annotations := ftc.GetAnnotations()
			if annotations["generator"] == "true" {
				return eventHandler.generateEventHandler(ftc)
			}
			return nil
		},
	})

	// 3. Start InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Sanity check: eventHandler should be registered initially

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Trigger an unregistration by updating the "predicate" annotation to "false"

	ftc.SetAnnotations(map[string]string{"generator": "false"})
	ftc, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 6. Sleep for a second to allow the InformerManager to process the FTC update.
	<-time.After(time.Second)

	// 7. Generate events for deployments

	// +1 add event

	dp2 := getDeployment("dp-2", "default")
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 update event

	dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// +1 delete event

	err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp2.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 8. Verify that events are no longer received by eventHandler.

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

// Verifies that the event handler from an EventHandlerGenerator is regenerated and registered on a corresponding FTC
// update where Predicate returns true and Generator returns an event handler.
func TestInformerManagerEventHandlerReregisteredFTCUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environemnt with a single Deployment FTC and a single deployment.

	ftc := deploymentFTC.DeepCopy()
	dp1 := getDeployment("dp-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
	defaultObjects := []*unstructured.Unstructured{dp1}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager, eventHandler SHOULD always be regenerated and registered.

	eventHandler := &countingResourceEventHandler{}
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied *fedcorev1a1.FederatedTypeConfig, latest *fedcorev1a1.FederatedTypeConfig) bool {
			return true
		},
		Generator: eventHandler.generateEventHandler,
	})

	// 3. Start InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Verify that eventHandler is generated and registered initially.

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Trigger FTC updates

	for i := 0; i < 5; i++ {
		ftc.SetAnnotations(map[string]string{"trigger": strconv.Itoa(rand.Intn(1000))})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 5a. Generate deployment events as well

		// +1 Update event

		dp1.SetAnnotations(map[string]string{"trigger": strconv.Itoa(rand.Intn(1000))})
		dp1, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 5b. Verify eventHandler is regenerated and registered

		g.Eventually(func(g gomega.Gomega) {
			g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1+i))
			g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
			g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", i))
			g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
		})
	}
}

// Verifies that the event handler from an EventHandlerGenerator is unchanged after a corresponding FTC update where
// Predicate returns false and an event handler did not exist previously.
func TestInformerManagerEventHandlerUnchangedFTCUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environemnt with a single Deployment FTC and a single deployment.

	ftc := deploymentFTC.DeepCopy()
	dp1 := getDeployment("dp-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{ftc}
	defaultObjects := []*unstructured.Unstructured{dp1}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager, eventHandler SHOULD only be created once per new FTC and
	// never be regenerated.

	eventHandler := &countingResourceEventHandler{}
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied *fedcorev1a1.FederatedTypeConfig, latest *fedcorev1a1.FederatedTypeConfig) bool {
			if lastApplied == nil {
				return true
			}
			return false
		},
		Generator: eventHandler.generateEventHandler,
	})

	// 3. Start InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Verify that eventHandler is generated and registered initially.

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
		g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Trigger FTC updates

	for i := 0; i < 5; i++ {
		ftc.SetAnnotations(map[string]string{"trigger": strconv.Itoa(rand.Intn(1000))})
		_, err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Update(ctx, ftc, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 5a. Generate deployment events as well

		// +1 Update event

		dp1.SetAnnotations(map[string]string{"trigger": strconv.Itoa(rand.Intn(1000))})
		dp1, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp1, metav1.UpdateOptions{})
		g.Expect(err).ToNot(gomega.HaveOccurred())

		// 5b. Verify eventHandler is not regenerated but continues to be registered.

		g.Eventually(func(g gomega.Gomega) {
			g.Expect(eventHandler.getGenerateCount()).To(gomega.BeNumerically("==", 1))
			g.Expect(eventHandler.getAddEventCount()).To(gomega.BeNumerically("==", 1))
			g.Expect(eventHandler.getUpdateEventCount()).To(gomega.BeNumerically("==", i))
			g.Expect(eventHandler.getDeleteEventCount()).To(gomega.BeZero())
		})
	}
}

// Verifies that event handlers from EventHandlerGenerators are unregistered when a FTC is deleted.
func TestInformerManagerEventHandlerRegistrationOnFTCDelete(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with FTCs for deplyoments, configmaps and secrets. Also create an existing
	// deployment, configmap and secret.

	dp1 := getDeployment("dp-1", "default")
	cm1 := getConfigMap("cm-1", "default")
	sc1 := getSecret("sc-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := []*unstructured.Unstructured{dp1, cm1, sc1}
	manager, dynamicClient, fedClient := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager. eventHandler1 and eventHandler2 SHOULD be registered to ALL
	// FTCs (based on its Predicate).

	eventHandler1 := &countingResourceEventHandler{}
	eventHandler2 := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: eventHandler1.generateEventHandler,
	})
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: eventHandler2.generateEventHandler,
	})

	// 3. Start the InformerManager.

	ctx := context.Background()
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Santiy check: verify that both eventHandler1 and eventHandler2 is registered and received events for the
	// existing objects.

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler1.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler1.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler1.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler1.getDeleteEventCount()).To(gomega.BeZero())

		g.Expect(eventHandler2.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler2.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler2.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler2.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Delete the deployment FTC.

	err := fedClient.CoreV1alpha1().FederatedTypeConfigs().Delete(ctx, deploymentFTC.Name, metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 6. Sleep for a second to allow the InformerManager to process the FTC deletion.

	<-time.After(time.Second)

	// 7. Verify that events are no longer received by eventHandler1 and eventHandler2.

	// 7a. Generate +1 add event for deployments.

	dp2 := getDeployment("dp-2", "default")
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 7b. Generate +1 update event for deployments.

	dp2.SetAnnotations(map[string]string{"test-annotation": "test-value"})
	dp2, err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Update(ctx, dp2, metav1.UpdateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 7c. Generate +1 delete event for deployments.

	err = dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Delete(ctx, dp1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(eventHandler1.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler1.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler1.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler1.getDeleteEventCount()).To(gomega.BeZero())

		g.Expect(eventHandler2.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler2.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler2.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler2.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 8. Sanity check: verify that events continue to be received for the other remaining FTCs' source types

	// 8a. Generate +1 add event for secrets.

	sc2 := getSecret("sc-2", "default")
	sc2, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 8b. Generate +1 update event for configmaps.

	err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Delete(ctx, cm1.GetName(), metav1.DeleteOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler1.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler1.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)+1))
		g.Expect(eventHandler1.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler1.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))

		g.Expect(eventHandler2.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler2.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)+1))
		g.Expect(eventHandler2.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler2.getDeleteEventCount()).To(gomega.BeNumerically("==", 1))
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())
}

// Verifies that all event handlers from EventHandlerGenerators no longer receive any events after the InformerManager
// is shutdown (or when the context passed to the Start method expires).
func TestInformerManagerEventHandlerRegistrationOnShutdown(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// 1. Bootstrap an environment with FTCs for deplyoments, configmaps and secrets. Also create an existing
	// deployment, configmap and secret.

	dp1 := getDeployment("dp-1", "default")
	cm1 := getConfigMap("cm-1", "default")
	sc1 := getSecret("sc-1", "default")

	defaultFTCs := []*fedcorev1a1.FederatedTypeConfig{deploymentFTC, configmapFTC, secretFTC}
	defaultObjects := []*unstructured.Unstructured{dp1, cm1, sc1}
	manager, dynamicClient, _ := boostrapInformerManagerWithFakeClients(defaultFTCs, defaultObjects)

	// 2. Add EventHandlerGenerators to the InformerManager. eventHandler1 and eventHandler2 SHOULD be registered to ALL
	// FTCs (based on its Predicate).

	eventHandler1 := &countingResourceEventHandler{}
	eventHandler2 := &countingResourceEventHandler{}

	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: eventHandler1.generateEventHandler,
	})
	manager.AddEventHandlerGenerator(&EventHandlerGenerator{
		Predicate: func(lastApplied, latest *fedcorev1a1.FederatedTypeConfig) bool { return true },
		Generator: eventHandler2.generateEventHandler,
	})

	// 3. Start the InformerManager.

	ctx, managerCancel := context.WithCancel(context.Background())
	manager.Start(ctx)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	if !cache.WaitForCacheSync(ctxWithTimeout.Done(), manager.HasSynced) {
		g.Fail("Timed out waiting for InformerManager cache sync")
	}

	// 4. Santiy check: verify that both eventHandler1 and eventHandler2 is registered and received events for the
	// existing objects.

	g.Eventually(func(g gomega.Gomega) {
		g.Expect(eventHandler1.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler1.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler1.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler1.getDeleteEventCount()).To(gomega.BeZero())

		g.Expect(eventHandler2.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler2.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler2.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler2.getDeleteEventCount()).To(gomega.BeZero())
	}).WithTimeout(time.Second * 2).Should(gomega.Succeed())

	// 5. Stop the InformerManager

	managerCancel()

	// 6. Sleep for a second to allow the InformerManager to process the shutdown

	<-time.After(time.Second)

	// 7. Verify that events are not received for ANY FTCs by both eventHandler1 and eventHandler2.

	// 7a. Generate +1 add event for deployments.

	dp2 := getDeployment("dp-2", "default")
	dp2, err := dynamicClient.Resource(common.DeploymentGVR).Namespace("default").Create(ctx, dp2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 7b. Generate +1 add event for configmaps.

	cm2 := getConfigMap("cm-2", "default")
	cm2, err = dynamicClient.Resource(common.ConfigMapGVR).Namespace("default").Create(ctx, cm2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// 7b. Generate +1 add event for secrets.

	sc2 := getSecret("sc-2", "default")
	sc2, err = dynamicClient.Resource(common.SecretGVR).Namespace("default").Create(ctx, sc2, metav1.CreateOptions{})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	g.Consistently(func(g gomega.Gomega) {
		g.Expect(eventHandler1.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler1.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler1.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler1.getDeleteEventCount()).To(gomega.BeZero())

		g.Expect(eventHandler2.getGenerateCount()).To(gomega.BeNumerically("==", len(defaultFTCs)))
		g.Expect(eventHandler2.getAddEventCount()).To(gomega.BeNumerically("==", len(defaultObjects)))
		g.Expect(eventHandler2.getUpdateEventCount()).To(gomega.BeZero())
		g.Expect(eventHandler2.getDeleteEventCount()).To(gomega.BeZero())
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
