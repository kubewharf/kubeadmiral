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

package prometheusstats

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

func New(namespace string, addr string, port uint16, quantiles map[float64]float64) stats.Metrics {
	registry := legacyregistry.Registerer()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(legacyregistry.DefaultGatherer, promhttp.HandlerOpts{Registry: registry}))

	server := &http.Server{
		Addr:              net.JoinHostPort(addr, fmt.Sprint(port)),
		ReadHeaderTimeout: time.Second * 10,
		ReadTimeout:       time.Second * 10,
		Handler:           mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			klog.Errorf("metric server error: %v", err)
		}
	}()

	return &promMetrics{
		factory:   promauto.With(registry),
		namespace: namespace,
		quantiles: quantiles,
	}
}

type promMetrics struct {
	factory   promauto.Factory
	namespace string
	quantiles map[float64]float64
	onceMap   sync.Map
}

type once struct {
	once     sync.Once
	gauge    *prometheus.GaugeVec
	counter  *prometheus.CounterVec
	summary  *prometheus.SummaryVec
	tagNames []string
}

func initOnce(metrics *promMetrics, name string, tags []stats.Tag, init func(once *once, tags []string)) (*once, []string) {
	objAny, _ := metrics.onceMap.LoadOrStore(name, &once{})
	obj := objAny.(*once)

	tagNames := make([]string, len(tags))
	tagValues := make([]string, len(tags))
	for i, tag := range tags {
		tagNames[i] = tag.Name
		tagValues[i] = tag.Value
	}

	obj.once.Do(func() {
		init(obj, tagNames)
		obj.tagNames = tagNames
	})

	if !reflect.DeepEqual(tagNames, obj.tagNames) {
		panic(fmt.Sprintf("inconsistent tag name order for %q: %v, %v", name, tagNames, obj.tagNames))
	}

	return obj, tagValues
}

func valToFloat64(val interface{}) float64 {
	switch v := val.(type) {
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		return v
	default:
		panic("unsupported metric value type")
	}
}

func (metrics *promMetrics) Store(name string, val interface{}, tags ...stats.Tag) {
	once, tagValues := initOnce(metrics, name, tags, func(once *once, tagNames []string) {
		once.gauge = metrics.factory.NewGaugeVec(prometheus.GaugeOpts{Name: name, Namespace: metrics.namespace}, tagNames)
	})
	once.gauge.WithLabelValues(tagValues...).Set(valToFloat64(val))
}

func (metrics *promMetrics) Counter(name string, val interface{}, tags ...stats.Tag) {
	once, tagValues := initOnce(metrics, name, tags, func(once *once, tagNames []string) {
		once.counter = metrics.factory.NewCounterVec(prometheus.CounterOpts{Name: name, Namespace: metrics.namespace}, tagNames)
	})
	once.counter.WithLabelValues(tagValues...).Add(valToFloat64(val))
}

func (metrics *promMetrics) Summary(name string, val interface{}, tags ...stats.Tag) {
	once, tagValues := initOnce(metrics, name, tags, func(once *once, tagNames []string) {
		once.summary = metrics.factory.NewSummaryVec(prometheus.SummaryOpts{
			Name:       name,
			Namespace:  metrics.namespace,
			Objectives: metrics.quantiles,
		}, tagNames)
	})
	once.summary.WithLabelValues(tagValues...).Observe(valToFloat64(val))
}

func (metrics *promMetrics) Duration(name string, start time.Time, tags ...stats.Tag) {
	duration := time.Since(start).Seconds()
	key := fmt.Sprintf("%s_seconds", name)
	metrics.Summary(key, duration, tags...)
}
