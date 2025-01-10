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

package framework

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type FrameworkOptions struct {
	CreateNamespace bool
}

type Framework interface {
	HostKubeClient() kubeclient.Interface
	HostFedClient() fedclient.Interface
	HostDynamicClient() dynamic.Interface
	HostDiscoveryClient() discovery.DiscoveryInterface
	FTCManager() informermanager.FederatedTypeConfigManager

	Name() string
	TestNamespace() *corev1.Namespace

	NewCluster(ctx context.Context, clusterModifiers ...ClusterModifier) (*fedcorev1a1.FederatedCluster, *corev1.Secret)

	ClusterKubeClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) kubeclient.Interface
	ClusterFedClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) fedclient.Interface
	ClusterDynamicClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) dynamic.Interface
}
