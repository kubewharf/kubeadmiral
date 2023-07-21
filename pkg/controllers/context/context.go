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

package context

import (
	"context"
	"regexp"
	"time"

	dynamicclient "k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformer "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type Context struct {
	FedSystemNamespace string
	TargetNamespace    string

	WorkerCount             int
	ClusterAvailableDelay   time.Duration
	ClusterUnavailableDelay time.Duration

	RestConfig      *rest.Config
	ComponentConfig *ComponentConfig

	Metrics stats.Metrics

	KubeClientset          kubeclient.Interface
	DynamicClientset       dynamicclient.Interface
	FedClientset           fedclient.Interface
	KubeInformerFactory    kubeinformer.SharedInformerFactory
	DynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory
	FedInformerFactory     fedinformers.SharedInformerFactory

	InformerManager          informermanager.InformerManager
	FederatedInformerManager informermanager.FederatedInformerManager
}

func (c *Context) StartFactories(ctx context.Context) {
	if c.KubeInformerFactory != nil {
		c.KubeInformerFactory.Start(ctx.Done())
	}
	if c.DynamicInformerFactory != nil {
		c.DynamicInformerFactory.Start(ctx.Done())
	}
	if c.FedInformerFactory != nil {
		c.FedInformerFactory.Start(ctx.Done())
	}

	if c.InformerManager != nil {
		c.InformerManager.Start(ctx)
	}
	if c.FederatedInformerManager != nil {
		c.FederatedInformerManager.Start(ctx)
	}
}

type ComponentConfig struct {
	NSAutoPropExcludeRegexp  *regexp.Regexp
	ClusterJoinTimeout       time.Duration
	MemberObjectEnqueueDelay time.Duration
}
