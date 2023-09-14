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

package federatedcluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextv1b1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/discovery"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
)

const (
	ClusterReadyReason  = "ClusterReady"
	ClusterReadyMessage = "Cluster is ready"

	ClusterHealthzNotOKReason  = "HealthzNotOK"
	ClusterHealthzNotOKMessage = "/healthz responded without ok"

	ClusterResourceCollectionFailedReason          = "ClusterResourceCollectionFailed"
	ClusterResourceCollectionFailedMessageTemplate = "Failed to collect cluster resources: %v"

	ClusterAPIDiscoveryFailedReason          = "ClusterAPIDiscoveryFailed"
	ClusterAPIDiscoveryFailedMessageTemplate = "Failed to discover cluster API resources: %v"

	ClusterReachableReason    = "ClusterReachable"
	ClusterReachableMsg       = "Cluster is reachable"
	ClusterNotReachableReason = "ClusterNotReachable"
	ClusterNotReachableMsg    = "Cluster is not reachable"
)

func (c *FederatedClusterController) collectIndividualClusterStatus(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
) (retryAfter time.Duration, err error) {
	startTime := time.Now()
	defer func() {
		c.recordClusterStatus(cluster, startTime)
	}()
	logger := klog.FromContext(ctx)

	clusterKubeClient, exists := c.federatedInformerManager.GetClusterKubeClient(cluster.Name)
	if !exists {
		return 0, fmt.Errorf("failed to get cluster client: FederatedInformerManager not yet up-to-date")
	}
	podLister, podsSynced, exists := c.federatedInformerManager.GetPodLister(cluster.Name)
	if !exists {
		return 0, fmt.Errorf("failed to get pod lister: FederatedInformerManager not yet up-to-date")
	}
	nodeLister, nodesSynced, exists := c.federatedInformerManager.GetNodeLister(cluster.Name)
	if !exists {
		return 0, fmt.Errorf("failed to get node lister: FederatedInformerManager not yet up-to-date")
	}

	discoveryClient := clusterKubeClient.Discovery()

	oldClusterStatus := cluster.Status.DeepCopy()
	cluster = cluster.DeepCopy()
	conditionTime := metav1.Now()

	offlineStatus, readyStatus := checkReadyByHealthz(ctx, discoveryClient)
	var readyReason, readyMessage string
	switch readyStatus {
	case corev1.ConditionTrue:
		readyReason = ClusterReadyReason
		readyMessage = ClusterReadyMessage
	case corev1.ConditionFalse:
		readyReason = ClusterHealthzNotOKReason
		readyMessage = ClusterHealthzNotOKMessage
	case corev1.ConditionUnknown:
		readyReason = ClusterNotReachableReason
		readyMessage = ClusterNotReachableMsg
	}

	// We skip updating cluster resources and api resources if cluster is not ready
	if readyStatus == corev1.ConditionTrue {
		if err := c.updateClusterResources(
			ctx,
			&cluster.Status,
			podLister,
			podsSynced,
			nodeLister,
			nodesSynced,
		); err != nil {
			logger.Error(err, "Failed to update cluster resources")
			readyStatus = corev1.ConditionFalse
			readyReason = ClusterResourceCollectionFailedReason
			readyMessage = fmt.Sprintf(ClusterResourceCollectionFailedMessageTemplate, err.Error())
		} else if err := updateClusterAPIResources(ctx, &cluster.Status, discoveryClient); err != nil {
			logger.Error(err, "Failed to update cluster api resources")
			readyStatus = corev1.ConditionFalse
			readyReason = ClusterAPIDiscoveryFailedReason
			readyMessage = fmt.Sprintf(ClusterAPIDiscoveryFailedMessageTemplate, err.Error())
		}
	}

	offlineCondition := getNewClusterOfflineCondition(offlineStatus, conditionTime)
	setClusterCondition(&cluster.Status, &offlineCondition)
	readyCondition := getNewClusterReadyCondition(readyStatus, readyReason, readyMessage, conditionTime)
	setClusterCondition(&cluster.Status, &readyCondition)

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		latestCluster, err := c.fedClient.CoreV1alpha1().FederatedClusters().Get(
			context.TODO(),
			cluster.Name,
			metav1.GetOptions{},
		)
		if err != nil {
			return err
		}
		cluster.Status.DeepCopyInto(&latestCluster.Status)
		_, err = c.fedClient.CoreV1alpha1().FederatedClusters().UpdateStatus(
			context.TODO(),
			latestCluster,
			metav1.UpdateOptions{},
		)
		return err
	}); err != nil {
		return 0, fmt.Errorf("failed to update cluster status: %w", err)
	}

	if isReadyStatusChanged(oldClusterStatus, readyStatus) {
		switch readyStatus {
		case corev1.ConditionTrue:
			c.eventRecorder.Eventf(cluster, corev1.EventTypeNormal, readyReason, readyMessage)
		case corev1.ConditionFalse, corev1.ConditionUnknown:
			c.eventRecorder.Eventf(cluster, corev1.EventTypeWarning, readyReason, readyMessage)
		}
	}

	return 0, nil
}

