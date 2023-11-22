package resource

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/metrics"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/hpaaggregatorapiserver/aggregatedlister"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type metricsGetter struct {
	federatedInformerManager informermanager.FederatedInformerManager
	logger                   klog.Logger
}

var _ MetricsGetter = &metricsGetter{}

var (
	podMetricsGVR  = metricsv1beta1.SchemeGroupVersion.WithResource("pods")
	nodeMetricsGVR = metricsv1beta1.SchemeGroupVersion.WithResource("nodes")
)

func NewMetricsGetter(
	informer informermanager.FederatedInformerManager,
	logger klog.Logger,
) MetricsGetter {
	return &metricsGetter{federatedInformerManager: informer, logger: logger}
}

func (m *metricsGetter) GetPodMetrics(pods ...*metav1.PartialObjectMetadata) ([]metrics.PodMetrics, error) {
	readyClusters, err := m.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	clusters := make(map[string][]common.QualifiedName, len(pods))
	for _, pod := range pods {
		cluster := pod.Annotations[aggregatedlister.ClusterNameAnnotationKey]
		rawName := pod.Annotations[aggregatedlister.RawNameAnnotationKey]
		if cluster != "" && rawName != "" {
			clusters[cluster] = append(clusters[cluster], common.QualifiedName{
				Namespace: pod.Namespace,
				Name:      rawName,
			})
		}
	}

	var result []metrics.PodMetrics
	var wg sync.WaitGroup
	var lock sync.Mutex
	ctx := context.Background()
	for _, cluster := range readyClusters {
		pods := clusters[cluster.Name]
		if len(pods) == 0 {
			continue
		}

		wg.Add(1)
		go func(clusterName string) {
			defer wg.Done()
			logger := m.logger.WithValues("cluster", clusterName)

			client, ok := m.federatedInformerManager.GetClusterDynamicClient(clusterName)
			if !ok {
				return
			}
			clusterMetrics := make([]metrics.PodMetrics, 0, len(pods))
			for _, pod := range pods {
				unsMetric, err := client.Resource(podMetricsGVR).
					Namespace(pod.Namespace).
					Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					logger.V(4).Info("Failed reading pod metrics", "pod", pod, "err", err)
					continue
				}

				podMetric, err := podMetricsConverter(unsMetric, clusterName)
				if err != nil {
					logger.V(4).Info("Failed converting pod metric", "pod", pod, "err", err)
					continue
				}
				clusterMetrics = append(clusterMetrics, podMetric)
			}

			lock.Lock()
			defer lock.Unlock()
			result = append(result, clusterMetrics...)
		}(cluster.Name)
	}

	wg.Wait()
	return result, nil
}

func (m *metricsGetter) GetNodeMetrics(nodes ...*corev1.Node) ([]metrics.NodeMetrics, error) {
	readyClusters, err := m.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	clusters := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		cluster := node.Annotations[aggregatedlister.ClusterNameAnnotationKey]
		rawName := node.Annotations[aggregatedlister.RawNameAnnotationKey]
		if cluster != "" && rawName != "" {
			clusters[cluster] = append(clusters[cluster], rawName)
		}
	}

	var result []metrics.NodeMetrics
	var wg sync.WaitGroup
	var lock sync.Mutex
	ctx := context.Background()
	for _, cluster := range readyClusters {
		nodes := clusters[cluster.Name]
		if len(nodes) == 0 {
			continue
		}

		wg.Add(1)
		go func(clusterName string) {
			defer wg.Done()
			logger := m.logger.WithValues("cluster", clusterName)

			client, ok := m.federatedInformerManager.GetClusterDynamicClient(clusterName)
			if !ok {
				return
			}
			clusterMetrics := make([]metrics.NodeMetrics, 0, len(nodes))
			for _, node := range nodes {
				unsMetric, err := client.Resource(nodeMetricsGVR).Get(ctx, node, metav1.GetOptions{})
				if err != nil {
					logger.V(4).Info("Failed reading node metrics", "node", node, "err", err)
					continue
				}

				nodeMetric, err := nodeMetricsConverter(unsMetric, clusterName)
				if err != nil {
					logger.V(4).Info("Failed converting node metric", "node", node, "err", err)
					continue
				}
				clusterMetrics = append(clusterMetrics, nodeMetric)
			}

			lock.Lock()
			defer lock.Unlock()
			result = append(result, clusterMetrics...)
		}(cluster.Name)
	}

	wg.Wait()
	return result, nil
}

func podMetricsConverter(uns *unstructured.Unstructured, clusterName string) (metrics.PodMetrics, error) {
	result := metrics.PodMetrics{}
	metric := &metricsv1beta1.PodMetrics{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uns.Object, metric); err != nil {
		return result, err
	}
	aggregatedlister.MakeObjectUnique(metric, clusterName)
	if err := metricsv1beta1.Convert_v1beta1_PodMetrics_To_metrics_PodMetrics(metric, &result, nil); err != nil {
		return result, err
	}
	return result, nil
}

func nodeMetricsConverter(uns *unstructured.Unstructured, clusterName string) (metrics.NodeMetrics, error) {
	result := metrics.NodeMetrics{}
	metric := &metricsv1beta1.NodeMetrics{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uns.Object, metric); err != nil {
		return result, err
	}
	aggregatedlister.MakeObjectUnique(metric, clusterName)
	if err := metricsv1beta1.Convert_v1beta1_NodeMetrics_To_metrics_NodeMetrics(metric, &result, nil); err != nil {
		return result, err
	}
	return result, nil
}
