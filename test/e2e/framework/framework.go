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
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/clusterprovider"
)

const (
	KwokClusterProvider = "kwok"
	KindClusterProvider = "kind"
)

var (
	// TODO: make this configurable through flags
	FedSystemNamespace string = common.DefaultFedSystemNamespace

	// variables used to connect to the host cluster

	master       string
	kubeconfig   string
	kubeAPIQPS   float64
	kubeAPIBurst int

	// variables used by cluster providers

	clusterProvider           string
	kindNodeImage             string
	kwokImagePrefix           string
	kwokKubeVersion           string
	kubeConfigForTestClusters string
	kwokWorkDir               string

	// variables used for cleanup

	preserveClusters  bool
	preserveNamespace bool

	// global variables used by the framework itself

	hostKubeClient        kubeclient.Interface
	hostFedClient         fedclient.Interface
	hostDynamicClient     dynamic.Interface
	clusterKubeClients    sync.Map
	clusterFedClients     sync.Map
	clusterDynamicClients sync.Map
	clusterProviderImpl   clusterprovider.ClusterProvider
)

func init() {
	flag.StringVar(&master, "master", "", "The address of the host Kubernetes cluster.")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "The path of the kubeconfig for the host Kubernetes cluster.")
	flag.Float64Var(&kubeAPIQPS, "kube-api-qps", 500, "The maximum QPS from each Kubernetes client.")
	flag.IntVar(&kubeAPIBurst, "kube-api-burst", 1000, "The maximum burst for throttling requests from each Kubernetes client.")

	flag.StringVar(&clusterProvider, "cluster-provider", "kwok", "The cluster provider [kwok,kind] to use.")
	flag.StringVar(
		&kindNodeImage,
		"kind-node-image",
		"kindest/node:v1.20.15@sha256:a32bf55309294120616886b5338f95dd98a2f7231519c7dedcec32ba29699394",
		"The node image to use for creating kind test clusters, it should include the image digest.",
	)
	flag.StringVar(&kwokImagePrefix, "kwok-image-prefix", "registry.k8s.io", "The image prefix used by kwok to pull kubernetes images.")
	flag.StringVar(&kwokKubeVersion, "kwok-kube-version", "v1.20.15", "The kubernetes version to be used for kwok member clusters")

	flag.BoolVar(&preserveClusters, "preserve-clusters", false, "If set, clusters created during testing are preserved")
	flag.BoolVar(&preserveNamespace, "preserve-namespaces", false, "If set, namespaces created during testing are preserved")
}

var _ = ginkgo.SynchronizedBeforeSuite(
	func() []byte {
		tempKubeConfig, err := os.CreateTemp(os.TempDir(), "")
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		ginkgo.GinkgoLogr.Info("temporary kubeconfig created", "file", tempKubeConfig.Name())

		kwokWorkDir := filepath.Join(os.TempDir(), rand.String(12))
		err = os.Mkdir(kwokWorkDir, fs.ModePerm)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		ginkgo.GinkgoLogr.Info("temporary kwok workdir created", "dir", kwokWorkDir)

		bytes, err := json.Marshal([]string{tempKubeConfig.Name(), kwokWorkDir})
		gomega.Expect(err).ToNot(gomega.HaveOccurred())

		return bytes
	},
	func(data []byte) {
		params := []string{}
		err := json.Unmarshal(data, &params)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())

		kubeConfigForTestClusters = params[0]
		kwokWorkDir = params[1]

		rand.Seed(ginkgo.GinkgoRandomSeed() + int64(ginkgo.GinkgoParallelProcess()))

		restConfig, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		hostKubeClient, err = kubeclient.NewForConfig(restConfig)
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		hostFedClient, err = fedclient.NewForConfig(restConfig)
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		hostDynamicClient, err = dynamic.NewForConfig(restConfig)
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		clusterKubeClients = sync.Map{}
		clusterFedClients = sync.Map{}
		clusterDynamicClients = sync.Map{}

		switch clusterProvider {
		case KwokClusterProvider:
			clusterProviderImpl = clusterprovider.NewKwokClusterProvider(
				kubeConfigForTestClusters,
				kwokWorkDir,
				kwokImagePrefix,
				kwokKubeVersion,
				defaultClusterWaitTimeout,
			)
		case KindClusterProvider:
			clusterProviderImpl = clusterprovider.NewKindClusterProvider(
				kubeConfigForTestClusters,
				kindNodeImage,
				defaultClusterWaitTimeout,
			)
		default:
			ginkgo.Fail(fmt.Sprintf("invalid cluster provider, %s or %s accepted", KwokClusterProvider, KindClusterProvider))
		}
	},
)

