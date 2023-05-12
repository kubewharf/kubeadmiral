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

package automigration

import (
	"context"
	"sort"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/policies"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/util"
)

var (
	autoMigrationTestLabel = ginkgo.Label("auto-migration")

	defaultPollingInterval = 10 * time.Millisecond

	resourcePropagationTimeout = 10 * time.Second
	replicasReadyTimeout       = 30 * time.Second
	autoMigrationTimeout       = 50 * time.Second
)

var _ = ginkgo.Describe("auto migration", autoMigrationTestLabel, func() {
	f := framework.NewFramework("auto-migration", framework.FrameworkOptions{CreateNamespace: true})

	ginkgo.It("Should automatically migrate unschedulable pods", func(ctx context.Context) {
		var err error

		var clusters []*fedcorev1a1.FederatedCluster
		var clusterToMigrateFrom *fedcorev1a1.FederatedCluster
		ginkgo.By("Getting clusters", func() {
			clusterList, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().List(ctx, metav1.ListOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

			candidateClustes := make([]*fedcorev1a1.FederatedCluster, len(clusterList.Items))
			for i := range clusterList.Items {
				candidateClustes[i] = &clusterList.Items[i]
			}
			candidateClustes = util.FilterOutE2ETestObjects(candidateClustes)
			gomega.Expect(len(candidateClustes)).To(gomega.BeNumerically(">=", 3), "At least 3 clusters are required for this test")

			clusters = candidateClustes[:3]
			sort.Slice(clusters, func(i, j int) bool {
				return clusters[i].Name < clusters[j].Name
			})
			clusterToMigrateFrom = clusters[0]
		})

		propPolicy := policies.PropagationPolicyForClustersWithPlacements(f.Name(), clusters)
		// Equal weight for all clusters
		for i := range propPolicy.Spec.Placements {
			propPolicy.Spec.Placements[i].Preferences = fedcorev1a1.Preferences{
				Weight: pointer.Int64(1),
			}
		}
		propPolicy.Spec.SchedulingMode = fedcorev1a1.SchedulingModeDivide
		// Enable auto migration; don't keep unschedulable replicas
		propPolicy.Spec.AutoMigration = &fedcorev1a1.AutoMigration{
			Trigger: fedcorev1a1.AutoMigrationTrigger{
				PodUnschedulableDuration: &metav1.Duration{Duration: 5 * time.Second},
			},
			KeepUnschedulableReplicas: false,
		}

		ginkgo.By("Creating PropagationPolicy", func() {
			propPolicy, err = f.HostFedClient().CoreV1alpha1().PropagationPolicies(f.TestNamespace().Name).Create(
				ctx,
				propPolicy,
				metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		replicasPerCluster := int32(2)
		totalReplicas := int32(len(clusters)) * replicasPerCluster
		replicasPerClusterAfterMigration := replicasPerCluster + 1

		dp := resources.GetSimpleDeployment(f.Name())
		dp.Spec.Replicas = pointer.Int32(totalReplicas)
		maxSurge := intstr.FromInt(0)
		dp.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				// Prevent upgrade getting stuck
				MaxSurge: &maxSurge,
			},
		}
		policies.SetPropagationPolicy(dp, propPolicy)

		ginkgo.By("Creating Deployment", func() {
			dp, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Create(
				ctx, dp, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Checking replicas evenly distributed in member clusters", func() {
			ctx, cancel := context.WithTimeout(ctx, resourcePropagationTimeout)
			defer cancel()

			util.AssertForItems(clusters, func(g gomega.Gomega, c *fedcorev1a1.FederatedCluster) {
				g.Eventually(ctx, func(g gomega.Gomega) {
					clusterDp, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp.Name, metav1.GetOptions{},
					)
					if err != nil {
						gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
					}
					g.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(replicasPerCluster)))
				}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			})
		})

		ginkgo.By("Checking replicas ready in member clusters", func() {
			ctx, cancel := context.WithTimeout(ctx, replicasReadyTimeout)
			defer cancel()

			util.AssertForItems(clusters, func(g gomega.Gomega, c *fedcorev1a1.FederatedCluster) {
				g.Eventually(ctx, func(g gomega.Gomega) {
					clusterDp, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp.Name, metav1.GetOptions{},
					)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp.Status.ReadyReplicas).To(gomega.Equal(replicasPerCluster))
				}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			})
		})

		overridePolicy := policies.OverridePolicyForClustersWithPatches(f.Name(), map[string]fedcorev1a1.Overriders{
			clusterToMigrateFrom.Name: {
				JsonPatch: []fedcorev1a1.JsonPatchOverrider{{
					Operator: "add",
					Path:     "/spec/template/spec/nodeSelector",
					Value: apiextensionsv1.JSON{
						// should select no nodes
						Raw: []byte(`{"non-existing-key": "non-existing-value"}`),
					},
				}},
			},
		})
		ginkgo.By("Creating OverridePolicy", func() {
			overridePolicy, err = f.HostFedClient().CoreV1alpha1().OverridePolicies(f.TestNamespace().Name).Create(
				ctx, overridePolicy, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Adding OverridePolicy to Deployment", func() {
			policies.SetOverridePolicy(dp, overridePolicy.Name)
			dp, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Patch(
				ctx, dp.Name, types.MergePatchType,
				[]byte(`{"metadata":{"labels":{"`+override.OverridePolicyNameLabel+`":"`+overridePolicy.Name+`"}}}`), metav1.PatchOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Check override propagated", func() {
			ctx, cancel := context.WithTimeout(ctx, resourcePropagationTimeout)
			defer cancel()

			util.AssertForItems(clusters, func(g gomega.Gomega, c *fedcorev1a1.FederatedCluster) {
				g.Eventually(ctx, func(g gomega.Gomega) {
					clusterDp, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp.Name, metav1.GetOptions{},
					)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					if c.Name == clusterToMigrateFrom.Name {
						g.Expect(clusterDp.Spec.Template.Spec.NodeSelector).To(gomega.HaveKeyWithValue("non-existing-key", "non-existing-value"))
					} else {
						gomega.Expect(clusterDp.Spec.Template.Spec.NodeSelector).To(gomega.BeEmpty())
					}
				}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			})
		})

		ginkgo.By("Check pods migrated", func() {
			ctx, cancel := context.WithTimeout(ctx, autoMigrationTimeout)
			defer cancel()

			gomega.Eventually(ctx, func(g gomega.Gomega) {
				_, err := f.ClusterKubeClient(ctx, clusterToMigrateFrom).AppsV1().Deployments(f.TestNamespace().Name).Get(
					ctx, dp.Name, metav1.GetOptions{},
				)
				g.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
			}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())

			for _, cluster := range clusters {
				if cluster.Name == clusterToMigrateFrom.Name {
					continue
				}
				gomega.Eventually(ctx, func(g gomega.Gomega) {
					clusterDp, err := f.ClusterKubeClient(ctx, cluster).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp.Name, metav1.GetOptions{},
					)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(replicasPerClusterAfterMigration)))
				}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			}
		})
	})
})
