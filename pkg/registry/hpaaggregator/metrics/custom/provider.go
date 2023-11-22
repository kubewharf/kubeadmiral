package custom

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/custom_metrics/v1beta2"
	custommetricsclient "k8s.io/metrics/pkg/client/custom_metrics"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

var (
	converter = custommetricsclient.NewMetricConverter()

	defaultUpdateInterval = 60 * time.Second
)

const providerName = "custom-metrics-provider"

type CustomMetricsProvider struct {
	federatedInformerManager informermanager.FederatedInformerManager
	updateInterval           time.Duration
	logger                   klog.Logger

	metrics []provider.CustomMetricInfo
	lock    sync.RWMutex
}

var _ provider.CustomMetricsProvider = &CustomMetricsProvider{}

func NewCustomMetricsProvider(
	federatedInformerManager informermanager.FederatedInformerManager,
	updateInterval time.Duration,
	logger klog.Logger,
) *CustomMetricsProvider {
	if updateInterval == 0 {
		updateInterval = defaultUpdateInterval
	}
	p := &CustomMetricsProvider{
		federatedInformerManager: federatedInformerManager,
		updateInterval:           updateInterval,
		logger:                   logger.WithValues("provider", providerName),
	}
	return p
}

func (c *CustomMetricsProvider) newCustomMetricsClient(
	cluster string,
) (discovery.DiscoveryInterface, meta.RESTMapper, custommetricsclient.CustomMetricsClient, error) {
	config, ok := c.federatedInformerManager.GetClusterRestConfig(cluster)
	if !ok {
		return nil, nil, nil, fmt.Errorf("failed to get rest config for %s", cluster)
	}
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get discovery client for %s", cluster)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(config, apiutil.WithLazyDiscovery)
	if err != nil {
		return nil, nil, nil, err
	}
	apiVersionsGetter := custommetricsclient.NewAvailableAPIsGetter(client)

	return client, mapper, custommetricsclient.NewForConfig(config, mapper, apiVersionsGetter), err
}

func (c *CustomMetricsProvider) GetMetricByName(
	ctx context.Context,
	name types.NamespacedName,
	info provider.CustomMetricInfo,
	metricSelector labels.Selector,
) (*custom_metrics.MetricValue, error) {
	clusters, err := c.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	logger := c.logger.WithValues(
		"operation", "get-metrics-by-name",
		"target", name.String(),
		"gr", info.GroupResource.String(),
		"metrics", info.Metric,
	)

	var wg sync.WaitGroup
	var lock sync.Mutex
	collections := map[string]*custom_metrics.MetricValue{}
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()
			logger := logger.WithValues("cluster", cluster)

			_, mapper, client, err := c.newCustomMetricsClient(cluster)
			if err != nil {
				logger.V(4).Info("Failed to get cluster client, won't retry", "err", err)
				return
			}
			// find the target gvk
			gvk, err := mapper.KindFor(info.GroupResource.WithVersion(""))
			if err != nil {
				logger.V(4).Info("Failed to find the GVK, won't retry", "err", err)
				return
			}

			var metric *v1beta2.MetricValue
			if info.Namespaced {
				metric, err = client.NamespacedMetrics(name.Namespace).
					GetForObject(gvk.GroupKind(), name.Name, info.Metric, metricSelector)
			} else {
				metric, err = client.RootScopedMetrics().
					GetForObject(gvk.GroupKind(), name.Name, info.Metric, metricSelector)
			}
			if err != nil {
				logger.V(4).Info("Failed to get the metric, won't retry", "err", err)
				return
			}

			m := &custom_metrics.MetricValue{}
			if err = converter.Scheme().Convert(metric, m, nil); err != nil {
				logger.V(4).Info("Failed to convert metric", "err", err)
				return
			}

			lock.Lock()
			defer lock.Unlock()
			collections[cluster] = m
		}(cluster.Name)
	}

	wg.Wait()
	var result *custom_metrics.MetricValue
	for _, m := range collections {
		if result == nil {
			result = m
			continue
		}
		result.Value.Add(m.Value)
	}
	if result == nil {
		return nil, provider.NewMetricNotFoundForError(info.GroupResource, info.Metric, name.Name)
	}
	result.Value.Set(result.Value.Value() / int64(len(collections)))
	return result, nil
}

