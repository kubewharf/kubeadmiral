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

package federatedclientfake

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubeclient "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/federatedclient"
)

func New(
	ctx context.Context,
	fedClient fedclient.Interface,
	kubeClient kubeclient.Interface,
	informer fedcorev1a1informers.FederatedClusterInformer,
	scheme *runtime.Scheme,
	objects map[clusterName][]runtime.Object,
) federatedclient.FederatedClientFactory {
	return federatedclient.NewFederatedClientsetFactoryWithBuilder(
		fedClient,
		kubeClient,
		informer,
		common.DefaultFedSystemNamespace,
		1,
		false,
		&mockBuilder{
			scheme:  scheme,
			objects: objects,
		},
	)
}

type clusterName string

type mockBuilder struct {
	scheme  *runtime.Scheme
	objects map[clusterName][]runtime.Object
}

func (builder *mockBuilder) Build(
	ctx context.Context,
	kubeClient kubeclient.Interface,
	cluster *fedcorev1a1.FederatedCluster,
	fedSystemNamespace string,
) (kubeclient.Interface, dynamic.Interface, error) {
	return kubefake.NewSimpleClientset(builder.objects[clusterName(cluster.Name)]...),
		dynamicfake.NewSimpleDynamicClient(builder.scheme, builder.objects[clusterName(cluster.Name)]...),
		nil
}
