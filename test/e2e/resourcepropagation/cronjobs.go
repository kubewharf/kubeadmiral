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

package resourcepropagation

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	batchv1b1 "k8s.io/api/batch/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/policies"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/util"
)

var _ = ginkgo.Describe("CronJob Propagation", resourcePropagationTestLabel, func() {
	f := framework.NewFramework("cronjob-propagation", framework.FrameworkOptions{CreateNamespace: true})

	var cronJob *batchv1b1.CronJob
	var clusters []*fedcorev1a1.FederatedCluster

	ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
		cronJob = resources.GetSimpleCronJob(f.Name())
		clusterList, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().List(ctx, metav1.ListOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

		clusters = make([]*fedcorev1a1.FederatedCluster, len(clusterList.Items))
		for i := range clusterList.Items {
			clusters[i] = &clusterList.Items[i]
		}
		clusters = util.FilterOutE2ETestObjects(clusters)

		policy := policies.PropagationPolicyForClustersWithPlacements(f.Name(), clusters)
		policies.SetPropagationPolicy(cronJob, policy)

		_, err = f.HostFedClient().CoreV1alpha1().PropagationPolicies(f.TestNamespace().Name).Create(
			ctx,
			policy,
			metav1.CreateOptions{},
		)
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
	})

	assertCronJobsPropagated := func(ctx context.Context) {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, resourcePropagationTimeout)
		defer cancel()

		failedClusters, err := util.PollUntilForItems(
			ctxWithTimeout,
			clusters,
			func(c *fedcorev1a1.FederatedCluster) (bool, error) {
				_, err := f.ClusterKubeClient(ctx, c).BatchV1beta1().CronJobs(f.TestNamespace().Name).Get(
					ctx,
					cronJob.Name,
					metav1.GetOptions{},
				)
				if err != nil && apierrors.IsNotFound(err) {
					return false, nil
				}
				return true, err
			},
			defaultPollingInterval,
		)

		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		gomega.Expect(failedClusters).
			To(gomega.BeEmpty(), "Timed out waiting for cronjob to propagate to clusters %v", util.NameList(failedClusters))
	}

	assertCronJobsRunOnce := func(ctx context.Context) {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, resourceReadyTimeout)
		defer cancel()

		failedClusters, err := util.PollUntilForItems(
			ctxWithTimeout,
			clusters,
			func(c *fedcorev1a1.FederatedCluster) (bool, error) {
				clusterJob, err := f.ClusterKubeClient(ctx, c).BatchV1beta1().CronJobs(f.TestNamespace().Name).Get(
					ctx,
					cronJob.Name,
					metav1.GetOptions{},
				)
				if err != nil {
					return true, err
				}
				return resources.IsCronJobScheduledOnce(clusterJob), nil
			},
			defaultPollingInterval,
		)

		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		gomega.Expect(failedClusters).
			To(gomega.BeEmpty(), "Timed out waiting for cronjob %s to be run once in clusters %v", cronJob.Name, failedClusters)
	}

	assertCronJobsDeleted := func(ctx context.Context) {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, resourceDeleteTimeout)
		defer cancel()

		failedClusters, err := util.PollUntilForItems(
			ctxWithTimeout,
			clusters,
			func(c *fedcorev1a1.FederatedCluster) (bool, error) {
				_, err := f.ClusterKubeClient(ctx, c).BatchV1beta1().CronJobs(f.TestNamespace().Name).Get(
					ctx,
					cronJob.Name,
					metav1.GetOptions{},
				)
				if err != nil && !apierrors.IsNotFound(err) {
					return true, err
				}
				return apierrors.IsNotFound(err), nil
			},
			defaultPollingInterval,
		)
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		gomega.Expect(failedClusters).
			To(gomega.BeEmpty(), "Timed out waiting for job to be deleted in clusters %v", failedClusters)

		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			_, err := f.HostKubeClient().BatchV1beta1().CronJobs(f.TestNamespace().Name).Get(ctx, cronJob.Name, metav1.GetOptions{})
			gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
			g.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
		}).WithContext(ctxWithTimeout).Should(gomega.Succeed(), "Timed out waiting for source object deletion")
	}

	ginkgo.It("should succeed", func(ctx ginkgo.SpecContext) {
		var err error

		ginkgo.By("Creating cronjob")
		cronJob, err = f.HostKubeClient().BatchV1beta1().CronJobs(f.TestNamespace().Name).Create(ctx, cronJob, metav1.CreateOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		ginkgo.GinkgoLogr.Info("created cronJob for propagation", "cronjob-name", cronJob.Name, "cronjob-namespace", f.TestNamespace().Name)

		start := time.Now()

		ginkgo.By("Waiting for cronjob propagation")
		assertCronJobsPropagated(ctx)
		ginkgo.GinkgoLogr.Info("all cronjobs propagated", "duration", time.Since(start))

		ginkgo.By("Waiting for cronjob to be scheduled once")
		assertCronJobsRunOnce(ctx)
		ginkgo.GinkgoLogr.Info("all cronjobs run once", "duration", time.Since(start))

		err = f.HostKubeClient().BatchV1beta1().CronJobs(f.TestNamespace().Name).Delete(ctx, cronJob.Name, metav1.DeleteOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		ginkgo.GinkgoLogr.Info("deleted source cronjob object")

		start = time.Now()
		ginkgo.By("Waiting for cronjob deletion")
		assertCronJobsDeleted(ctx)
		ginkgo.GinkgoLogr.Info("all cronjobs deleted", "duration", time.Since(start))
	})
})