func isReadyStatusChanged(clusterStatus *fedcorev1a1.FederatedClusterStatus, readyStatus corev1.ConditionStatus) bool {
	for _, condition := range clusterStatus.Conditions {
		if condition.Type == fedcorev1a1.ClusterReady {
			if condition.Status == readyStatus {
				return false
			} else {
				return true
			}
		}
	}

	return true
}

func checkReadyByHealthz(
	ctx context.Context,
	clusterDiscoveryClient discovery.DiscoveryInterface,
) (offline, ready corev1.ConditionStatus) {
	logger := klog.FromContext(ctx)

	body, err := clusterDiscoveryClient.RESTClient().Get().AbsPath("/healthz").Timeout(30 * time.Second).Do(ctx).Raw()
	if err != nil {
		logger.Error(err, "Cluster health check failed")
		return corev1.ConditionTrue, corev1.ConditionUnknown
	}

	var clusterReadyStatus corev1.ConditionStatus
	if strings.EqualFold(string(body), "ok") {
		clusterReadyStatus = corev1.ConditionTrue
	} else {
		logger.V(3).WithValues("body", body).Info("Cluster is not ready")
		clusterReadyStatus = corev1.ConditionFalse
	}
	return corev1.ConditionFalse, clusterReadyStatus
}

func (c *FederatedClusterController) updateClusterResources(
	ctx context.Context,
	clusterStatus *fedcorev1a1.FederatedClusterStatus,
	podLister corev1listers.PodLister,
	podsSynced cache.InformerSynced,
	nodeLister corev1listers.NodeLister,
	nodesSynced cache.InformerSynced,
) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !cache.WaitForCacheSync(ctx.Done(), podsSynced, nodesSynced) {
		return fmt.Errorf("timeout waiting for node and pod informer sync")
	}

	nodes, err := nodeLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}
	pods, err := podLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	schedulableNodes := int64(0)
	for _, node := range nodes {
		if isNodeSchedulable(node) && !c.isNodeFiltered(node) {
			schedulableNodes++
		}
	}

	allocatable, available := c.aggregateResources(nodes, pods)
	clusterStatus.Resources = fedcorev1a1.Resources{
		SchedulableNodes: &schedulableNodes,
		Allocatable:      allocatable,
		Available:        available,
	}

	return nil
}