func (c *CustomMetricsProvider) GetMetricBySelector(
	ctx context.Context,
	namespace string,
	selector labels.Selector,
	info provider.CustomMetricInfo,
	metricSelector labels.Selector,
) (*custom_metrics.MetricValueList, error) {
	clusters, err := c.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	logger := c.logger.WithValues(
		"operation", "get-metrics-by-selector",
		"namespace", namespace,
		"gr", info.GroupResource.String(),
		"metrics", info.Metric,
	)

	var wg sync.WaitGroup
	var lock sync.Mutex
	collections := map[string]*custom_metrics.MetricValueList{}
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()
			logger := logger.WithValues("cluster", cluster)

			_, mapper, client, err := c.newCustomMetricsClient(cluster)
			if err != nil {
				logger.V(4).Info("Failed to get cluster client, won't retry", "err", err)
				return
			}
			// find the target gvk
			gvk, err := mapper.KindFor(info.GroupResource.WithVersion(""))
			if err != nil {
				logger.V(4).Info("Failed to find the GVK, won't retry", "err", err)
				return
			}

			var metrics *v1beta2.MetricValueList
			if info.Namespaced {
				metrics, err = client.NamespacedMetrics(namespace).
					GetForObjects(gvk.GroupKind(), selector, info.Metric, metricSelector)
			} else {
				metrics, err = client.RootScopedMetrics().
					GetForObjects(gvk.GroupKind(), selector, info.Metric, metricSelector)
			}
			if err != nil {
				logger.V(4).Info("Failed to get the metric list", "err", err)
				return
			}
			if len(metrics.Items) == 0 {
				return
			}

			m := &custom_metrics.MetricValueList{}
			if err = converter.Scheme().Convert(metrics, m, nil); err != nil {
				logger.V(4).Info("Failed to convert metric list", "err", err)
				return
			}

			lock.Lock()
			defer lock.Unlock()
			collections[cluster] = m
		}(cluster.Name)
	}

	wg.Wait()
	var result *custom_metrics.MetricValueList
	for _, m := range collections {
		if result == nil {
			result = m
			continue
		}
		result.Items = append(result.Items, m.Items...)
	}
	if result == nil || len(result.Items) == 0 {
		return nil, provider.NewMetricNotFoundError(info.GroupResource, info.Metric)
	}
	return result, nil
}

func (c *CustomMetricsProvider) ListAllMetrics() []provider.CustomMetricInfo {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.metrics
}

func (c *CustomMetricsProvider) listAllMetrics() (metrics []provider.CustomMetricInfo, success bool) {
	clusters, err := c.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, false
	}

	var wg sync.WaitGroup
	var lock sync.Mutex
	result := sets.Set[provider.CustomMetricInfo]{}
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()

			metricInfos, ok := c.listMetricsInfo(cluster)
			if !ok {
				return
			}

			lock.Lock()
			defer lock.Unlock()
			result.Insert(metricInfos...)
			success = true
		}(cluster.Name)
	}

	wg.Wait()
	metrics = result.UnsortedList()
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].GroupResource.String() == metrics[j].GroupResource.String() {
			return metrics[i].Metric < metrics[j].Metric
		}
		return metrics[i].GroupResource.String() < metrics[j].GroupResource.String()
	})
	return metrics, success
}

func (c *CustomMetricsProvider) RunUntil(stopChan <-chan struct{}) {
	go wait.Until(func() {
		if metrics, ok := c.listAllMetrics(); ok {
			c.lock.Lock()
			defer c.lock.Unlock()

			c.metrics = metrics
		}
	}, c.updateInterval, stopChan)
}

func (c *CustomMetricsProvider) listMetricsInfo(cluster string) (metrics []provider.CustomMetricInfo, success bool) {
	discoveryClient, _, _, err := c.newCustomMetricsClient(cluster)
	if err != nil {
		return nil, false
	}

	resourceList, err := getSupportedCustomMetricsAPIVersion(discoveryClient)
	if err != nil {
		return nil, false
	}

	metrics = make([]provider.CustomMetricInfo, 0, len(resourceList.APIResources))
	for _, resource := range resourceList.APIResources {
		// The resource name consists of GroupResource and Metric
		info := strings.SplitN(resource.Name, "/", 2)
		if len(info) != 2 {
			continue
		}
		metrics = append(metrics, provider.CustomMetricInfo{
			GroupResource: schema.ParseGroupResource(info[0]),
			Namespaced:    resource.Namespaced,
			Metric:        info[1],
		})
	}
	return metrics, true
}

func getSupportedCustomMetricsAPIVersion(
	client discovery.DiscoveryInterface,
) (resources *metav1.APIResourceList, err error) {
	groups, err := client.ServerGroups()
	if err != nil {
		return nil, err
	}

	var apiVersion string
	for _, group := range groups.Groups {
		if group.Name == custom_metrics.GroupName {
			versions := sets.Set[string]{}
			for _, version := range group.Versions {
				versions.Insert(version.Version)
			}
			for _, version := range custommetricsclient.MetricVersions {
				if versions.Has(version.Version) {
					apiVersion = version.String()
					break
				}
			}
			break
		}
	}

	if apiVersion == "" {
		return &metav1.APIResourceList{}, nil
	}
	return client.ServerResourcesForGroupVersion(apiVersion)
}
