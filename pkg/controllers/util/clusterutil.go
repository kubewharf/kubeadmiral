/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package util

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
)

// User account keys
const (
	ClientCertificateKey    = "client-certificate-data"
	ClientKeyKey            = "client-key-data"
	CertificateAuthorityKey = "certificate-authority-data"
)

// Service account keys
const (
	ServiceAccountTokenKey = "service-account-token-data"
	ServiceAccountCAKey    = "service-account-ca-data"
)

// BuildClusterConfig returns a restclient.Config that can be used to configure
// a client for the given FederatedCluster or an error.
func BuildClusterConfig(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient kubeclient.Interface,
	restConfig *restclient.Config,
	fedSystemNamespace string,
) (*restclient.Config, error) {
	return buildClusterConfig(
		cluster,
		fedClient,
		restConfig,
		fedSystemNamespace,
		cluster.Spec.UseServiceAccountToken,
	)
}

// BuildRawClusterConfig returns a restclient.Config built using key and certificate
// credentials from the secret referenced in the FederatedCluster.
func BuildRawClusterConfig(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient kubeclient.Interface,
	restConfig *restclient.Config,
	fedSystemNamespace string,
) (*restclient.Config, error) {
	return buildClusterConfig(
		cluster,
		fedClient,
		restConfig,
		fedSystemNamespace,
		false,
	)
}

func buildClusterConfig(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient kubeclient.Interface,
	restConfig *restclient.Config,
	fedSystemNamespace string,
	useServiceAccountToken bool,
) (*restclient.Config, error) {
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

	err = PopulateAuthDetailsFromSecret(clusterConfig, cluster.Spec.Insecure, secret, useServiceAccountToken)
	if err != nil {
		return nil, fmt.Errorf("cannot build rest config from cluster secret: %w", err)
	}
	return clusterConfig, nil
}

func PopulateAuthDetailsFromSecret(
	clusterConfig *restclient.Config,
	insecure bool,
	secret *corev1.Secret,
	useServiceAccount bool,
) error {
	var exists bool

	if useServiceAccount {
		serviceAccountToken, exists := secret.Data[ServiceAccountTokenKey]
		if !exists {
			return fmt.Errorf("%q data is missing from secret", ServiceAccountTokenKey)
		}
		clusterConfig.BearerToken = string(serviceAccountToken)

		if insecure {
			clusterConfig.Insecure = true
		} else {
			clusterConfig.CAData, exists = secret.Data[ServiceAccountCAKey]
			if !exists {
				return fmt.Errorf("%q data is missing from secret and insecure is false", ServiceAccountCAKey)
			}
		}
	} else {
		clusterConfig.CertData, exists = secret.Data[ClientCertificateKey]
		if !exists {
			return fmt.Errorf("%q data is missing from secret", ClientCertificateKey)
		}

		clusterConfig.KeyData, exists = secret.Data[ClientKeyKey]
		if !exists {
			return fmt.Errorf("%q data is missing from secret", ClientKeyKey)
		}

		if insecure {
			clusterConfig.Insecure = true
		} else {
			clusterConfig.CAData, exists = secret.Data[CertificateAuthorityKey]
			if !exists {
				return fmt.Errorf("%q data is missing from secret", CertificateAuthorityKey)
			}
		}
	}

	return nil
}

// BuildClusterConfig returns a restclient.Config that can be used to configure
// a client for the given FederatedCluster or an error.
func BuildClusterConfigWithGenericClient(
	cluster *fedcorev1a1.FederatedCluster,
	fedClient generic.Client,
	restConfig *restclient.Config,
	fedSystemNamespace string,
) (*restclient.Config, error) {
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

	secret := &corev1.Secret{}
	err = fedClient.Get(context.TODO(), secret, fedSystemNamespace, cluster.Spec.SecretRef.Name)
	if err != nil {
		return nil, err
	}

	err = PopulateAuthDetailsFromSecret(clusterConfig, cluster.Spec.Insecure, secret, cluster.Spec.UseServiceAccountToken)
	if err != nil {
		return nil, fmt.Errorf("cannot build rest config from cluster secret: %w", err)
	}

	return clusterConfig, nil
}
