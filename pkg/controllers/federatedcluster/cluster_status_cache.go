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
	"sync"
	"time"

	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

type clusterStatusStore struct {
	clusterStatusData      sync.Map
	clusterStatusThreshold time.Duration
}

type clusterStatusConditionData struct {
	offlineCondition fedcorev1a1.ClusterCondition
	readyCondition   fedcorev1a1.ClusterCondition
	probeTimestamp   time.Time
}

func (c *clusterStatusStore) thresholdAdjustedStatusCondition(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	observedOfflineCondition fedcorev1a1.ClusterCondition,
	observedReadyCondition fedcorev1a1.ClusterCondition,
) (fedcorev1a1.ClusterCondition, fedcorev1a1.ClusterCondition) {
	logger := klog.FromContext(ctx)

	saved := c.get(cluster.Name)
	if saved == nil {
		// the cluster is just joined
		c.update(cluster.Name, &clusterStatusConditionData{
			offlineCondition: observedOfflineCondition,
			readyCondition:   observedReadyCondition,
		})
		return observedOfflineCondition, observedReadyCondition
	}
	curOfflineCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterOffline)
	curReadyCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterReady)
	if curOfflineCondition == nil || curReadyCondition == nil {
		return observedOfflineCondition, observedReadyCondition
	}

	now := time.Now()
	if saved.offlineCondition.Status != observedOfflineCondition.Status || saved.readyCondition.Status != observedReadyCondition.Status {
		// condition status changed, record the probe timestamp
		saved = &clusterStatusConditionData{
			offlineCondition: observedOfflineCondition,
			readyCondition:   observedReadyCondition,
			probeTimestamp:   now,
		}
		c.update(cluster.Name, saved)
	}

	if curOfflineCondition.Status != observedOfflineCondition.Status || curReadyCondition.Status != observedReadyCondition.Status {
		// threshold not exceeded, return the old status condition
		if now.Before(saved.probeTimestamp.Add(c.clusterStatusThreshold)) {
			logger.V(3).WithValues("offline", curOfflineCondition.Status, "ready", curReadyCondition.Status).
				Info("Threshold not exceeded, return the old status condition")
			return *curOfflineCondition, *curReadyCondition
		}

		logger.V(3).WithValues("offline", observedOfflineCondition.Status, "ready", observedReadyCondition.Status).
			Info("Cluster status condition changed")
	}

	return observedOfflineCondition, observedReadyCondition
}

func (c *clusterStatusStore) get(cluster string) *clusterStatusConditionData {
	condition, ok := c.clusterStatusData.Load(cluster)
	if !ok {
		return nil
	}
	return condition.(*clusterStatusConditionData)
}

func (c *clusterStatusStore) update(cluster string, data *clusterStatusConditionData) {
	c.clusterStatusData.Store(cluster, data)
}
