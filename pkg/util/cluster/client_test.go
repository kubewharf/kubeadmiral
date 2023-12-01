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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func TestPopulateAuthDetailsFromSecret(t *testing.T) {
	tests := []struct {
		name           string
		insecure       bool
		inputSecret    *corev1.Secret
		useBootstrap   bool
		expectedConfig *rest.Config
		expectedErr    error
	}{
		{
			name:         "normal test, insecure=true, useBootstrap=true",
			insecure:     true,
			inputSecret:  buildSecretData("ca", "cert", "key", "bootstrapToken", ""),
			useBootstrap: true,
			expectedConfig: &rest.Config{
				TLSClientConfig: rest.TLSClientConfig{
					Insecure: true,
					CertData: []byte("cert"),
					KeyData:  []byte("key"),
				},
				BearerToken: "bootstrapToken",
			},
		},
		{
			name:         "normal test, insecure=false, useBootstrap=true",
			insecure:     false,
			inputSecret:  buildSecretData("ca", "cert", "key", "bootstrapToken", ""),
			useBootstrap: true,
			expectedConfig: &rest.Config{
				TLSClientConfig: rest.TLSClientConfig{
					CertData: []byte("cert"),
					KeyData:  []byte("key"),
					CAData:   []byte("ca"),
				},
				BearerToken: "bootstrapToken",
			},
		},
		{
			name:         "normal test, useBootstrap==true, (cert,key) is empty, bootstrapToken is not empty",
			insecure:     false,
			inputSecret:  buildSecretData("ca", "", "", "bootstrapToken", ""),
			useBootstrap: true,
			expectedConfig: &rest.Config{
				TLSClientConfig: rest.TLSClientConfig{
					CertData: []byte(""),
					KeyData:  []byte(""),
					CAData:   []byte("ca"),
				},
				BearerToken: "bootstrapToken",
			},
		},
		{
			name:         "normal test, useBootstrap==true, (cert,key) is not empty, bootstrapToken is empty",
			insecure:     false,
			inputSecret:  buildSecretData("ca", "cert", "key", "", ""),
			useBootstrap: true,
			expectedConfig: &rest.Config{
				TLSClientConfig: rest.TLSClientConfig{
					CertData: []byte("cert"),
					KeyData:  []byte("key"),
					CAData:   []byte("ca"),
				},
				BearerToken: "",
			},
		},
		{
			name:         "normal test, useBootstrap==false",
			insecure:     false,
			inputSecret:  buildSecretData("ca", "", "", "", "sat"),
			useBootstrap: false,
			expectedConfig: &rest.Config{
				TLSClientConfig: rest.TLSClientConfig{
					CAData: []byte("ca"),
				},
				BearerToken: "sat",
			},
		},
		// error test
		{
			name:        "insecure=false, but secret.Data[certificate-authority-data] is empty",
			insecure:    false,
			inputSecret: buildSecretData("", "", "", "", ""),
			expectedErr: fmt.Errorf(
				"%q data is missing from secret and insecure is false", common.ClusterCertificateAuthorityKey),
		},
		{
			name:         "useBootstrap=false, but Secret.Data[service-account-token-data] is empty",
			insecure:     false,
			useBootstrap: false,
			inputSecret:  buildSecretData("ca", "", "", "", ""),
			expectedErr:  fmt.Errorf("%q data is missing from secret", common.ClusterServiceAccountTokenKey),
		},
		{
			name:         "useBootstrap=true, but both (cert,key) and bootstrapToken are empty",
			insecure:     false,
			useBootstrap: true,
			inputSecret:  buildSecretData("ca", "", "", "", ""),
			expectedErr: fmt.Errorf("the client authentication information is missing from secret, " +
				"at least token or (certificate, key) information is required"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualClusterConfig := &rest.Config{}
			err := PopulateAuthDetailsFromSecret(actualClusterConfig, tt.insecure, tt.inputSecret, tt.useBootstrap)

			assert.Equal(t, tt.expectedErr, err)
			if tt.expectedConfig != nil {
				assert.Equal(t, tt.expectedConfig, actualClusterConfig)
			}
		})
	}
}

func buildSecretData(ca, cert, key, bt, sat string) *corev1.Secret {
	return &corev1.Secret{
		Data: map[string][]byte{
			common.ClusterCertificateAuthorityKey: []byte(ca),
			common.ClusterClientCertificateKey:    []byte(cert),
			common.ClusterClientKeyKey:            []byte(key),
			common.ClusterBootstrapTokenKey:       []byte(bt),
			common.ClusterServiceAccountTokenKey:  []byte(sat),
		},
	}
}
