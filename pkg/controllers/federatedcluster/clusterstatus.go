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
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/federatedclient"
)

const (
	ClusterReadyReason     = "ClusterReady"
	ClusterReadyMessage    = "/healthz responded with ok"
	ClusterNotReadyReason  = "ClusterNotReady"
	ClusterNotReadyMessage = "/healthz responded without ok"

	ClusterReachableReason    = "ClusterReachable"
	ClusterReachableMsg       = "cluster is reachable"
	ClusterNotReachableReason = "ClusterNotReachable"
	ClusterNotReachableMsg    = "cluster is not reachable"
)

func collectIndividualClusterStatus(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	fedClient fedclient.Interface,
	federatedClient federatedclient.FederatedClientFactory,
	logger klog.Logger,
) error {
	logger = logger.WithValues("sub-process", "status-collection")

	clusterKubeClient, exists, err := federatedClient.KubeClientsetForCluster(cluster.Name)
	if !exists {
		return fmt.Errorf("federated client is not yet up to date")
	}
	if err != nil {
		return fmt.Errorf("failed to get federated kube client: %w", err)
	}
	clusterKubeInformer, exists, err := federatedClient.KubeSharedInformerFactoryForCluster(cluster.Name)
	if !exists {
		return fmt.Errorf("federated client is not yet up to date")
	}
	if err != nil {
		return fmt.Errorf("failed to get federated kube informer factory: %w", err)
	}

	discoveryClient := clusterKubeClient.Discovery()
	cluster = cluster.DeepCopy()

	if err := updateClusterHealthConditions(ctx, &cluster.Status, discoveryClient, logger); err != nil {
		return fmt.Errorf("failed to get cluster health conditions: %w", err)
	}

	skip := false
	if readyCond := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterReady); readyCond != nil &&
		readyCond.Status != corev1.ConditionTrue {
		// we skip updating node levels and api resources if cluster is not ready
		skip = true
	}

	if !skip {
		if err := updateClusterResources(ctx, &cluster.Status, clusterKubeInformer); err != nil {
			return fmt.Errorf("failed to get cluster node levels: %w", err)
		}

		if err := updateClusterAPIResources(ctx, &cluster.Status, discoveryClient); err != nil {
			return fmt.Errorf("failed to get cluster api resources: %w", err)
		}
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		latestCluster, err := fedClient.CoreV1alpha1().FederatedClusters().Get(context.TODO(), cluster.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		cluster.Status.DeepCopyInto(&latestCluster.Status)
		_, err = fedClient.CoreV1alpha1().FederatedClusters().UpdateStatus(context.TODO(), latestCluster, metav1.UpdateOptions{})
		return err
	}); err != nil {
		return fmt.Errorf("failed to update cluster status: %w", err)
	}

	return nil
}

func updateClusterHealthConditions(
	ctx context.Context,
	clusterStatus *fedcorev1a1.FederatedClusterStatus,
	clusterDiscoveryClient discovery.DiscoveryInterface,
	logger klog.Logger,
) error {
	conditionTime := metav1.Now()

	body, err := clusterDiscoveryClient.RESTClient().Get().AbsPath("/healthz").Do(ctx).Raw()
	if err != nil {
		logger.Error(err, "Cluster health check failed")
		offlineCondition := getNewClusterOfflineCondition(corev1.ConditionTrue, conditionTime)
		setClusterCondition(clusterStatus, &offlineCondition)
		readyCondition := getNewClusterReadyCondition(corev1.ConditionUnknown, conditionTime)
		setClusterCondition(clusterStatus, &readyCondition)
	} else {
		offlineCondition := getNewClusterOfflineCondition(corev1.ConditionFalse, conditionTime)
		setClusterCondition(clusterStatus, &offlineCondition)

		var clusterReadyStatus corev1.ConditionStatus
		if strings.EqualFold(string(body), "ok") {
			logger.Info("Cluster is ready")
			clusterReadyStatus = corev1.ConditionTrue
		} else {
			logger.Info("Cluster is unready")
			clusterReadyStatus = corev1.ConditionFalse
		}

		readyCondition := getNewClusterReadyCondition(clusterReadyStatus, conditionTime)
		setClusterCondition(clusterStatus, &readyCondition)
	}

	return nil
}

func updateClusterResources(
	ctx context.Context,
	clusterStatus *fedcorev1a1.FederatedClusterStatus,
	clusterKubeInformer informers.SharedInformerFactory,
) error {
	podLister := clusterKubeInformer.Core().V1().Pods().Lister()
	podsSynced := clusterKubeInformer.Core().V1().Pods().Informer().HasSynced
	nodeLister := clusterKubeInformer.Core().V1().Nodes().Lister()
	nodesSynced := clusterKubeInformer.Core().V1().Nodes().Informer().HasSynced

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !cache.WaitForNamedCacheSync("federated-cluster-controller-status-collect", ctx.Done(), podsSynced, nodesSynced) {
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
		if isNodeSchedulable(node) {
			schedulableNodes++
		}
	}

	allocatable, available := aggregateResources(nodes, pods)
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
	_, apiResourceLists, err := clusterDiscoveryClient.ServerGroupsAndResources()
	if err != nil {
		return fmt.Errorf("failed to list cluster api resources: %w", err)
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
