package resource

import (
	"sync"

	"k8s.io/component-base/metrics"
)

var (
	metricFreshness = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Namespace: "metrics_server",
			Subsystem: "api",
			Name:      "metric_freshness_seconds",
			Help:      "Freshness of metrics exported",
			Buckets:   metrics.ExponentialBuckets(1, 1.364, 20),
		},
		[]string{},
	)

	registerIntoLegacyRegistryOnce sync.Once
)

// RegisterAPIMetrics registers a histogram metric for the freshness of
// exported metrics.
func RegisterAPIMetrics(registrationFunc func(metrics.Registerable) error) error {
	return registrationFunc(metricFreshness)
}