func updateClusterAPIResources(
	ctx context.Context,
	clusterStatus *fedcorev1a1.FederatedClusterStatus,
	clusterDiscoveryClient discovery.DiscoveryInterface,
) error {
	logger := klog.FromContext(ctx)

	_, apiResourceLists, err := clusterDiscoveryClient.ServerGroupsAndResources()
	if err != nil {
		if len(apiResourceLists) == 0 {
			return fmt.Errorf("failed to list cluster api resources: %w", err)
		}

		// the returned lists might be non-nil with partial results even in the case of non-nil error.
		logger.Error(err, "failed to list all cluster api resources")
	}

	resources := []fedcorev1a1.APIResource{}

	for _, apiResourceList := range apiResourceLists {
		groupVersion := strings.Split(apiResourceList.GroupVersion, "/")

		var group, version string
		switch len(groupVersion) {
		case 1:
			// for legacy group resources whose group version format be like v1
			version = groupVersion[0]
		case 2:
			// for new group resources whose group version format be like apps/v1
			group = groupVersion[0]
			version = groupVersion[1]
		}

		for _, apiResource := range apiResourceList.APIResources {
			// subresources such as "/status", "/scale" need to be skipped as they are not real APIResources that we are caring about.
			if strings.Contains(apiResource.Name, "/") {
				continue
			}

			item := fedcorev1a1.APIResource{
				Group:      apiResource.Group,
				Version:    apiResource.Version,
				Kind:       apiResource.Kind,
				PluralName: apiResource.Name,
				Scope:      apiextv1b1.NamespaceScoped,
			}
			if len(item.Group) == 0 {
				item.Group = group
			}
			if len(item.Version) == 0 {
				item.Version = version
			}
			if !apiResource.Namespaced {
				item.Scope = apiextv1b1.ClusterScoped
			}
			resources = append(resources, item)
		}
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Kind < resources[j].Kind
	})

	clusterStatus.APIResourceTypes = resources
	return nil
}

func shouldCollectClusterStatus(cluster *fedcorev1a1.FederatedCluster, collectInterval time.Duration) bool {
	readyCond := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterReady)
	if readyCond == nil || readyCond.LastProbeTime.IsZero() {
		return true
	}

	nextCollectTime := readyCond.LastProbeTime.Time.Add(collectInterval)
	return time.Now().After(nextCollectTime)
}

func (c *FederatedClusterController) recordClusterStatus(cluster *fedcorev1a1.FederatedCluster, startTime time.Time) {
	readyReason, joinedReason, offlineReason := clusterutil.GetClusterConditionReasons(&cluster.Status)
	if clusterutil.IsClusterReady(&cluster.Status) {
		c.metrics.Store(metrics.ClusterReadyState,
			1,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "reason", Value: readyReason})
	} else {
		c.metrics.Store(metrics.ClusterReadyState,
			0,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "reason", Value: readyReason})
	}
	if clusterutil.IsClusterOffline(&cluster.Status) {
		c.metrics.Store(metrics.ClusterOfflineState,
			1,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "reason", Value: offlineReason})
	} else {
		c.metrics.Store(metrics.ClusterOfflineState,
			0,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "reason", Value: offlineReason})
	}
	if clusterutil.IsClusterJoined(&cluster.Status) {
		c.metrics.Store(metrics.ClusterJoinedState,
			1,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "reason", Value: joinedReason})
	} else {
		c.metrics.Store(metrics.ClusterJoinedState,
			0,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "reason", Value: joinedReason})
	}
	c.metrics.Duration(metrics.ClusterSyncStatusDuration,
		startTime,
		stats.Tag{Name: "cluster_name", Value: cluster.Name})
	if cluster.Status.Resources.Allocatable != nil {
		c.metrics.Store(metrics.ClusterMemoryAllocatableBytes,
			cluster.Status.Resources.Allocatable.Memory().AsApproximateFloat64(),
			stats.Tag{Name: "cluster_name", Value: cluster.Name})
		c.metrics.Store(metrics.ClusterCPUAllocatableNumber,
			cluster.Status.Resources.Allocatable.Cpu().AsApproximateFloat64(),
			stats.Tag{Name: "cluster_name", Value: cluster.Name})
	}
	if cluster.Status.Resources.Available != nil {
		c.metrics.Store(metrics.ClusterMemoryAvailableBytes,
			cluster.Status.Resources.Available.Memory().AsApproximateFloat64(),
			stats.Tag{Name: "cluster_name", Value: cluster.Name})
		c.metrics.Store(metrics.ClusterCPUAvailableNumber,
			cluster.Status.Resources.Available.Cpu().AsApproximateFloat64(),
			stats.Tag{Name: "cluster_name", Value: cluster.Name})
	}
	if cluster.Status.Resources.SchedulableNodes != nil {
		c.metrics.Store(metrics.ClusterSchedulableNodesTotal,
			*cluster.Status.Resources.SchedulableNodes,
			stats.Tag{Name: "cluster_name", Value: cluster.Name})
	}
}
