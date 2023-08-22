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
	"fmt"
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

	assertNoAutoMigrationDuration = 20 * time.Second
)

var _ = ginkgo.Describe("Auto Migration", autoMigrationTestLabel, func() {
	f := framework.NewFramework("auto-migration", framework.FrameworkOptions{CreateNamespace: true})

	var clusters []*fedcorev1a1.FederatedCluster
	var propPolicy *fedcorev1a1.PropagationPolicy

	ginkgo.JustBeforeEach(func(ctx ginkgo.SpecContext) {
		// This JustBeforeEach node gets 3 member clusters to use in the automigration e2e tests and also generates a
		// propagation policy that distributes replicas evenly across all 3 member clusters. Note that the propagation
		// policy is not created to allow for each test to modify it if required.
		ginkgo.By("Getting clusters", func() {
			clusterList, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().List(ctx, metav1.ListOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

			candidateClusters := make([]*fedcorev1a1.FederatedCluster, len(clusterList.Items))
			for i := range clusterList.Items {
				candidateClusters[i] = &clusterList.Items[i]
			}
			candidateClusters = util.FilterOutE2ETestObjects(candidateClusters)
			gomega.Expect(len(candidateClusters)).To(gomega.BeNumerically(">=", 3), "At least 3 clusters are required for this test")

			sort.Slice(candidateClusters, func(i, j int) bool {
				return candidateClusters[i].Name < candidateClusters[j].Name
			})

			clusters = candidateClusters[:3]
		})

		propPolicy = policies.PropagationPolicyForClustersWithPlacements(f.Name(), clusters)
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
		}
	})

	ginkgo.It("Should automatically migrate unschedulable pods", func(ctx context.Context) {
		clusterToMigrateFrom := clusters[0]

		var err error

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

		ginkgo.By("Creating deployment", func() {
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
						g.Expect(clusterDp.Spec.Template.Spec.NodeSelector).
							To(gomega.HaveKeyWithValue("non-existing-key", "non-existing-value"))
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

	ginkgo.It("Automigration of 2 deployments with overlapping selectors should not affect each other", func(ctx context.Context) {
		// This test case ensures that automigration of 2 deployments with overlapping label selectors do not affect
		// each other. One possible consequence if not handled properly is the unexpected scaling of the 2 deployments.
		//
		// Consider deployments dp-a and dp-b with overlapping selectors:
		//
		// If dp-a has some unschedulable pods, automigration would create extra replicas in other clusters to migrate
		// these pods. If mishandled, these pods might be wrongly counted as dp-b's. If this happens, automigration
		// would attempt to migrate these "extra" pods for dp-b. If the migrated replicas for dp-b are unschedulable as
		// well, they would subsequently be counted as dp-a's and trigger a second round of migration for dp-a. In the
		// worst case, this would result in an infinite scaling loop.

		// 1. Get test clusters

		clusterToMigrateFromForDp1 := clusters[0]
		clusterToMigrateFromForDp2 := clusters[2]

		var err error

		// 2. Initialize replicas

		replicasPerCluster := int32(3)
		totalReplicas := int32(len(clusters)) * replicasPerCluster

		expectedFinalDistributionDp1 := map[string]int32{}
		for _, cluster := range clusters {
			if cluster.Name == clusterToMigrateFromForDp1.Name {
				expectedFinalDistributionDp1[cluster.Name] = replicasPerCluster
			} else {
				// The deployment in clusterToMigrateFromForDp1 will only have 1 schedulable pod (refer to
				// getOverridePolicy below). The remaining should be migrated to the remaining clusters evenly (based on
				// the propagation policy above).
				expectedFinalDistributionDp1[cluster.Name] = replicasPerCluster + ((replicasPerCluster)-1)/int32(len(clusters)-1)
			}
		}

		// If overlapping labels were not accounted for properly, automigration may not occur properly for dp2 because
		// dp1's ready pods in clusterToMigrateFrom may be wrongly counted as dp2's.
		expectedFinalDistributionDp2 := map[string]int32{}
		for _, cluster := range clusters {
			if cluster.Name == clusterToMigrateFromForDp2.Name || cluster.Name == clusterToMigrateFromForDp1.Name {
				expectedFinalDistributionDp2[cluster.Name] = replicasPerCluster
			} else {
				// Because of pod anti-affinity, we expect 0 pods to be able to run in both clusterToMigrateFromForDp1
				// and clusterToMigrateFromForDp2 as both clusters should already contain pods from dp1 that have the
				// same labels (refer to getOverridePolicy below). These 2 * replicasPerCluster pods should be
				// distributed evenly among the remaining clusters.
				expectedFinalDistributionDp2[cluster.Name] = replicasPerCluster + (2*replicasPerCluster)/int32(len(clusters)-2)
			}
		}

		// 3. Initialize deployments

		antiAffinityPodLabelKey := "automigration-test"
		antiAffinityPodLabelValue := "automigration-test"

		dp1 := resources.GetSimpleDeployment(f.Name())
		dp1.Spec.Replicas = pointer.Int32(totalReplicas)
		maxSurge := intstr.FromInt(0)
		dp1.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				// Prevent upgrade getting stuck
				MaxSurge: &maxSurge,
			},
		}
		if dp1.Spec.Template.Labels == nil {
			dp1.Spec.Template.Labels = map[string]string{}
		}
		dp1.Spec.Template.Labels[antiAffinityPodLabelKey] = antiAffinityPodLabelValue
		dp2 := dp1.DeepCopy()

		// 4. Create policies

		propPolicy.Spec.AutoMigration.KeepUnschedulableReplicas = true
		ginkgo.By("Creating PropagationPolicy", func() {
			propPolicy, err = f.HostFedClient().CoreV1alpha1().PropagationPolicies(f.TestNamespace().Name).Create(
				ctx,
				propPolicy,
				metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		// This function creates an override policy that results in only a single pod of a deployment being schedulable
		// in a given cluster using a combination of node affinity and pod anti-affinity.
		getOverridePolicy := func(cluster *fedcorev1a1.FederatedCluster, deploy *appsv1.Deployment) *fedcorev1a1.OverridePolicy {
			var selectedNode string

			nodeList, err := f.ClusterKubeClient(ctx, clusterToMigrateFromForDp1).CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			nodes := nodeList.Items
			gomega.Expect(nodes).ShouldNot(gomega.BeEmpty())

			selectedNode = nodes[0].Name
			return policies.OverridePolicyForClustersWithPatches(f.Name(), map[string]fedcorev1a1.Overriders{
				cluster.Name: {
					JsonPatch: []fedcorev1a1.JsonPatchOverrider{
						{
							Operator: "add",
							Path:     "/spec/template/spec/affinity",
							Value: apiextensionsv1.JSON{
								Raw: []byte(
									//nolint:lll
									`{"nodeAffinity": {"requiredDuringSchedulingIgnoredDuringExecution": {"nodeSelectorTerms": []}}, "podAntiAffinity": {"requiredDuringSchedulingIgnoredDuringExecution": []}}`,
								),
							},
						},
						{
							Path: "/spec/template/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution/nodeSelectorTerms",
							Value: apiextensionsv1.JSON{
								Raw: []byte(
									fmt.Sprintf(
										`[{"matchFields": [{"key": "metadata.name", "operator": "In", "values":  ["%s"]}]}]`,
										selectedNode,
									),
								),
							},
						},
						{
							Path: "/spec/template/spec/affinity/podAntiAffinity/requiredDuringSchedulingIgnoredDuringExecution",
							Value: apiextensionsv1.JSON{
								Raw: []byte(
									fmt.Sprintf(
										`[{"labelSelector": {"matchLabels": {"%s": "%s"}}, "topologyKey": "kubernetes.io/hostname"}]`,
										antiAffinityPodLabelKey,
										antiAffinityPodLabelValue,
									),
								),
							},
						},
					},
				},
			})
		}

		overridePolicyForDp1 := getOverridePolicy(clusterToMigrateFromForDp1, dp1)
		overridePolicyForDp2 := getOverridePolicy(clusterToMigrateFromForDp2, dp2)

		ginkgo.By("Creating OverridePolicies", func() {
			overridePolicyForDp1, err = f.HostFedClient().CoreV1alpha1().OverridePolicies(f.TestNamespace().Name).Create(
				ctx, overridePolicyForDp1, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			overridePolicyForDp2, err = f.HostFedClient().CoreV1alpha1().OverridePolicies(f.TestNamespace().Name).Create(
				ctx, overridePolicyForDp2, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		// 5. Set policies

		policies.SetPropagationPolicy(dp1, propPolicy)
		policies.SetOverridePolicy(dp1, overridePolicyForDp1.Name)
		policies.SetPropagationPolicy(dp2, propPolicy)
		policies.SetOverridePolicy(dp2, overridePolicyForDp2.Name)

		// 6. Create deployments and check results

		ginkgo.By("Creating original deployment", func() {
			dp1, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Create(
				ctx, dp1, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		// This function checks a deployment for two things:
		// 1) the replica count for clusterToMigrateFrom remains the same as replicasPerCluster
		// 2) the replica counts for other clusters match an expected distribution given by newReplicaCounts.
		checkPodsMigrated := func(dpName string, clusterToMigrateFrom *fedcorev1a1.FederatedCluster, newReplicaCounts map[string]int32) {
			ginkgo.GinkgoLogr.Info(fmt.Sprintf("expected distribution: %v", newReplicaCounts))
			ctx, cancel := context.WithTimeout(ctx, resourcePropagationTimeout+autoMigrationTimeout)
			defer cancel()

			for _, cluster := range clusters {
				if cluster.Name == clusterToMigrateFrom.Name {
					continue
				}

				gomega.Eventually(ctx, func(g gomega.Gomega) {
					clusterDp, err := f.ClusterKubeClient(ctx, cluster).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dpName, metav1.GetOptions{},
					)
					gomega.Expect(err).To(gomega.Or(gomega.Not(gomega.HaveOccurred()), gomega.Satisfy(apierrors.IsNotFound)))
					g.Expect(err).ToNot(gomega.HaveOccurred())
					g.Expect(clusterDp.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(newReplicaCounts[cluster.Name])))
				}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			}

			// This ensures keepUnschedulabeReplicas is working correctly
			gomega.Consistently(ctx, func(g gomega.Gomega) {
				clusterDp, err := f.ClusterKubeClient(ctx, clusterToMigrateFrom).AppsV1().Deployments(f.TestNamespace().Name).Get(
					ctx, dp2.Name, metav1.GetOptions{},
				)
				gomega.Expect(err).ToNot(gomega.HaveOccurred())
				g.Expect(clusterDp.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(newReplicaCounts[clusterToMigrateFrom.Name])))
			}).WithTimeout(assertNoAutoMigrationDuration).Should(gomega.Succeed())
		}

		ginkgo.By("Check pods migrated for original deployment", func() {
			checkPodsMigrated(dp1.Name, clusterToMigrateFromForDp1, expectedFinalDistributionDp1)
		})

		// Create a new deployment with overlapping selectors
		dp2.Name = fmt.Sprintf("%s-%s", dp1.Name, "overlap")
		policies.SetPropagationPolicy(dp2, propPolicy)
		policies.SetOverridePolicy(dp2, overridePolicyForDp2.Name)

		ginkgo.By("Creating overlapping deployment", func() {
			dp2, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Create(
				ctx, dp2, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Check pods migrated for overlapping deployment", func() {
			checkPodsMigrated(dp2.Name, clusterToMigrateFromForDp2, expectedFinalDistributionDp2)
		})

		// Similarly, automigration may have occurred unnecessarily for dp1 because dp2's unschedulable pods in
		// clusterToMigrateFrom may be wrongly counted as dp1's.
		ginkgo.By("Check pods did not migrate for original deployment", func() {
			// NOTE: We annotate dp1 to trigger reconciliation by automigration controller, since it currently only
			// reconciles on changes to the deployment itself.
			dp1, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Patch(
				ctx, dp1.Name, types.MergePatchType,
				[]byte(`{"metadata":{"annotations":{"test":"automigration"}}}`), metav1.PatchOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

			gomega.Consistently(ctx, func(g gomega.Gomega) {
				for _, cluster := range clusters {
					clusterDp, err := f.ClusterKubeClient(ctx, cluster).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp1.Name, metav1.GetOptions{},
					)
					gomega.Expect(err).ToNot(gomega.HaveOccurred())
					g.Expect(clusterDp.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(expectedFinalDistributionDp1[cluster.Name])))
				}
			}).WithTimeout(assertNoAutoMigrationDuration).Should(gomega.Succeed())
		})
	})

	ginkgo.It("Should not migrate if capacity for all clusters is 0", func(ctx context.Context) {
		var err error

		// 1. Initialize replicas

		replicasPerCluster := int32(2)
		totalReplicas := int32(len(clusters)) * replicasPerCluster

		// 2. Create policies

		propPolicy.Spec.AutoMigration.KeepUnschedulableReplicas = true
		ginkgo.By("Creating PropagationPolicy", func() {
			propPolicy, err = f.HostFedClient().CoreV1alpha1().PropagationPolicies(f.TestNamespace().Name).Create(
				ctx,
				propPolicy,
				metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		// 3. Create deployments

		dp1 := resources.GetSimpleDeployment(f.Name())
		dp1.Spec.Replicas = pointer.Int32(totalReplicas)
		maxSurge := intstr.FromInt(0)
		dp1.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				// Prevent upgrade getting stuck
				MaxSurge: &maxSurge,
			},
		}
		dp1.Spec.Template.Spec.NodeSelector = map[string]string{
			"non-existing-key": "non-existing-value",
		}
		policies.SetPropagationPolicy(dp1, propPolicy)

		// Create a deployment with overlapping selectors to test if it still behaves as expected
		dp2 := dp1.DeepCopy()
		dp2.Name = fmt.Sprintf("%s-%s", dp1.Name, "overlap")

		// 4. Check deployments propagated and not migrated

		ginkgo.By("Creating deployments", func() {
			dp1, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Create(
				ctx, dp1, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			dp2, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Create(
				ctx, dp2, metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Checking replicas evenly distributed in member clusters", func() {
			ctx, cancel := context.WithTimeout(ctx, resourcePropagationTimeout)
			defer cancel()

			util.AssertForItems(clusters, func(g gomega.Gomega, c *fedcorev1a1.FederatedCluster) {
				g.Eventually(ctx, func(g gomega.Gomega) {
					clusterDp1, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp1.Name, metav1.GetOptions{},
					)
					if err != nil {
						gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
					}
					g.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp1.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(replicasPerCluster)))
					g.Expect(clusterDp1.Status.ReadyReplicas).To(gomega.BeZero())

					clusterDp2, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp2.Name, metav1.GetOptions{},
					)
					if err != nil {
						gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
					}
					g.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp2.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(replicasPerCluster)))
					g.Expect(clusterDp2.Status.ReadyReplicas).To(gomega.BeZero())
				}).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			})
		})

		ginkgo.By("Check pods did not migrate", func() {
			gomega.Consistently(ctx, func(g gomega.Gomega) {
				util.AssertForItems(clusters, func(g gomega.Gomega, c *fedcorev1a1.FederatedCluster) {
					clusterDp1, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp1.Name, metav1.GetOptions{},
					)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp1.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(replicasPerCluster)))

					clusterDp2, err := f.ClusterKubeClient(ctx, c).AppsV1().Deployments(f.TestNamespace().Name).Get(
						ctx, dp2.Name, metav1.GetOptions{},
					)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					g.Expect(clusterDp2.Spec.Replicas).To(gomega.HaveValue(gomega.Equal(replicasPerCluster)))
				})
			}).WithTimeout(assertNoAutoMigrationDuration).Should(gomega.Succeed())
		})
	})
})
