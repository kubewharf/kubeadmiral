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

package cluster

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federatedcluster"
)

func ClusterJoined(cluster *fedcorev1a1.FederatedCluster) bool {
	cond := GetClusterJoinCondition(cluster.Status.Conditions)
	return cond != nil && cond.Status == corev1.ConditionTrue &&
		cond.Reason == federatedcluster.ClusterJoinedReason &&
		cond.Message == federatedcluster.ClusterJoinedMessage
}

func ClusterReady(cluster *fedcorev1a1.FederatedCluster) bool {
	cond := GetClusterReadyCondition(cluster.Status.Conditions)
	return cond != nil && cond.Status == corev1.ConditionTrue &&
		cond.Reason == federatedcluster.ClusterReadyReason &&
		cond.Message == federatedcluster.ClusterReadyMessage
}

func ClusterTimedOut(cluster *fedcorev1a1.FederatedCluster) bool {
	cond := GetClusterJoinCondition(cluster.Status.Conditions)
	messagePrefix := strings.TrimSuffix(federatedcluster.JoinTimeoutExceededMessageTemplate, "%v")
	return cond != nil && cond.Status == corev1.ConditionFalse &&
		cond.Reason == federatedcluster.JoinTimeoutExceededReason &&
		strings.HasPrefix(cond.Message, messagePrefix)
}

func ClusterReachable(cluster *fedcorev1a1.FederatedCluster) bool {
	cond := GetClusterOfflineCondition(cluster.Status.Conditions)
	return ClusterJoined(cluster) && ClusterReady(cluster) &&
		cond != nil && cond.Status == corev1.ConditionFalse &&
		cond.Reason == federatedcluster.ClusterReachableReason &&
		cond.Message == federatedcluster.ClusterReachableMsg
}

func ClusterUnreachable(cluster *fedcorev1a1.FederatedCluster) bool {
	conditions := cluster.Status.Conditions

	readyCond := GetClusterReadyCondition(conditions)
	offlineCond := GetClusterOfflineCondition(conditions)

	return ClusterJoined(cluster) &&
		readyCond != nil && readyCond.Status == corev1.ConditionUnknown &&
		readyCond.Reason == federatedcluster.ClusterNotReachableReason &&
		readyCond.Message == federatedcluster.ClusterNotReachableMsg &&
		offlineCond != nil && offlineCond.Status == corev1.ConditionTrue &&
		offlineCond.Reason == federatedcluster.ClusterNotReachableReason &&
		offlineCond.Message == federatedcluster.ClusterNotReachableMsg
}

func GetClusterJoinCondition(conditions []fedcorev1a1.ClusterCondition) *fedcorev1a1.ClusterCondition {
	for _, existingCondition := range conditions {
		if existingCondition.Type == fedcorev1a1.ClusterJoined {
			return &existingCondition
		}
	}

	return nil
}

func GetClusterReadyCondition(conditions []fedcorev1a1.ClusterCondition) *fedcorev1a1.ClusterCondition {
	for _, existingCondition := range conditions {
		if existingCondition.Type == fedcorev1a1.ClusterReady {
			return &existingCondition
		}
	}

	return nil
}

func GetClusterOfflineCondition(conditions []fedcorev1a1.ClusterCondition) *fedcorev1a1.ClusterCondition {
	for _, existingCondition := range conditions {
		if existingCondition.Type == fedcorev1a1.ClusterOffline {
			return &existingCondition
		}
	}

	return nil
}
