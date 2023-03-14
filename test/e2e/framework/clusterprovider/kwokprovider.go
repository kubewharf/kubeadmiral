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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type kwokClusterProvider struct {
	kubeConfig         string
	kwokImagePrefix    string
	kwokKubeVersion    string
	clusterWaitTimeout time.Duration

	kwokWorkDir string

	clusters sync.Map
}

func NewKwokClusterProvider(
	kubeConfig, kwokWorkDir, kwokImagePrefix, kwokKubeVersion string,
	clusterWaitTimeout time.Duration,
) ClusterProvider {
	return &kwokClusterProvider{
		kubeConfig:         kubeConfig,
		kwokImagePrefix:    kwokImagePrefix,
		kwokKubeVersion:    kwokKubeVersion,
		kwokWorkDir:        kwokWorkDir,
		clusterWaitTimeout: clusterWaitTimeout,
	}
}

var _ ClusterProvider = &kwokClusterProvider{}

func (p *kwokClusterProvider) NewCluster(ctx context.Context, name string) (*fedcorev1a1.FederatedCluster, *corev1.Secret) {
	clusterName := name
	secretName := fmt.Sprintf("%s-secret", clusterName)

	ctx, cancel := context.WithTimeout(ctx, p.clusterWaitTimeout)
	defer cancel()

	var stdout []byte
	var stderr []byte
	var runErr error
	ginkgo.DeferCleanup(func(ctx ginkgo.SpecContext) {
		if runErr != nil {
			p.stopCluster(ctx, clusterName)
		}
	})

	// 1. build and run cluster

	// generate a random port number in range 50000-60000 for kubeapiserver
	port := rand.Int63nRange(50000, 60000)
	_, stderr, runErr = p.runKwokCommand(
		ctx,
		clusterName,
		[]string{},
		[]string{"create", "cluster", "--quiet-pull=true", "--kube-authorization", fmt.Sprintf("--kube-apiserver-port=%d", port)},
	)
	gomega.Expect(runErr).ToNot(gomega.HaveOccurred(), fmt.Sprintf("error creating kwok cluster: %s", stderr))

	p.clusters.Store(clusterName, nil)

	// 2. get kubeconfig for cluster

	stdout, stderr, runErr = p.runKwokCommand(
		ctx,
		clusterName,
		[]string{},
		[]string{"get", "kubeconfig"},
	)
	gomega.Expect(runErr).ToNot(gomega.HaveOccurred(), fmt.Sprintf("error getting kubeconfig: %s", stderr))

	// 3. create node

	restConfig, runErr := clientcmd.RESTConfigFromKubeConfig(stdout)
	gomega.Expect(runErr).ToNot(gomega.HaveOccurred())

	client := kubernetes.NewForConfigOrDie(restConfig)
	_, runErr = client.CoreV1().Nodes().Create(ctx, p.getNodeTemplate(clusterName), metav1.CreateOptions{})
	gomega.Expect(runErr).ToNot(gomega.HaveOccurred())

	// 4. wait for apiserver to be ready

	runErr = wait.PollUntilWithContext(ctx, 50*time.Millisecond, func(ctx context.Context) (done bool, err error) {
		body, err := client.DiscoveryV1().RESTClient().Get().AbsPath("/healthz").Do(ctx).Raw()
		if err != nil {
			return false, nil
		}
		return strings.EqualFold(string(body), "ok"), nil
	})
	gomega.Expect(runErr).ToNot(gomega.HaveOccurred(), "timeout waiting for kwok cluster to be ready")

	// 5. return cluster

	// NOTE: kwok does not write the apiserver's CA cert to the generated kubeconfig so we use this hack to retrieve it
	caFile := fmt.Sprintf("%s/clusters/%s/pki/ca.crt", p.kwokWorkDir, name)
	caData, runErr := os.ReadFile(caFile)
	gomega.Expect(runErr).ToNot(gomega.HaveOccurred())

	cluster := &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Spec: fedcorev1a1.FederatedClusterSpec{
			APIEndpoint: restConfig.Host,
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
			common.ClusterCertificateAuthorityKey: caData,
			common.ClusterClientCertificateKey:    restConfig.CertData,
			common.ClusterClientKeyKey:            restConfig.KeyData,
		},
	}

	return cluster, secret
}

func (p *kwokClusterProvider) StopCluster(ctx context.Context, name string) {
	if _, ok := p.clusters.Load(name); ok {
		p.stopCluster(ctx, name)
	}
}

func (p *kwokClusterProvider) StopAllClusters(ctx context.Context) {
	p.clusters.Range(func(cluster, _ any) bool {
		p.stopCluster(ctx, cluster.(string))
		return true
	})
}

func (p *kwokClusterProvider) stopCluster(ctx context.Context, name string) {
	_, stderr, err := p.runKwokCommand(ctx, name, []string{}, []string{"delete", "cluster"})
	gomega.Expect(err).ToNot(gomega.HaveOccurred(), fmt.Sprintf("error deleting kwok cluster: %s", stderr))
}

func (p *kwokClusterProvider) getNodeTemplate(cluster string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-control-plane", cluster),
			Annotations: map[string]string{
				"node.alpha.kubernetes.io/ttl": "0",
			},
			Labels: map[string]string{
				"beta.kubernetes.io/arch":       runtime.GOARCH,
				"beta.kubernetes.io/os":         "linux",
				"kubernetes.io/arch":            runtime.GOARCH,
				"kubernetes.io/hostname":        fmt.Sprintf("%s-control-plane", cluster),
				"kubernetes.io/os":              "linux",
				"kubernetes.io/role":            "agent",
				"node-role.kubernetes.io/agent": "",
				"type":                          "kwok",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("256Gi"),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("256Gi"),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			NodeInfo: corev1.NodeSystemInfo{
				Architecture:            runtime.GOARCH,
				BootID:                  "",
				ContainerRuntimeVersion: "",
				KernelVersion:           "",
				KubeProxyVersion:        "",
				KubeletVersion:          "fake",
				MachineID:               "",
				OperatingSystem:         "linux",
				OSImage:                 "",
				SystemUUID:              "",
			},
			Phase: corev1.NodeRunning,
		},
	}
}

func (p *kwokClusterProvider) runKwokCommand(
	ctx context.Context,
	cluster string,
	extraEnvs, extraArgs []string,
) (stdout []byte, stderr []byte, err error) {
	baseArgs := []string{"--name=" + cluster}
	args := append(baseArgs, extraArgs...)

	baseEnvs := []string{
		"PATH=" + os.Getenv("PATH"),
		"KUBECONFIG=" + p.kubeConfig,
		"KWOK_KUBE_IMAGE_PREFIX=" + p.kwokImagePrefix,
		"KWOK_KUBE_VERSION=" + p.kwokKubeVersion,
		"KWOK_WORKDIR=" + p.kwokWorkDir,
	}
	envs := append(baseEnvs, extraEnvs...)

	cmd := exec.CommandContext(ctx, "kwokctl", args...)
	cmd.Env = append(cmd.Env, envs...)

	outBuf := bytes.NewBuffer(nil)
	cmd.Stdout = outBuf

	errBuf := bytes.NewBuffer(nil)
	cmd.Stderr = errBuf

	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}
