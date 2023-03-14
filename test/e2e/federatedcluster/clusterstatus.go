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

package federatedcluster

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	clusterfwk "github.com/kubewharf/kubeadmiral/test/e2e/framework/cluster"
)

var _ = ginkgo.Describe("Cluster Status", federatedClusterTestLabels, func() {
	f := framework.NewFramework("cluster-status", framework.FrameworkOptions{
		CreateNamespace: false,
	})

	var cluster *fedcorev1a1.FederatedCluster

	waitForClusterJoin := func(ctx context.Context) {
		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterJoined))
		}).WithTimeout(clusterJoinTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for cluster join")
	}

	waitForFirstStatusUpdate := func(ctx context.Context) {
		// check initial status update
		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			g.Expect(cluster).To(gomega.Satisfy(statusCollected))
		}).WithTimeout(clusterStatusCollectTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for first status update")
	}

	ginkgo.Context("Healthy cluster", func() {
		waitForStatusConvergence := func(ctx context.Context) {
			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				var err error
				cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
				g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterReachable))
				g.Expect(cluster.Status.APIResourceTypes).ToNot(gomega.BeEmpty())

				clusterResources := cluster.Status.Resources
				g.Expect(clusterResources.SchedulableNodes).ToNot(gomega.BeNil())
				g.Expect(*clusterResources.SchedulableNodes).To(gomega.BeEquivalentTo(1))
				g.Expect(clusterResources.Allocatable.Cpu()).ToNot(gomega.BeNil())
				g.Expect(clusterResources.Allocatable.Memory()).ToNot(gomega.BeNil())
				g.Expect(clusterResources.Available.Cpu()).ToNot(gomega.BeNil())
				g.Expect(clusterResources.Available.Memory()).ToNot(gomega.BeNil())
			}).WithTimeout(clusterStatusUpdateInterval*2).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for status convergence")
		}

		ginkgo.Context("Without service account", func() {
			ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, _ = f.NewCluster(ctx, framework.WithTaints)

				start := time.Now()

				ginkgo.By("Waiting for cluster join")
				waitForClusterJoin(ctx)

				ginkgo.By("Waiting for initial status update")
				waitForFirstStatusUpdate(ctx)
				ginkgo.GinkgoLogr.Info("initial cluster status collected", "duration", time.Since(start))

				ginkgo.By("Waiting for status to converge to expected state")
				waitForStatusConvergence(ctx)
				ginkgo.GinkgoLogr.Info("cluster status converged", "duration", time.Since(start))
			})
		})

		ginkgo.Context("With service account", func() {
			ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, _ = f.NewCluster(ctx, framework.WithTaints, framework.WithServiceAccount)

				start := time.Now()

				ginkgo.By("Waiting for cluster join")
				waitForClusterJoin(ctx)

				ginkgo.By("Waiting for initial status update")
				waitForFirstStatusUpdate(ctx)
				ginkgo.GinkgoLogr.Info("initial cluster status collected", "duration", time.Since(start))

				ginkgo.By("Waiting for status to converge to expected state")
				waitForStatusConvergence(ctx)
				ginkgo.GinkgoLogr.Info("cluster status converged", "duration", time.Since(start))
			})
		})
	})

	ginkgo.Context("Unhealthy cluster", func() {
		waitForStatusConvergence := func(ctx context.Context) {
			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				var err error
				cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
				g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterUnreachable))
			}).WithTimeout(clusterStatusUpdateInterval*2).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for status convergence")
		}

		makeClusterUnreachable := func(ctx context.Context) {
			_, err := f.HostFedClient().
				CoreV1alpha1().
				FederatedClusters().
				Patch(ctx, cluster.Name, types.JSONPatchType, invalidClusterEndpointPatchData, metav1.PatchOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		}

		ginkgo.Context("Without service account", func() {
			ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, _ = f.NewCluster(ctx, framework.WithTaints)

				start := time.Now()

				ginkgo.By("Waiting for cluster join")
				waitForClusterJoin(ctx)

				ginkgo.By("Waiting for initial status update")
				waitForFirstStatusUpdate(ctx)
				ginkgo.GinkgoLogr.Info("initial cluster status collected", "duration", time.Since(start))

				ginkgo.By("Making cluster unreachable")
				makeClusterUnreachable(ctx)

				ginkgo.By("Waiting for status to converge to expected state")
				waitForStatusConvergence(ctx)
				ginkgo.GinkgoLogr.Info("cluster status converged", "duration", time.Since(start))
			})
		})

		ginkgo.Context("With service account", func() {
			ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, _ = f.NewCluster(ctx, framework.WithTaints, framework.WithServiceAccount)

				start := time.Now()

				ginkgo.By("Waiting for cluster join")
				waitForClusterJoin(ctx)

				ginkgo.By("Waiting for initial status update")
				waitForFirstStatusUpdate(ctx)
				ginkgo.GinkgoLogr.Info("initial cluster status collected", "duration", time.Since(start))

				ginkgo.By("Making cluster unhealthy")
				makeClusterUnreachable(ctx)

				ginkgo.By("Waiting for status to converge to expected state")
				waitForStatusConvergence(ctx)
				ginkgo.GinkgoLogr.Info("cluster status converged", "duration", time.Since(start))
			})
		})
	})
})
