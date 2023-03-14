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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federatedcluster"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	clusterfwk "github.com/kubewharf/kubeadmiral/test/e2e/framework/cluster"
)

var _ = ginkgo.Describe("Cluster Join", federatedClusterTestLabels, func() {
	f := framework.NewFramework("cluster-join", framework.FrameworkOptions{
		CreateNamespace: false,
	})

	var cluster *fedcorev1a1.FederatedCluster
	var secret *corev1.Secret

	waitForClusterJoin := func(ctx context.Context) {
		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterJoined))
		}).WithTimeout(clusterJoinTimeout).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for cluster join")
	}

	assertSystemNamespaceCreated := func(ctx context.Context) {
		clusterClient := f.ClusterKubeClient(ctx, cluster)
		_, err := clusterClient.CoreV1().Namespaces().Get(ctx, framework.FedSystemNamespace, metav1.GetOptions{})
		gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)), framework.MessageUnexpectedError)
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), "System namespace not created")
	}

	ginkgo.Context("Without service account", func() {
		ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
			ginkgo.By("Creating cluster")
			cluster, secret = f.NewCluster(ctx, framework.WithTaints)

			ginkgo.By("Waiting for cluster join")
			start := time.Now()
			waitForClusterJoin(ctx)
			ginkgo.GinkgoLogr.Info("cluster join succeeded", "duration", time.Since(start))

			ginkgo.By("Assert system namespace created")
			assertSystemNamespaceCreated(ctx)

			ginkgo.By("Assert cluster secret not updated with service account information")
			secret, err := f.HostKubeClient().CoreV1().Secrets(framework.FedSystemNamespace).Get(ctx, secret.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			token, ca := getServiceAccountInfo(secret)
			gomega.Expect(token).To(gomega.BeNil())
			gomega.Expect(ca).To(gomega.BeNil())
		})
	})

	ginkgo.Context("With service account", func() {
		ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
			ginkgo.By("Creating cluster")
			cluster, secret = f.NewCluster(ctx, framework.WithServiceAccount, framework.WithTaints)

			ginkgo.By("Waiting for cluster join")
			start := time.Now()
			waitForClusterJoin(ctx)
			ginkgo.GinkgoLogr.Info("cluster join succeeded", "duration", time.Since(start))

			ginkgo.By("Assert system namespace created")
			assertSystemNamespaceCreated(ctx)

			clusterClient := f.ClusterKubeClient(ctx, cluster)

			ginkgo.By("Assert RBAC created")
			saName := federatedcluster.MemberServiceAccountName
			roleName := fmt.Sprintf("kubeadmiral-controller-manager:%s", saName)

			// 1. service account
			sa, err := clusterClient.CoreV1().ServiceAccounts(framework.FedSystemNamespace).Get(ctx, saName, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// 2. service account tokenSecret secret
			tokenSecret, err := clusterClient.CoreV1().Secrets(framework.FedSystemNamespace).Get(ctx, saName, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// 3. cluster role
			role, err := clusterClient.RbacV1().ClusterRoles().Get(ctx, roleName, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			gomega.Expect(role.Rules).
				To(gomega.ContainElements(gomega.Satisfy(hasAllResourcePermissions), gomega.Satisfy(hasNonResourceURLReadPermission)))

			// 4. cluster role binding
			binding, err := clusterClient.RbacV1().ClusterRoleBindings().Get(ctx, roleName, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			gomega.Expect(binding.Subjects).To(gomega.ContainElement(rbacv1.Subject{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      sa.Name,
				Namespace: framework.FedSystemNamespace,
			}))

			ginkgo.By("Assert cluster secret updated with service account information")
			secret, err = f.HostKubeClient().CoreV1().Secrets(framework.FedSystemNamespace).Get(ctx, secret.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			token, ca := getServiceAccountInfo(secret)
			gomega.Expect(token).To(gomega.Equal(tokenSecret.Data["token"]))
			gomega.Expect(ca).To(gomega.Equal(tokenSecret.Data["ca.crt"]))
		})

		ginkgo.It("Should timeout if cluster faulty", func(ctx ginkgo.SpecContext) {
			ginkgo.By("Creating cluster")
			cluster, _ := f.NewCluster(ctx, framework.WithServiceAccount, framework.WithInvalidEndpoint, framework.WithTaints)

			ginkgo.By("Waiting for cluster join timeout")
			start := time.Now()
			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred())
				g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterTimedOut))
			}).WithTimeout(clusterJoinTimeoutTimeout).WithContext(ctx).Should(gomega.Succeed())

			ginkgo.GinkgoLogr.Info("cluster timed out", "duration", time.Since(start))
		})
	})
})
