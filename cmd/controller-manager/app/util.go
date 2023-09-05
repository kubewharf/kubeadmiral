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
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kubewharf/kubeadmiral/cmd/controller-manager/app/options"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	prometheusstats "github.com/kubewharf/kubeadmiral/pkg/stats/prometheus"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

// KnownControllers returns all well known controller names
func KnownControllers() []string {
	controllers := sets.StringKeySet(knownControllers)
	return controllers.List()
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

	var metrics stats.Metrics
	if opts.PrometheusMetrics {
		quantiles := map[float64]float64{}
		for qStr, eStr := range opts.PrometheusQuantiles {
			q, err := strconv.ParseFloat(qStr, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid float %q: %w", qStr, err)
			}

			e, err := strconv.ParseFloat(eStr, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid float %q: %w", eStr, err)
			}

			quantiles[q] = e
		}
		metrics = prometheusstats.New("kubeadmiral_controller_manager", opts.PrometheusAddr, opts.PrometheusPort, quantiles)
	} else {
		metrics = stats.NewMock("", "kubeadmiral_controller_manager", true)
	}

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

	informerResyncPeriod := time.Duration(0)
	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClientset, informerResyncPeriod)
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClientset, informerResyncPeriod)
	fedInformerFactory := fedinformers.NewSharedInformerFactory(fedClientset, informerResyncPeriod)

	informerManager := informermanager.NewInformerManager(
		dynamicClientset,
		fedInformerFactory.Core().V1alpha1().FederatedTypeConfigs(),
		nil,
	)
	federatedInformerManager := informermanager.NewFederatedInformerManager(
		informermanager.ClusterClientHelper{
			ConnectionHash: informermanager.DefaultClusterConnectionHash,
			RestConfigGetter: func(cluster *fedcorev1a1.FederatedCluster) (*rest.Config, error) {
				return clusterutil.BuildClusterConfig(
					cluster,
					kubeClientset,
					restConfig,
					common.DefaultFedSystemNamespace,
				)
			},
		},
		fedInformerFactory.Core().V1alpha1().FederatedTypeConfigs(),
		fedInformerFactory.Core().V1alpha1().FederatedClusters(),
	)

	return &controllercontext.Context{
		FedSystemNamespace: common.DefaultFedSystemNamespace,
		TargetNamespace:    metav1.NamespaceAll,

		WorkerCount:                  opts.WorkerCount,
		CascadingDeletionWorkerCount: opts.CascadingDeletionWorkerCount,
		ClusterAvailableDelay:        20 * time.Second,
		ClusterUnavailableDelay:      60 * time.Second,

		RestConfig:      restConfig,
		ComponentConfig: componentConfig,

		Metrics: metrics,

		KubeClientset:          kubeClientset,
		DynamicClientset:       dynamicClientset,
		FedClientset:           fedClientset,
		KubeInformerFactory:    kubeInformerFactory,
		DynamicInformerFactory: dynamicInformerFactory,
		FedInformerFactory:     fedInformerFactory,

		InformerManager:          informerManager,
		FederatedInformerManager: federatedInformerManager,
	}, nil
}

func getComponentConfig(opts *options.Options) (*controllercontext.ComponentConfig, error) {
	componentConfig := &controllercontext.ComponentConfig{
		ClusterJoinTimeout:       opts.ClusterJoinTimeout,
		MemberObjectEnqueueDelay: opts.MemberObjectEnqueueDelay,
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
