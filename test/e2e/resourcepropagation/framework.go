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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	controllerutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/policies"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/util"
)

var resourcePropagationTestLabel = ginkgo.Label("resource-propagation")

const (
	resourceUpdateTestAnnotationKey   = "kubeadmiral.io/e2e-update-test"
	resourceUpdateTestAnnotationValue = "1"

	defaultPollingInterval = 10 * time.Millisecond

	resourcePropagationTimeout = 30 * time.Second
	resourceStatusTimeout      = 30 * time.Second
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

type resourceStatusCollectionTestConfig struct {
	// GVR of the federatedstatus.
	gvr schema.GroupVersionResource
	// Path to a field in the resource whose value should be collected by status collection.
	path string
}

type resourcePropagationTestConfig[T k8sObject] struct {
	gvr              schema.GroupVersionResource
	statusCollection *resourceStatusCollectionTestConfig
	// Returns an object template with the given name.
	objectFactory func(name string) T
	// Given a Kubernetes client interface and namespace, returns a client that can be used to perform operations on T in the host apiserver.
	clientGetter func(client kubernetes.Interface, namespace string) resourceClient[T]
	// Checks whether the given resource is working properly in the given cluster.
	isPropagatedResourceWorking func(kubernetes.Interface, dynamic.Interface, T) (bool, error)
}

func resourcePropagationTest[T k8sObject](
	f framework.Framework,
	config *resourcePropagationTestConfig[T],
) {
	ginkgo.It("Should succeed", resourcePropagationTestLabel, func(ctx ginkgo.SpecContext) {
		var err error
		object := config.objectFactory(f.Name())
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

		ginkgo.By("Updating the source object", func() {
			patch := []map[string]interface{}{
				{
					"op": "add",
					// escape the / in annotation key
					"path":  "/metadata/annotations/" + strings.Replace(resourceUpdateTestAnnotationKey, "/", "~1", 1),
					"value": resourceUpdateTestAnnotationValue,
				},
			}
			patchBytes, err := json.Marshal(patch)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)

			object, err = hostClient.Patch(ctx, object.GetName(), types.JSONPatchType, patchBytes, metav1.PatchOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)
		})

		ginkgo.By("Waiting for update to be propagated", func() {
			ctx, cancel := context.WithTimeout(ctx, resourcePropagationTimeout)
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
						return false, err
					}
					updatePropagated := clusterObj.GetAnnotations()[resourceUpdateTestAnnotationKey] == resourceUpdateTestAnnotationValue
					return updatePropagated, nil
				},
				defaultPollingInterval,
			)

			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			gomega.Expect(failedClusters).
				To(gomega.BeEmpty(), "Timed out waiting for resource update to propagate to clusters %v", util.NameList(failedClusters))
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

		if config.statusCollection != nil {
			pathSegments := strings.Split(config.statusCollection.path, ".")
			ginkgo.By("Waiting for status collection", func() {
				gomega.Eventually(ctx, func(g gomega.Gomega) {
					actualFieldByCluster := make(map[string]any, len(clusters))
					for _, cluster := range clusters {
						clusterObject, err := config.clientGetter(
							f.ClusterKubeClient(ctx, cluster), object.GetNamespace(),
						).Get(ctx, object.GetName(), metav1.GetOptions{})
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)

						uns, err := pkgruntime.DefaultUnstructuredConverter.ToUnstructured(clusterObject)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)
						actualField, exists, err := unstructured.NestedFieldNoCopy(uns, pathSegments...)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)
						gomega.Expect(exists).To(
							gomega.BeTrue(),
							fmt.Sprintf("Cluster object does not contain specified field %q", config.statusCollection.path),
						)
						actualFieldByCluster[cluster.Name] = actualField
					}

					fedStatusUns, err := f.HostDynamicClient().Resource(config.statusCollection.gvr).Namespace(object.GetNamespace()).Get(
						ctx, object.GetName(), metav1.GetOptions{})
					if err != nil && apierrors.IsNotFound(err) {
						// status might not have been created yet, use local g to fail only this attempt
						g.Expect(err).NotTo(gomega.HaveOccurred(), "Federated status object has not been created")
					}
					gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)

					fedStatus := controllerutil.FederatedResource{}
					err = pkgruntime.DefaultUnstructuredConverter.FromUnstructured(fedStatusUns.Object, &fedStatus)
					gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)

					g.Expect(fedStatus.ClusterStatus).To(gomega.HaveLen(len(actualFieldByCluster)), "Collected status has wrong number of clusters")
					for _, clusterStatus := range fedStatus.ClusterStatus {
						actualField, exists := actualFieldByCluster[clusterStatus.ClusterName]
						g.Expect(exists).To(gomega.BeTrue(), fmt.Sprintf("collected from unexpected cluster %s", clusterStatus.ClusterName))

						collectedField, exists, err := unstructured.NestedFieldNoCopy(clusterStatus.CollectedFields, pathSegments...)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), framework.MessageUnexpectedError)
						g.Expect(exists).To(
							gomega.BeTrue(),
							fmt.Sprintf("collected fields does not contain %q for cluster %s", config.statusCollection.path, clusterStatus.ClusterName),
						)
						g.Expect(collectedField).To(gomega.Equal(actualField), "collected and actual fields differ")
					}
				}).WithTimeout(resourceStatusTimeout).WithPolling(defaultPollingInterval).Should(gomega.Succeed())
			})
		}

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
