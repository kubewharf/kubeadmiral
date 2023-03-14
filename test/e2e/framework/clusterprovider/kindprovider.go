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

package clusterprovider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	kindv1a4 "sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	kindcluster "sigs.k8s.io/kind/pkg/cluster"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type kindClusterProvider struct {
	provider *kindcluster.Provider

	kubeConfig         string
	kindNodeImage      string
	clusterWaitTimeout time.Duration

	clusters sync.Map
}

func NewKindClusterProvider(kubeConfig, nodeImage string, clusterWaitTimeout time.Duration) ClusterProvider {
	return &kindClusterProvider{
		provider:           kindcluster.NewProvider(),
		kubeConfig:         kubeConfig,
		kindNodeImage:      nodeImage,
		clusterWaitTimeout: clusterWaitTimeout,
		clusters:           sync.Map{},
	}
}

var _ ClusterProvider = &kindClusterProvider{}

func (c *kindClusterProvider) NewCluster(_ context.Context, name string) (*fedcorev1a1.FederatedCluster, *corev1.Secret) {
	clusterName := name
	secretName := fmt.Sprintf("%s-secret", clusterName)

	err := c.provider.Create(
		clusterName,
		kindcluster.CreateWithKubeconfigPath(c.kubeConfig),
		kindcluster.CreateWithWaitForReady(c.clusterWaitTimeout),
		kindcluster.CreateWithV1Alpha4Config(&kindv1a4.Cluster{
			Name: clusterName,
			Nodes: []kindv1a4.Node{
				{
					Role:  kindv1a4.ControlPlaneRole,
					Image: c.kindNodeImage,
				},
			},
		}),
	)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	kubeconfig, err := c.provider.KubeConfig(clusterName, false)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	c.clusters.Store(clusterName, nil)

	config, err := clientcmd.Load([]byte(kubeconfig))
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	ctx := config.Contexts[config.CurrentContext]
	clusterConfig := config.Clusters[ctx.Cluster]
	userConfig := config.AuthInfos[ctx.AuthInfo]
	gomega.Expect(clusterConfig).ToNot(gomega.BeNil())
	gomega.Expect(userConfig).ToNot(gomega.BeNil())

	cluster := &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Spec: fedcorev1a1.FederatedClusterSpec{
			APIEndpoint: clusterConfig.Server,
			SecretRef: fedcorev1a1.LocalSecretReference{
				Name: secretName,
			},
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Data: map[string][]byte{
			common.ClusterCertificateAuthorityKey: clusterConfig.CertificateAuthorityData,
			common.ClusterClientCertificateKey:    userConfig.ClientCertificateData,
			common.ClusterClientKeyKey:            userConfig.ClientKeyData,
		},
	}

	return cluster, secret
}

func (c *kindClusterProvider) StopCluster(_ context.Context, name string) {
	if _, ok := c.clusters.Load(name); ok {
		err := c.provider.Delete(name, c.kubeConfig)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		c.clusters.Delete(name)
	}
}

func (c *kindClusterProvider) StopAllClusters(_ context.Context) {
	c.clusters.Range(func(cluster, _ any) bool {
		err := c.provider.Delete(cluster.(string), c.kubeConfig)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		return true
	})
}
