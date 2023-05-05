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

package federatedcluster

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func getClusterCondition(
	status *fedcorev1a1.FederatedClusterStatus,
	conditionType fedcorev1a1.ClusterConditionType,
) *fedcorev1a1.ClusterCondition {
	for _, existingCondition := range status.Conditions {
		if existingCondition.Type == conditionType {
			return &existingCondition
		}
	}

	return nil
}

func setClusterCondition(
	status *fedcorev1a1.FederatedClusterStatus,
	newCondition *fedcorev1a1.ClusterCondition,
) {
	for i, existingCondition := range status.Conditions {
		if existingCondition.Type == newCondition.Type {
			status.Conditions[i] = *newCondition
			return
		}
	}

	status.Conditions = append(status.Conditions, *newCondition)
}

func getNewClusterOfflineCondition(
	status corev1.ConditionStatus,
	conditionTime metav1.Time,
) fedcorev1a1.ClusterCondition {
	condition := fedcorev1a1.ClusterCondition{
		Type:               fedcorev1a1.ClusterOffline,
		LastProbeTime:      conditionTime,
		LastTransitionTime: &conditionTime,
	}

	var reason, message string
	if status == corev1.ConditionTrue {
		reason = ClusterNotReachableReason
		message = ClusterNotReachableMsg
	} else if status == corev1.ConditionFalse {
		reason = ClusterReachableReason
		message = ClusterReachableMsg
	}

	condition.Status = status
	condition.Reason = &reason
	condition.Message = &message

	return condition
}

func getNewClusterReadyCondition(
	status corev1.ConditionStatus,
	reason, message string,
	conditionTime metav1.Time,
) fedcorev1a1.ClusterCondition {
	condition := fedcorev1a1.ClusterCondition{
		Type:               fedcorev1a1.ClusterReady,
		Status:             status,
		Reason:             &reason,
		Message:            &message,
		LastProbeTime:      conditionTime,
		LastTransitionTime: &conditionTime,
	}

	return condition
}

func isClusterJoined(status *fedcorev1a1.FederatedClusterStatus) (joined, failed bool) {
	joinedCondition := getClusterCondition(status, fedcorev1a1.ClusterJoined)
	if joinedCondition == nil {
		return false, false
	}
	if joinedCondition.Status == corev1.ConditionTrue {
		return true, false
	}

	if joinedCondition.Reason == nil {
		return false, false
	}
	if *joinedCondition.Reason == JoinTimeoutExceededReason ||
		*joinedCondition.Reason == ClusterUnjoinableReason {
		return false, true
	}

	return false, false
}
