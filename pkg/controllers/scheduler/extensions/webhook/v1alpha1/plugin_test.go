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

package v1alpha1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	schedwebhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/test/gomega/custommatchers"
)

const (
	filterPath = "filter"
	scorePath  = "score"
	selectPath = "select"
)

var (
	// sample error that http.Client.Do might return
	errClientSample    = fmt.Errorf("XXX")
	sampleWebhookError = "rejected: kubeadmiral is too weak"
)

type fakeHTTPClient struct {
	roundTrip func(req *http.Request) *http.Response
	err       error
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.roundTrip(req), nil
}

var _ HTTPClient = &fakeHTTPClient{}

func doTest[T any](
	t *testing.T,
	path string,
	checkReq func(g gomega.Gomega, req *T),
	requestErr error,
	respStatusCode *int,
	// if provided, will be used instead of marshalling resp
	respBody string,
	resp any,
	runPluginAndCheckResults func(g gomega.Gomega, plugin *WebhookPlugin),
) {
	t.Helper()
	g := gomega.NewWithT(t)

	client := &fakeHTTPClient{
		err: requestErr,
		roundTrip: func(httpReq *http.Request) *http.Response {
			// Verify the plugin sends the request correctly
			g.Expect(httpReq.Method).To(gomega.Equal(http.MethodPost))
			g.Expect(httpReq.Header.Get("Content-Type")).To(gomega.Equal("application/json"))
			g.Expect(httpReq.Header.Get("Accept")).To(gomega.Equal("application/json"))
			g.Expect(httpReq.Header.Get("User-Agent")).To(gomega.Equal("kubeadmiral-scheduler"))
			g.Expect(httpReq.URL.Path).To(gomega.Equal(path))

			req := new(T)
			err := json.NewDecoder(httpReq.Body).Decode(req)
			g.Expect(err).NotTo(gomega.HaveOccurred())

			checkReq(g, req)

			var respBytes []byte
			if respBody != "" {
				respBytes = []byte(respBody)
			} else {
				respBytes, err = json.Marshal(&resp)
				g.Expect(err).NotTo(gomega.HaveOccurred())
			}

			statusCode := http.StatusOK
			if respStatusCode != nil {
				statusCode = *respStatusCode
			}
			return &http.Response{
				StatusCode: statusCode,
				Body:       io.NopCloser(bytes.NewReader(respBytes)),
			}
		},
	}

	plugin := NewWebhookPlugin(
		"test",
		"",
		filterPath,
		scorePath,
		selectPath,
		client,
	)

	// Verify the plugin processes webhook responses correctly
	runPluginAndCheckResults(g, plugin)
}

type webhookErrors struct {
	// error returned by the http client
	requestError error
	// non-200 response returned by the webhook
	responseStatusCode *int
	responseBody       string
	// error field in the webhook response
	responseError string
}

func (e *webhookErrors) expectedMessage() string {
	switch {
	case e.requestError != nil:
		return fmt.Sprintf("request failed: %v", e.requestError)
	case e.responseStatusCode != nil && *e.responseStatusCode != http.StatusOK:
		return fmt.Sprintf("unexpected status code: %d, body: %s", *e.responseStatusCode, e.responseBody)
	case e.responseError != "":
		return e.responseError
	default:
		return ""
	}
}

