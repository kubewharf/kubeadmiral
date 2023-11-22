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

package options

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/features"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/filters"
	"k8s.io/apiserver/pkg/server/healthz"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	dynamicclient "k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	restfulcommon "k8s.io/kube-openapi/pkg/common"
	netutils "k8s.io/utils/net"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/apis/hpaaggregator"
	hpaaggregatorv1alpha1 "github.com/kubewharf/kubeadmiral/pkg/apis/hpaaggregator/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	fedopenapi "github.com/kubewharf/kubeadmiral/pkg/client/openapi"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	apiserver "github.com/kubewharf/kubeadmiral/pkg/hpaaggregatorapiserver"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

const (
	defaultEtcdPathPrefix = "/hpa-aggregator"
	openAPITitle          = "KubeAdmiral-HPA-Aggregator"
)

type Options struct {
	RecommendedOptions *genericoptions.RecommendedOptions

	Master       string
	KubeAPIQPS   float32
	KubeAPIBurst int

	MaxPodListers    int64
	EnablePodPruning bool

	DisableResourceMetrics bool
	DisableCustomMetrics   bool
	DiscoveryInterval      time.Duration
}

func NewOptions() *Options {
	o := &Options{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			apiserver.Codecs.LegacyCodec(hpaaggregatorv1alpha1.SchemeGroupVersion),
		),
	}
	o.RecommendedOptions.Etcd.StorageConfig.EncodeVersioner = runtime.NewMultiGroupVersioner(
		hpaaggregatorv1alpha1.SchemeGroupVersion,
		schema.GroupKind{Group: hpaaggregator.GroupName},
	)
	// we don't use it now
	o.RecommendedOptions.Etcd.SkipHealthEndpoints = true
	return o
}

//nolint:lll
func (o *Options) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&o.Master, "master", "",
		"The address of the host Kubernetes cluster.")
	flags.Float32Var(&o.KubeAPIQPS, "kube-api-qps", 500,
		"The maximum QPS from each Kubernetes client.")
	flags.IntVar(&o.KubeAPIBurst, "kube-api-burst", 1000,
		"The maximum burst for throttling requests from each Kubernetes client.")

	//flags.Int64Var(&o.MaxPodListers, "max-pod-listers", 0, "The maximum number of concurrent pod listing requests to member clusters. "+
	//	"A non-positive number means unlimited, but may increase the instantaneous memory usage.")
	//flags.BoolVar(&o.EnablePodPruning, "enable-pod-pruning", false, "Enable pod pruning for pod informer. "+
	//	"Enabling this can reduce memory usage of the pod informer, but will disable pod propagation.")

	flags.BoolVar(&o.DisableResourceMetrics, "disable-resource-metrics", false,
		"Whether to disable resource metrics provider")
	flags.BoolVar(&o.DisableCustomMetrics, "disable-custom-metrics", false,
		"Whether to disable custom metrics provider")
	flags.DurationVar(&o.DiscoveryInterval, "discovery-interval", o.DiscoveryInterval,
		"Interval at which to refresh custom-metrics API discovery information from member clusters")

	utilfeature.DefaultMutableFeatureGate.AddFlag(flags)

	o.RecommendedOptions.AddFlags(flags)
	o.addKlogFlags(flags)
}

// Validate validates Options
func (o *Options) Validate() error {
	var errors []error
	errors = append(errors, o.RecommendedOptions.Validate()...)
	return utilerrors.NewAggregate(errors)
}

// Complete fills in fields required to have valid data
func (o *Options) Complete() error {
	// TODO: register admission plugins and add it to o.RecommendedOptions.Admission.RecommendedPluginOrder
	return nil
}

// Config returns config for the api server given Options
func (o *Options) Config() (*apiserver.Config, error) {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts(
		"localhost",
		nil,
		[]net.IP{netutils.ParseIPSloppy("127.0.0.1")},
	); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	o.RecommendedOptions.ExtraAdmissionInitializers = func(c *genericapiserver.RecommendedConfig) ([]admission.PluginInitializer, error) {
		return []admission.PluginInitializer{}, nil
	}

	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(
		fedopenapi.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(apiserver.Scheme),
	)
	serverConfig.OpenAPIConfig.Info.Title = openAPITitle
	serverConfig.OpenAPIConfig.GetOperationIDAndTagsFromRoute = func(r restfulcommon.Route) (string, []string, error) {
		return r.OperationName() + r.Path(), nil, nil
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.OpenAPIV3) {
		serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
			fedopenapi.GetOpenAPIDefinitions,
			openapi.NewDefinitionNamer(apiserver.Scheme),
		)
		serverConfig.OpenAPIV3Config.Info.Title = openAPITitle
		serverConfig.OpenAPIV3Config.GetOperationIDAndTagsFromRoute = func(r restfulcommon.Route) (string, []string, error) {
			return r.OperationName() + r.Path(), nil, nil
		}
	}

	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	serverConfig.LongRunningFunc = filters.BasicLongRunningRequestCheck(
		sets.NewString("watch", "proxy"),
		sets.NewString("attach", "exec", "proxy", "log", "portforward"),
	)

	restConfig, err := clientcmd.BuildConfigFromFlags(o.Master, o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create rest config: %w", err)
	}
	restConfig.QPS = o.KubeAPIQPS
	restConfig.Burst = o.KubeAPIBurst

	kubeClientset, err := kubeclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube clientset: %w", err)
	}
	dynamicClientset, err := dynamicclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic clientset: %w", err)
	}
	fedClientset, err := fedclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create fed clientset: %w", err)
	}

	informerResyncPeriod := time.Duration(0)
	fedInformerFactory := fedinformers.NewSharedInformerFactory(fedClientset, informerResyncPeriod)

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

	serverConfig.AddReadyzChecks(
		healthz.NamedCheck("federated-informer-manager-sync", func(_ *http.Request) error {
			if !federatedInformerManager.HasSynced() {
				return errors.New("federated informer manager has not yet synchronized")
			}
			return nil
		}),
	)

	config := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig: &apiserver.ExtraConfig{
			KubeClientset:            kubeClientset,
			DynamicClientset:         dynamicClientset,
			FedClientset:             fedClientset,
			FedInformerFactory:       fedInformerFactory,
			FederatedInformerManager: federatedInformerManager,
			RestConfig:               restConfig,
			DisableResourceMetrics:   o.DisableResourceMetrics,
			DisableCustomMetrics:     o.DisableCustomMetrics,
			DiscoveryInterval:        o.DiscoveryInterval,
		},
	}
	return config, nil
}

func (o *Options) addKlogFlags(flags *pflag.FlagSet) {
	klogFlags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	klog.InitFlags(klogFlags)

	klogFlags.VisitAll(func(f *flag.Flag) {
		f.Name = fmt.Sprintf("klog-%s", strings.ReplaceAll(f.Name, "_", "-"))
	})
	flags.AddGoFlagSet(klogFlags)
}