type framework struct {
	clusterProvider clusterprovider.ClusterProvider

	name      string
	namespace *corev1.Namespace
}

func NewFramework(baseName string, options FrameworkOptions) Framework {
	// suffix with a random string to support parallel tests
	baseName = fmt.Sprintf("%s-%s", baseName, rand.String(12))
	f := &framework{name: baseName}

	ginkgo.BeforeEach(ginkgo.OncePerOrdered, func(ctx ginkgo.SpecContext) {
		if options.CreateNamespace {
			ns := &corev1.Namespace{}
			ns.Name = strings.ToLower(fmt.Sprintf("%s-%s", baseName, rand.String(12)))

			var err error
			f.namespace, err = hostKubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
		}

		f.clusterProvider = clusterProviderImpl
		ginkgo.DeferCleanup(func(ctx ginkgo.SpecContext) {
			if options.CreateNamespace && !preserveNamespace {
				err := hostKubeClient.CoreV1().Namespaces().Delete(ctx, f.namespace.Name, metav1.DeleteOptions{})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			}
		})
	})

	return f
}

var _ Framework = &framework{}

func (f *framework) Name() string {
	return f.name
}

func (*framework) HostKubeClient() kubeclient.Interface {
	return hostKubeClient
}

func (*framework) HostFedClient() fedclient.Interface {
	return hostFedClient
}

func (*framework) HostDynamicClient() dynamic.Interface {
	return hostDynamicClient
}

func (f *framework) TestNamespace() *corev1.Namespace {
	gomega.Expect(f.namespace).ToNot(gomega.BeNil(), MessageUnexpectedError)
	return f.namespace
}

func (f *framework) NewCluster(ctx context.Context, clusterModifiers ...ClusterModifier) (*fedcorev1a1.FederatedCluster, *corev1.Secret, func(context.Context)) {
	clusterName := strings.ToLower(fmt.Sprintf("%s-%s", f.name, rand.String(12)))
	cluster, secret := f.clusterProvider.NewCluster(ctx, clusterName)

	// register cleanup handler
	ginkgo.DeferCleanup(func(ctx ginkgo.SpecContext) {
		f.cleanupCluster(ctx, cluster.Name, secret.Name)
	}, ginkgo.NodeTimeout(defaultClusterCleanupTimeout))

	// apply modifiers
	for _, modifier := range clusterModifiers {
		modifier(cluster)
	}

	// add test labels
	if cluster.Labels == nil {
		cluster.Labels = map[string]string{}
	}
	cluster.Labels[E2ETestObjectLabel] = "true"

	// create secret and cluster
	var err error
	secret, err = hostKubeClient.CoreV1().Secrets(FedSystemNamespace).Create(
		ctx,
		secret,
		metav1.CreateOptions{},
	)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	cluster, err = hostFedClient.CoreV1alpha1().FederatedClusters().Create(
		ctx,
		cluster,
		metav1.CreateOptions{},
	)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	ginkgo.GinkgoLogr.Info("cluster created", "cluster-name", cluster.Name, "kube-config", kubeConfigForTestClusters)

	stopCluster := func(ctx context.Context) {
		f.clusterProvider.StopCluster(ctx, clusterName)
	}

	return cluster, secret, stopCluster
}

