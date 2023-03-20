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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/policies"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/util"
)

var (
	resourcePropagationTestLabel = ginkgo.Label("resource-propagation")

	defaultPollingInterval = 10 * time.Millisecond

	resourcePropagationTimeout = 30 * time.Second
	resourceReadyTimeout       = 2 * time.Minute
	resourceDeleteTimeout      = 30 * time.Second
)

type k8sObject interface {
	metav1.Object
	pkgruntime.Object
}

type resourceClient[T k8sObject] interface {
	Create(ctx context.Context, object T, opts metav1.CreateOptions) (T, error)
	Update(ctx context.Context, object T, opts metav1.UpdateOptions) (T, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	Get(ctx context.Context, name string, opts metav1.GetOptions) (T, error)
	Patch(
		ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string,
	) (result T, err error)
}

type resourcePropagationTestConfig[T k8sObject] struct {
	gvr                         schema.GroupVersionResource
	objectFactory               func(name string) T
	clientGetter                func(client kubernetes.Interface, namespace string) resourceClient[T]
	isPropagatedResourceWorking func(kubernetes.Interface, dynamic.Interface, T) (bool, error)
}

func resourcePropagationTest[T k8sObject](
	f framework.Framework,
	config *resourcePropagationTestConfig[T],
) {
	ginkgo.It("Should succeed", func(ctx ginkgo.SpecContext) {
		var err error
		object := config.objectFactory(f.Name())

		ginkgo.GinkgoLogr.WithValues(
			"gvr", config.gvr.String(),
			"namespace", f.TestNamespace().Name,
			"name", object.GetName(),
		)
		hostClient := config.clientGetter(f.HostKubeClient(), f.TestNamespace().Name)

		var clusters []*fedcorev1a1.FederatedCluster
		ginkgo.By("Getting clusters", func() {
			clusterList, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().List(ctx, metav1.ListOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

			clusters = make([]*fedcorev1a1.FederatedCluster, len(clusterList.Items))
			for i := range clusterList.Items {
				clusters[i] = &clusterList.Items[i]
			}
			clusters = util.FilterOutE2ETestObjects(clusters)
		})

		ginkgo.By("Creating PropagationPolicy", func() {
			policy := policies.PropagationPolicyForClustersWithPlacements(f.Name(), clusters)

			_, err = f.HostFedClient().CoreV1alpha1().PropagationPolicies(f.TestNamespace().Name).Create(
				ctx,
				policy,
				metav1.CreateOptions{},
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)

			policies.SetPropagationPolicy(object, policy)
		})

		ginkgo.By("Creating resource", func() {
			object, err = hostClient.Create(ctx, object, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Waiting for resource propagation", func() {
			ctx, cancel := context.WithTimeout(ctx, resourcePropagationTimeout)
			defer cancel()

			failedClusters, err := util.PollUntilForItems(
				ctx,
				clusters,
				func(c *fedcorev1a1.FederatedCluster) (bool, error) {
					_, err := config.clientGetter(
						f.ClusterKubeClient(ctx, c),
						object.GetNamespace(),
					).Get(ctx, object.GetName(), metav1.GetOptions{})
					if err != nil && apierrors.IsNotFound(err) {
						return false, nil
					}
					return true, err
				},
				defaultPollingInterval,
			)

			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			gomega.Expect(failedClusters).
				To(gomega.BeEmpty(), "Timed out waiting for resource to propagate to clusters %v", util.NameList(failedClusters))
		})

		ginkgo.By("Waiting for propagated resource to work correctly", func() {
			ctx, cancel := context.WithTimeout(ctx, resourceReadyTimeout)
			defer cancel()

			failedClusters, err := util.PollUntilForItems(
				ctx,
				clusters,
				func(c *fedcorev1a1.FederatedCluster) (bool, error) {
					clusterObj, err := config.clientGetter(
						f.ClusterKubeClient(ctx, c),
						object.GetNamespace(),
					).Get(ctx, object.GetName(), metav1.GetOptions{})
					if err != nil {
						return true, err
					}
					return config.isPropagatedResourceWorking(
						f.ClusterKubeClient(ctx, c),
						f.ClusterDynamicClient(ctx, c),
						clusterObj,
					)
				},
				defaultPollingInterval,
			)

			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			gomega.Expect(failedClusters).
				To(gomega.BeEmpty(), "Timed out waiting for resource to be working in clusters %v", failedClusters)
		})

		ginkgo.By("Deleting source object", func() {
			err = hostClient.Delete(ctx, object.GetName(), metav1.DeleteOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Waiting for resource deletion", func() {
			ctx, cancel := context.WithTimeout(ctx, resourceDeleteTimeout)
			defer cancel()

			failedClusters, err := util.PollUntilForItems(
				ctx,
				clusters,
				func(c *fedcorev1a1.FederatedCluster) (bool, error) {
					_, err := config.clientGetter(
						f.ClusterKubeClient(ctx, c),
						object.GetNamespace(),
					).Get(ctx, object.GetName(), metav1.GetOptions{})
					if err != nil && !apierrors.IsNotFound(err) {
						return true, err
					}
					return apierrors.IsNotFound(err), nil
				},
				defaultPollingInterval,
			)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			gomega.Expect(failedClusters).
				To(gomega.BeEmpty(), "Timed out waiting for resource to be deleted in clusters %v", failedClusters)

			gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
				_, err := hostClient.Get(ctx, object.GetName(), metav1.GetOptions{})
				gomega.Expect(err).To(gomega.Or(gomega.BeNil(), gomega.Satisfy(apierrors.IsNotFound)))
				g.Expect(err).To(gomega.Satisfy(apierrors.IsNotFound))
			}).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for source object deletion")
		})
	})
}
