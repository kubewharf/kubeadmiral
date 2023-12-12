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
	"net/url"
	"time"

	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	schedwebhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

var (
	_ framework.FilterPlugin = &WebhookPlugin{}
	_ framework.ScorePlugin  = &WebhookPlugin{}
	_ framework.SelectPlugin = &WebhookPlugin{}
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type WebhookPlugin struct {
	name       string
	urlPrefix  string
	filterPath string
	scorePath  string
	selectPath string
	client     HTTPClient
}

func NewWebhookPlugin(
	name string,
	urlPrefix string,
	filterPath string,
	scorePath string,
	selectPath string,
	client HTTPClient,
) *WebhookPlugin {
	return &WebhookPlugin{
		name:       name,
		urlPrefix:  urlPrefix,
		filterPath: filterPath,
		scorePath:  scorePath,
		selectPath: selectPath,
		client:     client,
	}
}

func (p *WebhookPlugin) Name() string {
	return p.name
}

func (p *WebhookPlugin) doRequest(
	ctx context.Context,
	path string,
	body any,
	response any,
) error {
	url, err := url.JoinPath(p.urlPrefix, path)
	if err != nil {
		return fmt.Errorf("failed to join URL path: %w", err)
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("User-Agent", "kubeadmiral-scheduler")

	logger := klog.FromContext(ctx).WithValues("url", url)
	logger.V(4).Info("Sending request to webhook")
	start := time.Now()

	httpResp, err := p.client.Do(req)
	logger = logger.WithValues("duration", time.Since(start))
	if err != nil {
		logger.Error(err, "Webhook request failed")
		return fmt.Errorf("request failed: %w", err)
	}
	logger = logger.WithValues("status", httpResp.StatusCode)
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			logger.Error(err, "Received non-200 response from webhook and failed to read body")
			return fmt.Errorf("failed to read response body: %w", err)
		}
		logger.Error(nil, "Received non-200 response from webhook", "body", string(body))
		return fmt.Errorf("unexpected status code: %d, body: %s", httpResp.StatusCode, string(body))
	}

	err = json.NewDecoder(httpResp.Body).Decode(response)
	if err != nil {
		logger.Error(err, "Failed to decode response from webhook")
		return fmt.Errorf("failed to decode response: %w", err)
	}
	if logger.V(4).Enabled() {
		logger.Info("Received response from webhook", "response", response)
	}
	return nil
}

func (p *WebhookPlugin) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	if p.filterPath == "" {
		return framework.NewResult(framework.Error, "filter is not supported by the webhook")
	}

	logger := klog.FromContext(ctx).WithValues("plugin", p.name, "pluginType", "Webhook", "stage", "Filter")
	ctx = klog.NewContext(ctx, logger)

	req := schedwebhookv1a1.FilterRequest{
		SchedulingUnit: *ConvertSchedulingUnit(su),
		Cluster:        *cluster,
	}
	resp := schedwebhookv1a1.FilterResponse{}
	if err := p.doRequest(ctx, p.filterPath, &req, &resp); err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	if len(resp.Error) > 0 {
		return framework.NewResult(framework.Error, resp.Error)
	}

	if resp.Selected {
		return framework.NewResult(framework.Success)
	} else {
		return framework.NewResult(framework.Unschedulable, fmt.Sprintf("cluster(s) were filtered by webhookPlugin(%s)", p.name))
	}
}

func (p *WebhookPlugin) Score(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (int64, *framework.Result) {
	if p.scorePath == "" {
		return 0, framework.NewResult(framework.Error, "score is not supported by the webhook")
	}

	logger := klog.FromContext(ctx).WithValues("plugin", p.name, "pluginType", "Webhook", "stage", "Score")
	ctx = klog.NewContext(ctx, logger)

	req := schedwebhookv1a1.ScoreRequest{
		SchedulingUnit: *ConvertSchedulingUnit(su),
		Cluster:        *cluster,
	}
	resp := schedwebhookv1a1.ScoreResponse{}
	if err := p.doRequest(ctx, p.scorePath, &req, &resp); err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	if len(resp.Error) > 0 {
		return 0, framework.NewResult(framework.Error, resp.Error)
	}

	return resp.Score, framework.NewResult(framework.Success)
}

func (p *WebhookPlugin) NormalizeScore(ctx context.Context, scores framework.ClusterScoreList) *framework.Result {
	// TODO: should we enforce normalization for all plugins by default?
	return framework.DefaultNormalizeScore(framework.MaxClusterScore, false, scores)
}

func (p *WebhookPlugin) ScoreExtensions() framework.ScoreExtensions {
	return p
}

func (p *WebhookPlugin) SelectClusters(
	ctx context.Context,
	su *framework.SchedulingUnit,
	clusterScores framework.ClusterScoreList,
) (framework.ClusterScoreList, *framework.Result) {
	if p.selectPath == "" {
		return nil, framework.NewResult(framework.Error, "select is not supported by the webhook")
	}

	logger := klog.FromContext(ctx).WithValues("plugin", p.name, "pluginType", "Webhook", "stage", "Select")
	ctx = klog.NewContext(ctx, logger)

	req := schedwebhookv1a1.SelectRequest{
		SchedulingUnit: *ConvertSchedulingUnit(su),
		ClusterScores:  []schedwebhookv1a1.ClusterScore{},
	}

	clusterMap := map[string]framework.ClusterScore{}
	for _, score := range clusterScores {
		clusterMap[score.Cluster.Name] = score

		req.ClusterScores = append(req.ClusterScores, schedwebhookv1a1.ClusterScore{
			Cluster: *score.Cluster,
			Score:   score.Score,
		})
	}

	resp := schedwebhookv1a1.SelectResponse{}
	if err := p.doRequest(ctx, p.selectPath, &req, &resp); err != nil {
		return nil, framework.NewResult(framework.Error, err.Error())
	}

	if len(resp.Error) > 0 {
		return nil, framework.NewResult(framework.Error, resp.Error)
	}

	var selectedClusters framework.ClusterScoreList
	for _, clusterName := range resp.SelectedClusterNames {
		if cluster, ok := clusterMap[clusterName]; ok {
			selectedClusters = append(selectedClusters, cluster)
		} else {
			return nil, framework.NewResult(
				framework.Error,
				fmt.Sprintf("cluster %q was not in the request sent to the webhook", clusterName),
			)
		}
	}

	return selectedClusters, framework.NewResult(framework.Success)
}