func (f *framework) ClusterKubeClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) kubeclient.Interface {
	if client, ok := clusterKubeClients.Load(cluster.Name); ok {
		return client.(*kubeclient.Clientset)
	}

	restConfig := restConfigFromCluster(ctx, cluster)
	client, err := kubeclient.NewForConfig(restConfig)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	clusterKubeClients.Store(cluster.Name, client)
	return client
}

func (f *framework) ClusterFedClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) fedclient.Interface {
	if client, ok := clusterFedClients.Load(cluster.Name); ok {
		return client.(*fedclient.Clientset)
	}

	restConfig := restConfigFromCluster(ctx, cluster)
	client, err := fedclient.NewForConfig(restConfig)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	clusterFedClients.Store(cluster.Name, client)
	return client
}

func (f *framework) ClusterDynamicClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) dynamic.Interface {
	if client, ok := clusterDynamicClients.Load(cluster.Name); ok {
		return client.(*dynamic.DynamicClient)
	}

	restConfig := restConfigFromCluster(ctx, cluster)
	client, err := dynamic.NewForConfig(restConfig)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	clusterDynamicClients.Store(cluster.Name, client)
	return client
}

func restConfigFromCluster(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) *rest.Config {
	restConfig := &rest.Config{Host: cluster.Spec.APIEndpoint}
	restConfig.Insecure = cluster.Spec.Insecure

	clusterSecretName := cluster.Spec.SecretRef.Name
	gomega.Expect(clusterSecretName).ToNot(gomega.BeEmpty())

	secret, err := hostKubeClient.CoreV1().Secrets(FedSystemNamespace).Get(
		ctx,
		clusterSecretName,
		metav1.GetOptions{},
	)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	caData := secret.Data[common.ClusterCertificateAuthorityKey]
	gomega.Expect(caData).ToNot(gomega.BeEmpty())
	certData := secret.Data[common.ClusterClientCertificateKey]
	gomega.Expect(certData).ToNot(gomega.BeEmpty())
	keyData := secret.Data[common.ClusterClientKeyKey]
	gomega.Expect(keyData).ToNot(gomega.BeEmpty())

	restConfig.TLSClientConfig = rest.TLSClientConfig{
		CertData: certData,
		KeyData:  keyData,
		CAData:   caData,
	}

	return restConfig
}

func (f *framework) cleanupCluster(ctx context.Context, cluster, secret string) {
	if preserveClusters {
		return
	}

	err := hostFedClient.CoreV1alpha1().FederatedClusters().Delete(
		ctx,
		cluster,
		metav1.DeleteOptions{},
	)
	if err != nil && !apierrors.IsNotFound(err) {
		ginkgo.GinkgoLogr.Error(
			err,
			"failed to cleanup federated cluster",
			"cluster-name",
			cluster,
		)
	}
	if err == nil {
		// cluster deletion succeeded, since the cluster may contain finalizers, we force its removal by deleting all finalizers -- this is
		// safe to do since it is a test cluster
		_, err := hostFedClient.CoreV1alpha1().FederatedClusters().Patch(
			ctx,
			cluster,
			types.JSONPatchType,
			[]byte("[{\"op\": \"replace\", \"path\": \"/metadata/finalizers\", \"value\": []}]"),
			metav1.PatchOptions{},
		)
		if err != nil && !apierrors.IsNotFound(err) {
			ginkgo.GinkgoLogr.Error(
				err,
				"failed to cleanup federated cluster",
				"cluster-name",
				cluster,
			)
		}
	}

	if err := hostKubeClient.CoreV1().Secrets(FedSystemNamespace).Delete(
		ctx,
		secret,
		metav1.DeleteOptions{},
	); err != nil && !apierrors.IsNotFound(err) {
		ginkgo.GinkgoLogr.Error(
			err,
			"failed to cleanup federated cluster secret",
			"secret-name",
			secret,
		)
	}

	f.clusterProvider.StopCluster(ctx, cluster)
}
