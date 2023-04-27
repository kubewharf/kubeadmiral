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

package app

import (
	"fmt"
	"regexp"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kubewharf/kubeadmiral/cmd/controller-manager/app/options"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/federatedclient"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

// KnownControllers returns all well known controller names
func KnownControllers() []string {
	controllers := sets.StringKeySet(knownControllers)
	ftcSubControllers := sets.StringKeySet(knownFTCSubControllers)
	ret := controllers.Union(ftcSubControllers)
	return ret.List()
}

// ControllersDisabledByDefault returns all controllers that are disabled by default
func ControllersDisabledByDefault() []string {
	return controllersDisabledByDefault.UnsortedList()
}

func isControllerEnabled(name string, disabledByDefaultControllers sets.Set[string], controllers []string) bool {
	hasStar := false
	for _, ctrl := range controllers {
		if ctrl == name {
			return true
		}
		if ctrl == "-"+name {
			return false
		}
		if ctrl == "*" {
			hasStar = true
		}
	}
	// if we get here, there was no explicit choice
	if !hasStar {
		// nothing on by default
		return false
	}

	if disabledByDefaultControllers != nil {
		return !disabledByDefaultControllers.Has(name)
	}
	return true
}

func createControllerContext(opts *options.Options) (*controllercontext.Context, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags(opts.Master, opts.KubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create controller context: %w", err)
	}
	restConfig.QPS = opts.KubeAPIQPS
	restConfig.Burst = opts.KubeAPIBurst

	componentConfig, err := getComponentConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create component config: %w", err)
	}

	metrics := stats.NewMock("", "kube-federation-manager", false)

	kubeClientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube clientset: %w", err)
	}
	dynamicClientset, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic clientset: %w", err)
	}
	fedClientset, err := fedclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create fed clientset: %w", err)
	}

	informerResyncPeriod := util.NoResyncPeriod
	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClientset, informerResyncPeriod)
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClientset, informerResyncPeriod)
	fedInformerFactory := fedinformers.NewSharedInformerFactory(fedClientset, informerResyncPeriod)

	federatedClientFactory := federatedclient.NewFederatedClientsetFactory(
		fedClientset,
		kubeClientset,
		fedInformerFactory.Core().V1alpha1().FederatedClusters(),
		common.DefaultFedSystemNamespace,
		restConfig,
		opts.MaxPodListers,
		opts.EnablePodPruning,
	)

	return &controllercontext.Context{
		FedSystemNamespace: common.DefaultFedSystemNamespace,
		TargetNamespace:    metav1.NamespaceAll,

		WorkerCount:             opts.WorkerCount,
		ClusterAvailableDelay:   20 * time.Second,
		ClusterUnavailableDelay: 60 * time.Second,

		RestConfig:      restConfig,
		ComponentConfig: componentConfig,

		Metrics: metrics,

		KubeClientset:          kubeClientset,
		DynamicClientset:       dynamicClientset,
		FedClientset:           fedClientset,
		KubeInformerFactory:    kubeInformerFactory,
		DynamicInformerFactory: dynamicInformerFactory,
		FedInformerFactory:     fedInformerFactory,

		FederatedClientFactory: federatedClientFactory,
	}, nil
}

func getComponentConfig(opts *options.Options) (*controllercontext.ComponentConfig, error) {
	componentConfig := &controllercontext.ComponentConfig{
		FederatedTypeConfigCreateCRDsForFTCs: opts.CreateCRDsForFTCs,
		ClusterJoinTimeout:                   opts.ClusterJoinTimeout,
	}

	if opts.NSAutoPropExcludeRegexp != "" {
		nsAutoPropExcludeRegexp, err := regexp.Compile(opts.NSAutoPropExcludeRegexp)
		if err != nil {
			return nil, fmt.Errorf("failed to compile nsautoprop exclude regexp: %w", err)
		}
		componentConfig.NSAutoPropExcludeRegexp = nsAutoPropExcludeRegexp
	}

	return componentConfig, nil
}
