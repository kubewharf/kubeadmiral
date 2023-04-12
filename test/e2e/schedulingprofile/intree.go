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

package schedulingprofile

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/policies"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
)

var scheduleTimeout = time.Second * 10

var _ = ginkgo.Describe("Scheduling Profile", func() {
	f := framework.NewFramework("scheduling-profile", framework.FrameworkOptions{CreateNamespace: true})

	testPlacementFilter := func(ctx context.Context, profile *fedcorev1a1.SchedulingProfile, enabled bool) {
		clusters, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().List(ctx, metav1.ListOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		if len(clusters.Items) == 0 {
			ginkgo.Fail("Test requires at least one federated cluster")
		}

		ginkgo.By("Creating scheduling profile")
		profile, err = f.HostFedClient().CoreV1alpha1().SchedulingProfiles().Create(ctx, profile, metav1.CreateOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		ginkgo.DeferCleanup(func(ctx ginkgo.SpecContext) {
			if err := f.HostFedClient().CoreV1alpha1().SchedulingProfiles().Delete(ctx, profile.Name, metav1.DeleteOptions{}); err != nil &&
				!apierrors.IsNotFound(err) {
				ginkgo.GinkgoLogr.Error(
					err,
					"failed to cleanup scheduling profile",
					"profile-name",
					profile.Name,
				)
			}
		})

		ginkgo.By("Creating propagation policy that references scheduling profile")
		policy := policies.PropagationPolicyForClustersWithPlacements(f.Name(), []*fedcorev1a1.FederatedCluster{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusters.Items[0].Name,
				},
			},
		})
		policy.Spec.SchedulingProfile = profile.Name
		policy, err = f.HostFedClient().
			CoreV1alpha1().
			PropagationPolicies(f.TestNamespace().Name).
			Create(ctx, policy, metav1.CreateOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

		ginkgo.By("Creating configmap that references propagation policy")
		configMap := resources.GetSimpleConfigMap(f.Name())
		policies.SetPropagationPolicy(configMap, policy)
		configMap, err = f.HostKubeClient().CoreV1().ConfigMaps(f.TestNamespace().Name).Create(ctx, configMap, metav1.CreateOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

		ginkgo.By("Verifying scheduling result")
		if enabled {
			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				federatedConfigMap, err := f.HostDynamicClient().
					Resource(resources.FederatedConfigMapGVR).
					Namespace(f.TestNamespace().Name).
					Get(ctx, configMap.Name, metav1.GetOptions{})
				gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
				g.Expect(err).To(gomega.BeNil())

				placementObj, err := util.UnmarshalGenericPlacements(federatedConfigMap)
				gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

				placement := placementObj.Spec.GetPlacementOrNil(scheduler.PrefixedGlobalSchedulerName)
				g.Expect(placement).ToNot(gomega.BeNil())

				// only the first cluster should be selected since placement plugin is enabled
				clusters, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().List(ctx, metav1.ListOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

				g.Expect(len(placement.Clusters)).To(gomega.Equal(1))
				g.Expect(placement.Clusters[0].Name).To(gomega.Equal(clusters.Items[0].Name))

				ginkgo.GinkgoLogr.Info("Obtained scheduling result", "result", placement.Clusters)
			}).WithTimeout(scheduleTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for scheduling")
		} else {
			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				federatedConfigMap, err := f.HostDynamicClient().
					Resource(resources.FederatedConfigMapGVR).
					Namespace(f.TestNamespace().Name).
					Get(ctx, configMap.Name, metav1.GetOptions{})
				gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
				g.Expect(err).To(gomega.BeNil())

				placementObj, err := util.UnmarshalGenericPlacements(federatedConfigMap)
				gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

				placement := placementObj.Spec.GetPlacementOrNil(scheduler.PrefixedGlobalSchedulerName)
				g.Expect(placement).ToNot(gomega.BeNil())

				// all clusters should be selected since placement plugin was disabled
				g.Expect(len(placement.Clusters)).To(gomega.Equal(len(clusters.Items)))
				clusterSet := sets.New[string]()
				for _, cluster := range clusters.Items {
					clusterSet.Insert(cluster.Name)
				}
				for _, cluster := range placement.Clusters {
					gomega.Expect(clusterSet.Has(cluster.Name)).To(gomega.BeTrue())
				}

				ginkgo.GinkgoLogr.Info("Obtained scheduling result", "result", placement.Clusters)
			}).WithTimeout(scheduleTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for scheduling")
		}
	}

	ginkgo.It("disable no plugins", func(ctx ginkgo.SpecContext) {
		profile := &fedcorev1a1.SchedulingProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", f.Name(), rand.String(12)),
			},
			Spec: fedcorev1a1.SchedulingProfileSpec{
				Plugins: &fedcorev1a1.Plugins{},
			},
		}

		testPlacementFilter(ctx, profile, true)
	})

	ginkgo.It("disable explicit plugin", func(ctx ginkgo.SpecContext) {
		profile := &fedcorev1a1.SchedulingProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", f.Name(), rand.String(12)),
			},
			Spec: fedcorev1a1.SchedulingProfileSpec{
				Plugins: &fedcorev1a1.Plugins{
					Filter: fedcorev1a1.PluginSet{
						Disabled: []fedcorev1a1.Plugin{
							{
								Name: "PlacementFilter",
							},
						},
					},
				},
			},
		}

		testPlacementFilter(ctx, profile, false)
	})

	ginkgo.It("disable wildcard plugin", func(ctx ginkgo.SpecContext) {
		profile := &fedcorev1a1.SchedulingProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name: f.Name(),
			},
			Spec: fedcorev1a1.SchedulingProfileSpec{
				Plugins: &fedcorev1a1.Plugins{
					Filter: fedcorev1a1.PluginSet{
						Disabled: []fedcorev1a1.Plugin{
							{
								Name: "*",
							},
						},
					},
				},
			},
		}

		testPlacementFilter(ctx, profile, false)
	})
})
