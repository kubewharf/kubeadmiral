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

package scheduler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	jsonutil "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicFake "k8s.io/client-go/dynamic/fake"
	kubeFake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2/ktesting"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	schedwebhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
	fedFake "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/fake"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
)

// Generate a self-signed certificate and key pair.
// DO NOT USE FOR PRODUCTION.
// Ref: go's stdlib crypto/tls/generate_cert.go
func generateCertAndKey(isClientCert bool, serverNames []string, ipAddresses []net.IP) ([]byte, []byte) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	notBefore := time.Unix(0, 0)
	notAfter := notBefore.Add(100 * 365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		panic(err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{},
		BasicConstraintsValid: true,

		DNSNames:    serverNames,
		IPAddresses: ipAddresses,
	}

	if isClientCert {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	} else {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		panic(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	return certPEM, keyPEM
}

func getCluster(name string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: fedcorev1a1.FederatedClusterStatus{
			Conditions: []fedcorev1a1.ClusterCondition{
				{
					Type:   fedcorev1a1.ClusterJoined,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

/*
This tests the following:

- creating webhook plugins from SchedulerPluginWebhookConfiguration
- resolving webhook plugins from SchedulingProfile
- calling the webhook plugins
*/
func doTest(t *testing.T, clientTLS *fedcorev1a1.WebhookTLSConfig, serverTLS *tls.Config) {
	t.Helper()
	g := gomega.NewWithT(t)

	var filterCalled atomic.Int32
	mux := http.NewServeMux()
	mux.Handle("/filter", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filterCalled.Add(1)

		req := schedwebhookv1a1.FilterRequest{}
		err := json.NewDecoder(r.Body).Decode(&req)
		g.Expect(err).NotTo(gomega.HaveOccurred())

		t.Logf("Received filter request for cluster %v", req.Cluster.Name)
		response := schedwebhookv1a1.FilterResponse{
			Selected: !strings.Contains(req.Cluster.Name, "reject"),
			Error:    "",
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(&response)
		g.Expect(err).NotTo(gomega.HaveOccurred())
	}))

	server := httptest.NewUnstartedServer(mux)
	if serverTLS != nil {
		server.TLS = serverTLS.Clone()
		server.StartTLS()
	} else {
		server.Start()
	}
	defer server.Close()

	webhookConfig := fedcorev1a1.SchedulerPluginWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
		Spec: fedcorev1a1.SchedulerPluginWebhookConfigurationSpec{
			PayloadVersions: []string{"v1alpha1"},
			FilterPath:      "/filter",
			HTTPTimeout:     metav1.Duration{Duration: 2 * time.Second},
			URLPrefix:       server.URL,
			TLSConfig:       clientTLS.DeepCopy(),
		},
	}

	// only enable webhook plugin
	profile := fedcorev1a1.SchedulingProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "webhook-only",
		},
		Spec: fedcorev1a1.SchedulingProfileSpec{
			Plugins: &fedcorev1a1.Plugins{
				Filter: fedcorev1a1.PluginSet{
					Enabled: []fedcorev1a1.Plugin{
						{
							Type: "Webhook",
							Name: "test-webhook",
						},
					},
					Disabled: []fedcorev1a1.Plugin{
						{Name: "*"},
					},
				},
				Score: fedcorev1a1.PluginSet{
					Disabled: []fedcorev1a1.Plugin{
						{Name: "*"},
					},
				},
				Select: fedcorev1a1.PluginSet{
					Disabled: []fedcorev1a1.Plugin{
						{Name: "*"},
					},
				},
			},
		},
	}

	policy := fedcorev1a1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "webhook-policy",
		},
		Spec: fedcorev1a1.PropagationPolicySpec{
			SchedulingProfile: "webhook-only",
			SchedulingMode:    fedcorev1a1.SchedulingModeDuplicate,
		},
	}

	clusters := []runtime.Object{
		getCluster("accept"),
		getCluster("reject"),
	}

	ftc := fedcorev1a1.FederatedTypeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "deployment.apps",
		},
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "apps",
				Version:    "v1",
				Kind:       "Deployment",
				PluralName: "deployments",
				Scope:      v1beta1.NamespaceScoped,
			},
		},
	}

	kubeClient := kubeFake.NewSimpleClientset()

	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	err = appsv1.AddToScheme(scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	dynamicClient := dynamicFake.NewSimpleDynamicClient(scheme)
	dynInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)

	testObjs := []runtime.Object{}
	testObjs = append(testObjs, &webhookConfig, &profile, &policy, &ftc)
	testObjs = append(testObjs, clusters...)

	// Ensure watcher is started before creating objects to avoid missing events occurred after LIST and before WATCH
	// ref: https://github.com/kubernetes/client-go/blob/master/examples/fake-client/main_test.go
	watcherStarted := make(chan struct{})
	fedClient := fedFake.NewSimpleClientset(testObjs...)
	fedClient.PrependWatchReactor(
		"*",
		func(action clienttesting.Action) (handled bool, ret watch.Interface, err error) {
			gvr := action.GetResource()
			ns := action.GetNamespace()
			watch, err := fedClient.Tracker().Watch(gvr, ns)
			if err != nil {
				return false, nil, err
			}
			select {
			case <-watcherStarted:
			default:
				close(watcherStarted)
			}
			return true, watch, nil
		},
	)
	fedInformerFactory := fedinformers.NewSharedInformerFactory(fedClient, 0)

	manager := informermanager.NewInformerManager(
		dynamicClient,
		fedInformerFactory.Core().V1alpha1().FederatedTypeConfigs(),
		nil,
	)

	scheduler, err := NewScheduler(
		kubeClient,
		fedClient,
		dynamicClient,
		fedInformerFactory.Core().V1alpha1().FederatedObjects(),
		fedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		fedInformerFactory.Core().V1alpha1().PropagationPolicies(),
		fedInformerFactory.Core().V1alpha1().ClusterPropagationPolicies(),
		fedInformerFactory.Core().V1alpha1().FederatedClusters(),
		fedInformerFactory.Core().V1alpha1().SchedulingProfiles(),
		manager,
		fedInformerFactory.Core().V1alpha1().SchedulerPluginWebhookConfigurations(),
		stats.NewMock("test", "kube-admiral", false),
		ktesting.NewLogger(t, ktesting.NewConfig(ktesting.Verbosity(3))),
		1,
		false,
	)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	ctx := context.Background()
	dynInformerFactory.Start(ctx.Done())
	fedInformerFactory.Start(ctx.Done())
	manager.Start(ctx)

	go scheduler.Run(ctx)

	cache.WaitForCacheSync(ctx.Done(), scheduler.HasSynced)
	<-watcherStarted

	// Wait for the plugin to be initialized
	g.Eventually(func(g gomega.Gomega) {
		plugin, exists := scheduler.webhookPlugins.Load(webhookConfig.Name)
		g.Expect(exists).To(gomega.BeTrue())
		g.Expect(plugin.(framework.Plugin).Name()).To(gomega.Equal(webhookConfig.Name))
	}).WithContext(ctx).WithTimeout(3 * time.Second).WithPolling(100 * time.Millisecond).Should(gomega.Succeed())

	fedObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels: map[string]string{
				PropagationPolicyNameLabel: policy.Name,
			},
			Annotations: map[string]string{
				pendingcontrollers.PendingControllersAnnotation: "[]",
			},
		},
	}
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}
	rawBytes, err := jsonutil.Marshal(deployment)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	fedObj.Spec.Template.Raw = rawBytes

	fedObj, err = fedClient.CoreV1alpha1().
		FederatedObjects(fedObj.Namespace).
		Create(ctx, fedObj, metav1.CreateOptions{})
	g.Expect(err).NotTo(gomega.HaveOccurred())

	g.Eventually(func(g gomega.Gomega) {
		res, err := fedClient.CoreV1alpha1().
			FederatedObjects(fedObj.Namespace).
			Get(ctx, fedObj.Name, metav1.GetOptions{})
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(res.GetSpec().GetPlacementUnion()).To(gomega.Equal(sets.New("accept")))
	}).WithContext(ctx).WithTimeout(3 * time.Second).WithPolling(100 * time.Millisecond).Should(gomega.Succeed())

	g.Expect(filterCalled.Load()).To(gomega.Equal(int32(len(clusters))))
}