func TestFilter(t *testing.T) {
	testCases := map[string]struct {
		webhookErrors

		// args
		su      *framework.SchedulingUnit
		cluster *fedcorev1a1.FederatedCluster

		// result
		selected bool
	}{
		"webhook selects cluster": {
			su:       getSampleSchedulingUnit(),
			cluster:  getSampleCluster("test"),
			selected: true,
		},
		"webhook does not select cluster": {
			webhookErrors: webhookErrors{
				responseError: "cluster(s) were filtered by webhookPlugin(test)",
			},
			su:       getSampleSchedulingUnit(),
			cluster:  getSampleCluster("test"),
			selected: false,
		},
		"webhook returns 200 response with error": {
			webhookErrors: webhookErrors{
				responseError: sampleWebhookError,
			},
			su:       getSampleSchedulingUnit(),
			cluster:  getSampleCluster("test"),
			selected: false,
		},
		"webhook returns non-200 response": {
			webhookErrors: webhookErrors{
				responseStatusCode: pointer.Int(http.StatusInternalServerError),
				responseBody:       "XXX",
			},
			su:      getSampleSchedulingUnit(),
			cluster: getSampleCluster("test"),
		},
		"request error": {
			webhookErrors: webhookErrors{
				requestError: errClientSample,
			},
			su:      getSampleSchedulingUnit(),
			cluster: getSampleCluster("test"),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			doTest(
				t,
				filterPath,
				func(g gomega.Gomega, req *schedwebhookv1a1.FilterRequest) {
					g.Expect(req.SchedulingUnit).To(custommatchers.SemanticallyEqual(*ConvertSchedulingUnit(tc.su)))
					g.Expect(req.Cluster).To(custommatchers.SemanticallyEqual(*tc.cluster))
				},
				tc.webhookErrors.requestError,
				tc.webhookErrors.responseStatusCode,
				tc.webhookErrors.responseBody,
				schedwebhookv1a1.FilterResponse{
					Selected: tc.selected,
					Error:    tc.webhookErrors.responseError,
				},
				func(g gomega.Gomega, plugin *WebhookPlugin) {
					result := plugin.Filter(getPluginContext(), tc.su, tc.cluster)

					actualMessage := result.Message()
					expectedMessage := tc.expectedMessage()
					g.Expect(actualMessage).To(gomega.Equal(expectedMessage))

					if expectedMessage == "" {
						// result should be expected
						g.Expect(result.IsSuccess()).To(gomega.Equal(tc.selected))
					}
				},
			)
		})
	}
}

func TestScore(t *testing.T) {
	testCases := map[string]struct {
		webhookErrors

		// args
		su      *framework.SchedulingUnit
		cluster *fedcorev1a1.FederatedCluster

		// result
		score int64
	}{
		"webhook returns score": {
			su:      getSampleSchedulingUnit(),
			cluster: getSampleCluster("test"),
			score:   5,
		},
		"webhook returns 200 response with error": {
			webhookErrors: webhookErrors{
				responseError: sampleWebhookError,
			},
			su:      getSampleSchedulingUnit(),
			cluster: getSampleCluster("test"),
		},
		"webhook returns non-200 response": {
			webhookErrors: webhookErrors{
				responseStatusCode: pointer.Int(http.StatusInternalServerError),
				responseBody:       "YYY",
			},
			su:      getSampleSchedulingUnit(),
			cluster: getSampleCluster("test"),
		},
		"request error": {
			webhookErrors: webhookErrors{
				requestError: errClientSample,
			},
			su:      getSampleSchedulingUnit(),
			cluster: getSampleCluster("test"),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			doTest(
				t,
				scorePath,
				func(g gomega.Gomega, req *schedwebhookv1a1.FilterRequest) {
					g.Expect(req.SchedulingUnit).To(custommatchers.SemanticallyEqual(*ConvertSchedulingUnit(tc.su)))
					g.Expect(req.Cluster).To(custommatchers.SemanticallyEqual(*tc.cluster))
				},
				tc.webhookErrors.requestError,
				tc.webhookErrors.responseStatusCode,
				tc.webhookErrors.responseBody,
				schedwebhookv1a1.ScoreResponse{
					Score: tc.score,
					Error: tc.responseError,
				},
				func(g gomega.Gomega, plugin *WebhookPlugin) {
					score, result := plugin.Score(getPluginContext(), tc.su, tc.cluster)

					actualMessage := result.Message()
					expectedMessage := tc.expectedMessage()
					g.Expect(actualMessage).To(gomega.Equal(expectedMessage))

					if expectedMessage == "" {
						g.Expect(score).To(gomega.Equal(tc.score))
					}
				},
			)
		})
	}
}

