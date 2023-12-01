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

package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func BuildClusterConfig(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient kubernetes.Interface,
	restConfig *rest.Config,
	fedSystemNamespace string,
) (*rest.Config, error) {
	return buildClusterConfig(
		cluster,
		fedClient,
		restConfig,
		fedSystemNamespace,
		!cluster.Spec.UseServiceAccountToken,
	)
}

// BuildRawClusterConfig returns a restclient.Config built using key and certificate
// credentials from the secret referenced in the FederatedCluster.
func BuildRawClusterConfig(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient kubernetes.Interface,
	restConfig *rest.Config,
	fedSystemNamespace string,
) (*rest.Config, error) {
	return buildClusterConfig(
		cluster,
		fedClient,
		restConfig,
		fedSystemNamespace,
		true,
	)
}

func buildClusterConfig(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient kubernetes.Interface,
	restConfig *rest.Config,
	fedSystemNamespace string,
	useBootstrap bool,
) (*rest.Config, error) {
	apiEndpoint := cluster.Spec.APIEndpoint
	if len(apiEndpoint) == 0 {
		return nil, fmt.Errorf("api endpoint of cluster %s is empty", cluster.Name)
	}

	clusterConfig, err := clientcmd.BuildConfigFromFlags(apiEndpoint, "")
	if err != nil {
		return nil, err
	}

	clusterConfig.QPS = restConfig.QPS
	clusterConfig.Burst = restConfig.Burst
	clusterConfig.UserAgent = restConfig.UserAgent

	secretName := cluster.Spec.SecretRef.Name
	if len(secretName) == 0 {
		clusterConfig.CAFile = restConfig.CAFile
		clusterConfig.CertFile = restConfig.CertFile
		clusterConfig.KeyFile = restConfig.KeyFile
		return clusterConfig, nil
	}

	secret, err := fedClient.CoreV1().Secrets(fedSystemNamespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	err = PopulateAuthDetailsFromSecret(clusterConfig, cluster.Spec.Insecure, secret, useBootstrap)
	if err != nil {
		return nil, fmt.Errorf("cannot build rest config from cluster secret: %w", err)
	}
	return clusterConfig, nil
}

func PopulateAuthDetailsFromSecret(
	clusterConfig *rest.Config,
	insecure bool,
	secret *corev1.Secret,
	useBootstrap bool,
) error {
	if insecure {
		clusterConfig.Insecure = true
	} else {
		// if federatedCluster.Spec.Insecure is false, ca data is required
		if clusterConfig.CAData = secret.Data[common.ClusterCertificateAuthorityKey]; len(clusterConfig.CAData) == 0 {
			return fmt.Errorf("%q data is missing from secret and insecure is false", common.ClusterCertificateAuthorityKey)
		}
	}

	// if useBootstrap is true, we will use the client authentication specified by user
	// else, we will use the token created by admiral
	if useBootstrap {
		var bootstrapTokenExist bool
		clusterConfig.BearerToken, bootstrapTokenExist = getTokenDataFromSecret(secret, common.ClusterBootstrapTokenKey)
		clusterConfig.CertData, clusterConfig.KeyData = secret.Data[common.ClusterClientCertificateKey], secret.Data[common.ClusterClientKeyKey]

		if !bootstrapTokenExist && (len(clusterConfig.CertData) == 0 || len(clusterConfig.KeyData) == 0) {
			return fmt.Errorf("the client authentication information is missing from secret, " +
				"at least token or (certificate, key) information is required")
		}
	} else {
		var saTokenExist bool
		if clusterConfig.BearerToken, saTokenExist = getTokenDataFromSecret(secret, common.ClusterServiceAccountTokenKey); !saTokenExist {
			return fmt.Errorf("%q data is missing from secret", common.ClusterServiceAccountTokenKey)
		}
	}

	return nil
}

func getTokenDataFromSecret(secret *corev1.Secret, tokenKey string) (string, bool) {
	bootstrapToken := secret.Data[tokenKey]
	return string(bootstrapToken), len(bootstrapToken) != 0
}