func TestSchedulerWebhook(t *testing.T) {
	type testCase struct {
		clientConfig *fedcorev1a1.WebhookTLSConfig
		serverConfig *tls.Config
	}

	g := gomega.NewWithT(t)

	localHostCert, localHostKey := generateCertAndKey(false, nil, []net.IP{net.IPv4(127, 0, 0, 1)})
	exampleComHost := "example.com"
	exampleComHostCert, exampleComHostKey := generateCertAndKey(false, []string{exampleComHost}, nil)

	clientCert, clientKey := generateCertAndKey(true, nil, nil)

	testCases := map[string]testCase{
		"no TLS": {
			// neither party uses TLS and communicates with each other over plain TCP
		},
		"only server uses TLS but client skips verification": {
			clientConfig: &fedcorev1a1.WebhookTLSConfig{
				Insecure: true,
			},
			// Pass empty non-nil tls.Config to default to httptest server's internal TLS config
			serverConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		"only server uses TLS": func() testCase {
			serverCert, serverKey := localHostCert, localHostKey

			clientConfig := &fedcorev1a1.WebhookTLSConfig{
				Insecure: false,
				CAData:   serverCert,
			}

			tlsCert, err := tls.X509KeyPair(serverCert, serverKey)
			g.Expect(err).NotTo(gomega.HaveOccurred())

			serverConfig := &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{tlsCert},
			}

			return testCase{clientConfig: clientConfig, serverConfig: serverConfig}
		}(),
		"only server uses TLS but with different name in certificate": func() testCase {
			serverCert, serverKey := exampleComHostCert, exampleComHostKey

			clientConfig := &fedcorev1a1.WebhookTLSConfig{
				Insecure:   false,
				ServerName: exampleComHost,
				CAData:     serverCert,
			}

			tlsCert, err := tls.X509KeyPair(serverCert, serverKey)
			g.Expect(err).NotTo(gomega.HaveOccurred())

			serverConfig := &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{tlsCert},
			}

			return testCase{clientConfig: clientConfig, serverConfig: serverConfig}
		}(),
		"mutual TLS": func() testCase {
			serverCert, serverKey := localHostCert, localHostKey
			clientCert, clientKey := clientCert, clientKey

			clientConfig := &fedcorev1a1.WebhookTLSConfig{
				Insecure: false,
				CertData: clientCert,
				KeyData:  clientKey,
				CAData:   serverCert,
			}

			tlsCert, err := tls.X509KeyPair(serverCert, serverKey)
			g.Expect(err).NotTo(gomega.HaveOccurred())

			clientCAs := x509.NewCertPool()
			ok := clientCAs.AppendCertsFromPEM(clientCert)
			g.Expect(ok).To(gomega.BeTrue())

			serverConfig := &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{tlsCert},
				// require client authentication
				ClientAuth: tls.RequireAndVerifyClientCert,
				// server should trust the client cert
				ClientCAs: clientCAs,
			}

			return testCase{clientConfig: clientConfig, serverConfig: serverConfig}
		}(),
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			doTest(t, tc.clientConfig, tc.serverConfig)
		})
	}
}
