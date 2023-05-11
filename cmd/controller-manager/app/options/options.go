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
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
)

const (
	DefaultPort = 11257
)

type Options struct {
	Port int

	Controllers []string

	EnableLeaderElect          bool
	LeaderElectionResourceName string

	Master       string
	KubeConfig   string
	KubeAPIQPS   float32
	KubeAPIBurst int

	WorkerCount     int
	EnableProfiling bool
	LogFile         string
	LogVerbosity    int
	KlogVerbosity   int

	NSAutoPropExcludeRegexp string
	CreateCRDsForFTCs       bool
	ClusterJoinTimeout      time.Duration

	MaxPodListers    int64
	EnablePodPruning bool
}

func NewOptions() *Options {
	return &Options{
		WorkerCount: 1,
	}
}

//nolint:lll
func (o *Options) AddFlags(flags *pflag.FlagSet, allControllers []string, disabledByDefaultControllers []string) {
	flags.IntVar(&o.Port, "port", DefaultPort, "The port for kubeadmiral controller-manager to listen on.")

	defaultControllers := []string{"*"}
	for _, c := range disabledByDefaultControllers {
		defaultControllers = append(defaultControllers, fmt.Sprintf("-%s", c))
	}

	flags.StringSliceVar(&o.Controllers, "controllers", defaultControllers, fmt.Sprintf(""+
		"A list of controllers to enable. '*' enables all on-by-default controllers, 'foo' enables the controller named 'foo', '-foo' disables the controller named 'foo'. \nAll controllers: %s.\nEnabled-by-default controllers: %s",
		strings.Join(allControllers, ","), strings.Join(disabledByDefaultControllers, ",")))

	flags.BoolVar(
		&o.EnableLeaderElect,
		"enable-leader-elect",
		false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.",
	)
	flags.StringVar(
		&o.LeaderElectionResourceName,
		"leader-elect-resource-name",
		"federation-controller-manager",
		"The name of resource object that is used for locking during leader election.",
	)

	flags.StringVar(&o.Master, "master", "", "The address of the host Kubernetes cluster.")
	flags.StringVar(&o.KubeConfig, "kubeconfig", "", "The path of the kubeconfig for the host Kubernetes cluster.")
	flags.Float32Var(&o.KubeAPIQPS, "kube-api-qps", 500, "The maximum QPS from each Kubernetes client.")
	flags.IntVar(&o.KubeAPIBurst, "kube-api-burst", 1000, "The maximum burst for throttling requests from each Kubernetes client.")

	flags.IntVar(&o.WorkerCount, "worker-count", 1, "The number of workers to use for Kubeadmiral controllers")
	flags.BoolVar(&o.EnableProfiling, "enable-profiling", false, "Enable profiling for the controller manager.")

	flags.StringVar(
		&o.NSAutoPropExcludeRegexp,
		"ns-autoprop-exclude-regexp",
		"",
		"If non-empty, namespaces that match this go regular expression will be excluded from auto propagation.",
	)
	flags.BoolVar(&o.CreateCRDsForFTCs, "create-crds-for-ftcs", false, "Generate CRDs for federated types automatically.")
	flags.DurationVar(
		&o.ClusterJoinTimeout,
		"cluster-join-timeout",
		time.Minute*10,
		"The maximum amount of time to wait for a new cluster to join the federation before timing out.",
	)

	flags.Int64Var(&o.MaxPodListers, "max-pod-listers", 0, "The maximum number of concurrent pod listing requests to member clusters. "+
		"A non-positive number means unlimited, but may increase the instantaneous memory usage.")
	flags.BoolVar(&o.EnablePodPruning, "enable-pod-pruning", false, "Enable pod pruning for pod informer. "+
		"Enabling this can reduce memory usage of the pod informer, but will disable pod propagation.")
	o.addKlogFlags(flags)
}

func (o *Options) addKlogFlags(flags *pflag.FlagSet) {
	klogFlags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	klog.InitFlags(klogFlags)

	klogFlags.VisitAll(func(f *flag.Flag) {
		f.Name = fmt.Sprintf("klog-%s", strings.ReplaceAll(f.Name, "_", "-"))
	})
	flags.AddGoFlagSet(klogFlags)
}
