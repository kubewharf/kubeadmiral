# KubeAdmiral E2E Tests

This guide provides instructions on how to run and write end-to-end tests for KubeAdmiral.

## Dependencies

Make sure you have the following installed:

1. [Ginkgo](https://onsi.github.io/ginkgo/#getting-started): KubeAdmiral uses Ginkgo to run its e2e test suite
2. [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/) or [Kwok](https://kwok.sigs.k8s.io/docs/user/install/): certain e2e tests require cluster provisioning which is performed by either Kind or Kwok

## Running tests

The KubeAdmiral e2e test suite is designed to run against an existing KubeAdmiral control plane so that an existing federation of clusters can be tested for both its health and compatibility.

You may use our Make command to run the e2e test suite. The Make command reads the path to the host cluster's kubeconfig from the `KUBECONFIG` environment variable.

```console
$ make e2e
```

The command above will run the e2e test suite with a set of default flag values. These flags may be modified if required. You may modify the flags using our Make command as such.

```console
$ EXTRA_GINKGO_FLAGS='' EXTRA_E2E_FLAGS='' make e2e
```

### Test flags

#### Ginkgo flags

We can configure the way Ginkgo runs our tests by passing Ginkgo flags to the Ginkgo CLI. Refer to the [Ginkgo docs](https://onsi.github.io/ginkgo/) for more details. Some useful flags are:

1. `--focus`: used to filter test specs to run
2. `--nodes`: used to run test specs in parallel

#### E2E flags

In addition to Ginkgo flags, our test suite also exposes its own set of flags to configure its behavior. The available flags are:

1. `--master`: the address of the host Kubernetes cluster
2. `--kubeconfig`: the kubeconfig of the host Kubernetes cluster
3. `--kube-api-qps`: the maximum QPS of each cluster's client (default: 500)
4. `--kube-api-burst`: the maximum burst for throttling requests for each cluster's client (default: 1000)
5. `--cluster-provider`: `kwok` or `kind`, selects the cluster provider to use (default: kwok)
6. `--kind-node-image`: the node image to use if Kind is selected as the cluster provider (default: kindest/node:v1.20.15@sha256:a32bf55309294120616886b5338f95dd98a2f7231519c7dedcec32ba29699394)
7. `--kwok-image-prefix`: the image prefix used by Kwok to pull Kubernetes images (default: registry.k8s.io)
8. `--kwok-kube-version`: the kubernetes version to use when creating Kwok member clusters (default: v1.20.15)
9. `--preserve-clusters`: if true, clusters created during the tests are preserved (default: false)
10. `--preserve-namespaces`: if true, namespaces created during the tests are preserved (default: false)
11. `--default-pod-image`: the default pod image to use when generating container templates


## Writing tests

The KubeAdmiral e2e test suite is written using Ginkgo test specs. You may refer to the [Ginkgo docs](https://onsi.github.io/ginkgo/) for more information.

When defining a new test spec, first initialize the test framework by calling `framework.NewFramework`.

```go
var _ = ginkgo.Describe("Example", func() {
        f := framework.NewFramework("example", framework.FrameworkOptions{})
        ...
}
```

Subsequently, you may use the returned `Framework` interface to perform useful operations like cluster provisioning, accessing the test namespace or retrieving cluster clients.

```go
var _ = ginkgo.Describe("Example", ginkgo.Label(framework.TestLabelNeedCreateCluster), func() {
        f := framework.NewFramework("example", framework.FrameworkOptions{})
        
        waitForClusterJoin := func(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) {
              ...
        }

        ginkgo.It("create cluster", func(ctx ginkgo.SpecContext) {
                cluster, _ = f.NewCluster(ctx)
                waitForClusterJoin(ctx, cluster)
        })
})
```

This is the full interface of `Framework`.

```go
type Framework interface {
        HostKubeClient() kubeclient.Interface
        HostFedClient() fedclient.Interface
        HostDynamicClient() dynamic.Interface

        Name() string
        TestNamespace() *corev1.Namespace

        NewCluster(ctx context.Context, clusterModifiers ...ClusterModifier) (*fedcorev1a1.FederatedCluster, *corev1.Secret)

        ClusterKubeClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) kubeclient.Interface
        ClusterFedClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) fedclient.Interface
        ClusterDynamicClient(ctx context.Context, cluster *fedcorev1a1.FederatedCluster) dynamic.Interface
}
```

Additionally, useful helper functions are provided by the following packages:

* [github.com/kubewharf/kubeadmiral/test/e2e/framework/util](../test/e2e/framework/util/)
* [github.com/kubewharf/kubeadmiral/test/e2e/framework/resources](../test/e2e/framework/resources/)
* [github.com/kubewharf/kubeadmiral/test/e2e/framework/cluster](../test/e2e/framework/cluster/)