func TestSelect(t *testing.T) {
	clusters := []*fedcorev1a1.FederatedCluster{
		getSampleCluster("cluster1"),
		getSampleCluster("cluster2"),
		getSampleCluster("cluster3"),
	}
	clusterScores := make(framework.ClusterScoreList, 0, 3)
	for i, cluster := range clusters {
		clusterScores = append(clusterScores, framework.ClusterScore{
			Cluster: cluster,
			Score:   int64(i + 1),
		})
	}

	testCases := map[string]struct {
		webhookErrors

		// args
		su *framework.SchedulingUnit

		// result
		expectedClusters framework.ClusterScoreList
	}{
		"webhook selects clusters": {
			su:               getSampleSchedulingUnit(),
			expectedClusters: clusterScores[:2],
		},
		"webhook returns 200 response with error": {
			webhookErrors: webhookErrors{
				responseError: sampleWebhookError,
			},
			su:               getSampleSchedulingUnit(),
			expectedClusters: nil,
		},
		"webhook returns non-200 response": {
			webhookErrors: webhookErrors{
				responseStatusCode: pointer.Int(http.StatusInternalServerError),
				responseBody:       "ZZZ",
			},
			su: getSampleSchedulingUnit(),
		},
		"request error": {
			webhookErrors: webhookErrors{
				responseError: sampleWebhookError,
			},
			su:               getSampleSchedulingUnit(),
			expectedClusters: nil,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			selectedClusterNames := make([]string, 0, len(tc.expectedClusters))
			for _, cluster := range tc.expectedClusters {
				selectedClusterNames = append(selectedClusterNames, cluster.Cluster.Name)
			}

			doTest(
				t,
				selectPath,
				func(g gomega.Gomega, req *schedwebhookv1a1.SelectRequest) {
					g.Expect(req.SchedulingUnit).To(custommatchers.SemanticallyEqual(*ConvertSchedulingUnit(tc.su)))
					expectedClusterScores := make([]schedwebhookv1a1.ClusterScore, 0, len(clusterScores))
					for _, cluster := range clusterScores {
						expectedClusterScores = append(expectedClusterScores, schedwebhookv1a1.ClusterScore{
							Cluster: *cluster.Cluster,
							Score:   cluster.Score,
						})
					}
					g.Expect(req.ClusterScores).To(custommatchers.SemanticallyEqual(expectedClusterScores))
				},
				tc.webhookErrors.requestError,
				tc.webhookErrors.responseStatusCode,
				tc.webhookErrors.responseBody,
				schedwebhookv1a1.SelectResponse{
					SelectedClusterNames: selectedClusterNames,
					Error:                tc.responseError,
				},
				func(g gomega.Gomega, plugin *WebhookPlugin) {
					selectedClusters, result := plugin.SelectClusters(getPluginContext(), tc.su, clusterScores)

					actualMessage := result.Message()
					expectedMessage := tc.expectedMessage()
					g.Expect(actualMessage).To(gomega.Equal(expectedMessage))

					if expectedMessage == "" {
						g.Expect(selectedClusters).To(custommatchers.SemanticallyEqual(tc.expectedClusters))
					}
				},
			)
		})
	}
}

func getSampleSchedulingUnit() *framework.SchedulingUnit {
	return &framework.SchedulingUnit{
		Name:      "test",
		Namespace: "test",
		GroupVersion: schema.GroupVersion{
			Group:   "apps",
			Version: "v1",
		},
		Kind:     "Deployment",
		Resource: "deployments",
		Labels: map[string]string{
			"test-label-1-name": "test-label-1-value",
			"test-label-2-name": "test-label-2-value",
		},
		Annotations: map[string]string{
			"test-annotation-1-name": "test-annotation-1-value",
			"test-annotation-2-name": "test-annotation-2-value",
		},
		DesiredReplicas: nil,
		ResourceRequest: framework.Resource{},
		CurrentClusters: nil,
		SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
		StickyCluster:   false,
		ClusterSelector: nil,
		ClusterNames:    nil,
		Affinity:        nil,
		Tolerations:     nil,
		MaxClusters:     nil,
	}
}

func getSampleCluster(name string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: fedcorev1a1.FederatedClusterSpec{
			APIEndpoint: "https://test-cluster",
			SecretRef: fedcorev1a1.LocalSecretReference{
				Name: "test-cluster-secret",
			},
			Taints: []corev1.Taint{
				{
					Key:    "test-taint-1-name",
					Value:  "test-taint-1-value",
					Effect: corev1.TaintEffectNoSchedule,
				},
			},
		},
		Status: fedcorev1a1.FederatedClusterStatus{
			Resources: fedcorev1a1.Resources{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(10000, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(10000, resource.BinarySI),
				},
				Available: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(5000, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(5000, resource.BinarySI),
				},
			},
		},
	}
}

func getPluginContext() context.Context {
	return klog.NewContext(context.Background(), logr.Discard())
}
