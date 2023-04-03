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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federatedcluster"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	clusterfwk "github.com/kubewharf/kubeadmiral/test/e2e/framework/cluster"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/policies"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
)

var _ = ginkgo.Describe("Cluster Delete", federatedClusterTestLabels, func() {
	f := framework.NewFramework("cluster-delete", framework.FrameworkOptions{CreateNamespace: true})

	var cluster *fedcorev1a1.FederatedCluster
	var secret *corev1.Secret

	waitForClusterReady := func(ctx context.Context) {
		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterReady))
		}).WithTimeout(clusterReadyTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for cluster ready condition")
	}

	deleteCluster := func(ctx context.Context) {
		err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Delete(ctx, cluster.Name, metav1.DeleteOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			_, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
			gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)), framework.MessageUnexpectedError)
			g.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
		}).WithTimeout(clusterDeleteTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for cluster deletion")
	}

	assertSystemNamespaceDeleted := func(ctx context.Context) {
		clusterClient := f.ClusterKubeClient(ctx, cluster)
		ns, err := clusterClient.CoreV1().Namespaces().Get(ctx, framework.FedSystemNamespace, metav1.GetOptions{})
		if ns != nil {
			gomega.Expect(ns.DeletionTimestamp).ToNot(gomega.BeNil(), "System namespace not deleted")
		} else {
			gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound), "System namespace not deleted")
		}
	}

	assertRBACDeleted := func(ctx context.Context) {
		// note that service account and token are in the system namespace, which is already checked by assertSystemNamespaceDeleted

		roleName := fmt.Sprintf("kubeadmiral-controller-manager:%s", federatedcluster.MemberServiceAccountName)

		// 1. cluster role
		_, err := f.HostKubeClient().RbacV1().ClusterRoles().Get(ctx, roleName, metav1.GetOptions{})
		gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound), "Cluster role not deleted")

		// 2. cluster role binding
		_, err = f.HostKubeClient().RbacV1().ClusterRoleBindings().Get(ctx, roleName, metav1.GetOptions{})
		gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound), "Cluster role binding not deleted")

		// 3. service account info deleted from secret
		secret, err = f.HostKubeClient().CoreV1().Secrets(framework.FedSystemNamespace).Get(ctx, secret.Name, metav1.GetOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		token, ca := getServiceAccountInfo(secret)
		gomega.Expect(token).To(gomega.BeNil(), "Token data not removed from cluster secret")
		gomega.Expect(ca).To(gomega.BeNil(), "Token data not removed from cluster secret")
	}

	ginkgo.Context("Without cascading delete", func() {
		ginkgo.Context("Without service account", func() {
			ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, secret, _ = f.NewCluster(ctx, framework.WithCascadingDelete, framework.WithTaints)

				ginkgo.By("Waiting for cluster ready")
				waitForClusterReady(ctx)
			})

			ginkgo.It("should succeed", func(ctx ginkgo.SpecContext) {
				start := time.Now()

				ginkgo.By("Deleting cluster")
				deleteCluster(ctx)
				ginkgo.GinkgoLogr.Info("cluster deleted", "duration", time.Since(start))

				ginkgo.By("Assert system namespace deleted")
				assertSystemNamespaceDeleted(ctx)
			})
		})

		ginkgo.Context("With service account", func() {
			ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, secret, _ = f.NewCluster(ctx, framework.WithCascadingDelete, framework.WithTaints, framework.WithServiceAccount)
				waitForClusterReady(ctx)
			})

			ginkgo.It("should succeed", func(ctx ginkgo.SpecContext) {
				start := time.Now()

				ginkgo.By("Deleting cluster")
				deleteCluster(ctx)
				ginkgo.GinkgoLogr.Info("cluster deleted", "duration", time.Since(start))

				ginkgo.By("Assert system namespace deleted")
				assertSystemNamespaceDeleted(ctx)

				ginkgo.By("Assert RBAC deleted")
				assertRBACDeleted(ctx)
			})
		})
	})

	ginkgo.Context("With cascading delete", func() {
		var deploy *appsv1.Deployment
		var job *batchv1.Job
		var cronJob *batchv1b1.CronJob

		ginkgo.JustBeforeEach(func(ctx ginkgo.SpecContext) {
			ginkgo.By("Create resources for cascading delete")

			var err error

			deploy = resources.GetSimpleDeployment(f.Name())
			job = resources.GetSimpleJob(f.Name())
			cronJob = resources.GetSimpleCronJob(f.Name())

			policy := policies.PropagationPolicyForClustersWithPlacements(f.Name(), []*fedcorev1a1.FederatedCluster{cluster})
			policies.SetTolerationsForTaints(policy, cluster.Spec.Taints)
			policy, err = f.HostFedClient().
				CoreV1alpha1().
				PropagationPolicies(f.TestNamespace().Name).
				Create(ctx, policy, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			policies.SetPropagationPolicy(deploy, policy)
			policies.SetPropagationPolicy(job, policy)
			policies.SetPropagationPolicy(cronJob, policy)

			deploy, err = f.HostKubeClient().AppsV1().Deployments(f.TestNamespace().Name).Create(ctx, deploy, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			job, err = f.HostKubeClient().BatchV1().Jobs(f.TestNamespace().Name).Create(ctx, job, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			cronJob, err = f.HostKubeClient().BatchV1beta1().CronJobs(f.TestNamespace().Name).Create(ctx, cronJob, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// wait for resources to be propagated
			clusterClient := f.ClusterKubeClient(ctx, cluster)

			ginkgo.By("Waiting for resources to be propagated")
			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				_, err := clusterClient.AppsV1().Deployments(f.TestNamespace().Name).Get(ctx, deploy.Name, metav1.GetOptions{})
				gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
				g.Expect(err).ToNot(gomega.HaveOccurred())

				_, err = clusterClient.BatchV1().Jobs(f.TestNamespace().Name).Get(ctx, job.Name, metav1.GetOptions{})
				gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
				g.Expect(err).ToNot(gomega.HaveOccurred())

				_, err = clusterClient.BatchV1beta1().CronJobs(f.TestNamespace().Name).Get(ctx, cronJob.Name, metav1.GetOptions{})
				gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
				g.Expect(err).ToNot(gomega.HaveOccurred())
			}).WithTimeout(resourcePropagationTimeout).WithContext(ctx).Should(gomega.Succeed())
		})

		assertCascadingDelete := func(ctx context.Context) {
			clusterClient := f.ClusterKubeClient(ctx, cluster)

			_, err := clusterClient.AppsV1().Deployments(f.TestNamespace().Name).Get(ctx, deploy.Name, metav1.GetOptions{})
			gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))

			_, err = clusterClient.BatchV1().Jobs(f.TestNamespace().Name).Get(ctx, job.Name, metav1.GetOptions{})
			gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))

			_, err = clusterClient.BatchV1beta1().CronJobs(f.TestNamespace().Name).Get(ctx, cronJob.Name, metav1.GetOptions{})
			gomega.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
		}

		ginkgo.Context("Without service account", func() {
			ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, secret, _ = f.NewCluster(ctx, framework.WithCascadingDelete, framework.WithTaints)

				ginkgo.By("Waiting for cluster ready")
				waitForClusterReady(ctx)
			})

			ginkgo.It("should succeed", func(ctx ginkgo.SpecContext) {
				start := time.Now()

				ginkgo.By("Deleting cluster")
				deleteCluster(ctx)
				ginkgo.GinkgoLogr.Info("cluster deleted", "duration", time.Since(start))

				ginkgo.By("Assert system namespace deleted")
				assertSystemNamespaceDeleted(ctx)

				ginkgo.By("Assert cascading delete successful")
				assertCascadingDelete(ctx)
			})
		})

		ginkgo.Context("With service account", func() {
			ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
				ginkgo.By("Creating cluster")
				cluster, secret, _ = f.NewCluster(ctx, framework.WithCascadingDelete, framework.WithTaints, framework.WithServiceAccount)

				ginkgo.By("Waiting for cluster ready")
				waitForClusterReady(ctx)
			})

			ginkgo.It("should succeed", func(ctx ginkgo.SpecContext) {
				start := time.Now()

				ginkgo.By("Deleting cluster")
				deleteCluster(ctx)
				ginkgo.GinkgoLogr.Info("cluster deleted", "duration", time.Since(start))

				ginkgo.By("Assert system namespace deleted")
				assertSystemNamespaceDeleted(ctx)

				ginkgo.By("Assert cascading delete successful")
				assertCascadingDelete(ctx)
			})
		})
	})
})
