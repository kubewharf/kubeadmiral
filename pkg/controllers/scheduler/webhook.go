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
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	schedwebhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
	pluginv1a1 "github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/extensions/webhook/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/runtime"
)

// SchedulerSupportedPayloadVersions is the list of payload versions supported by the scheduler.
var SchedulerSupportedPayloadVersions = sets.New(
	schedwebhookv1a1.PayloadVersion,
)

func (s *Scheduler) cacheWebhookPlugin(config *fedcorev1a1.SchedulerPluginWebhookConfiguration) {
	logger := s.logger.WithValues("origin", "webhookEventHandler", "name", config.Name)

	// Find the most preferred payload version that is supported by the scheduler.
	var payloadVersion string
	for _, version := range config.Spec.PayloadVersions {
		if SchedulerSupportedPayloadVersions.Has(version) {
			payloadVersion = version
			break
		}
	}
	if len(payloadVersion) == 0 {
		msg := fmt.Sprintf(
			"Failed to resolve payload version: no supported payload version found, webhook supports %v, scheduler supports %v",
			config.Spec.PayloadVersions, SchedulerSupportedPayloadVersions.UnsortedList(),
		)
		logger.Error(nil, msg)
		s.eventRecorder.Event(
			config,
			corev1.EventTypeWarning,
			EventReasonWebhookConfigurationError,
			msg,
		)
		return
	}

	transport, err := makeTransport(&config.Spec)
	if err != nil {
		logger.Error(err, "Failed to create webhook transport")
		s.eventRecorder.Eventf(
			config,
			corev1.EventTypeWarning,
			EventReasonWebhookConfigurationError,
			"Failed to create webhook transport: %v",
			err,
		)
		return
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   config.Spec.HTTPTimeout.Duration,
	}

	var plugin framework.Plugin
	switch payloadVersion {
	case schedwebhookv1a1.PayloadVersion:
		plugin = pluginv1a1.NewWebhookPlugin(
			config.Name, config.Spec.URLPrefix,
			config.Spec.FilterPath, config.Spec.ScorePath, config.Spec.SelectPath,
			client,
		)
	default:
		// this should not happen
		utilruntime.HandleError(fmt.Errorf("unknown payload version %q", payloadVersion))
		return
	}
	s.webhookPlugins.Store(config.Name, plugin)
}

func makeTransport(config *fedcorev1a1.SchedulerPluginWebhookConfigurationSpec) (http.RoundTripper, error) {
	var restConfig rest.Config
	if config.TLSConfig != nil {
		restConfig.TLSClientConfig.Insecure = config.TLSConfig.Insecure
		restConfig.TLSClientConfig.ServerName = config.TLSConfig.ServerName
		restConfig.TLSClientConfig.CertData = config.TLSConfig.CertData
		restConfig.TLSClientConfig.KeyData = config.TLSConfig.KeyData
		restConfig.TLSClientConfig.CAData = config.TLSConfig.CAData
	}
	tlsConfig, err := rest.TLSConfigFor(&restConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating TLS config: %w", err)
	}
	return utilnet.SetTransportDefaults(&http.Transport{TLSClientConfig: tlsConfig}), nil
}

func (s *Scheduler) webhookPluginRegistry() (runtime.Registry, error) {
	registry := runtime.Registry{}

	var err error
	s.webhookPlugins.Range(func(name, plugin any) bool {
		err = registry.Register(name.(string), func(_ framework.Handle) (framework.Plugin, error) {
			return plugin.(framework.Plugin), nil
		})
		return err == nil
	})

	return registry, err
}
