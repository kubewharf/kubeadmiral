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

package example

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	clusterfwk "github.com/kubewharf/kubeadmiral/test/e2e/framework/cluster"
)

var _ = ginkgo.Describe("Example", ginkgo.Label(framework.TestLabelNeedCreateCluster), func() {
	f := framework.NewFramework("example", framework.FrameworkOptions{
		CreateNamespace: false,
	})

	var cluster *fedcorev1a1.FederatedCluster

	waitForClusterJoin := func(ctx context.Context) {
		gomega.Eventually(func(g gomega.Gomega, ctx context.Context) {
			cluster, err := f.HostFedClient().CoreV1alpha1().FederatedClusters().Get(ctx, cluster.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), framework.MessageUnexpectedError)
			g.Expect(cluster).To(gomega.Satisfy(clusterfwk.ClusterJoined))
		}).WithTimeout(time.Second*10).WithContext(ctx).Should(gomega.Succeed(), "Timed out waiting for cluster join")
	}

	ginkgo.It("create cluster", func(ctx ginkgo.SpecContext) {
		cluster, _, _ = f.NewCluster(ctx)
		ginkgo.GinkgoWriter.Printf("Cluster: %+v\n", *cluster)

		waitForClusterJoin(ctx)
	})
})
