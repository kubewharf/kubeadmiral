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

// isNodeSchedulable returns true if node is ready and schedulable, otherwise false.
func isNodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}

	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule ||
			taint.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}

	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
			return false
		}
	}
	return true
}

func addResources(src, dest corev1.ResourceList) {
	for k, v := range src {
		if prevVal, ok := dest[k]; ok {
			prevVal.Add(v)
			dest[k] = prevVal
		} else {
			dest[k] = v.DeepCopy()
		}
	}
}

// maxResources sets dst to the greater of dst/src for every resource in src
func maxResources(src, dst corev1.ResourceList) {
	for name, srcQuantity := range src {
		if dstQuantity, ok := dst[name]; !ok || srcQuantity.Cmp(dstQuantity) > 0 {
			dst[name] = srcQuantity.DeepCopy()
		}
	}
}

// podResourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers...) + overHead
func getPodResourceRequests(pod *corev1.Pod) corev1.ResourceList {
	reqs := make(corev1.ResourceList)

	for _, container := range pod.Spec.Containers {
		addResources(container.Resources.Requests, reqs)
	}

	for _, container := range pod.Spec.InitContainers {
		maxResources(container.Resources.Requests, reqs)
	}

	// if PodOverhead feature is supported, add overhead for running a pod
	// to the sum of requests and to non-zero limits:
	if pod.Spec.Overhead != nil {
		addResources(pod.Spec.Overhead, reqs)
	}

	return reqs
}

// aggregateResources returns
//   - allocatable resources from the nodes and,
//   - available resources after considering allocations to the given pods.
func aggregateResources(
	nodes []*corev1.Node,
	pods []*corev1.Pod,
) (corev1.ResourceList, corev1.ResourceList) {
	allocatable := make(corev1.ResourceList)
	for _, node := range nodes {
		if !isNodeSchedulable(node) {
			continue
		}

		addResources(node.Status.Allocatable, allocatable)
	}

	// Don't consider pod resource for now
	delete(allocatable, corev1.ResourcePods)

	available := make(corev1.ResourceList)
	for name, quantity := range allocatable {
		available[name] = quantity.DeepCopy()
	}

	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		podRequests := getPodResourceRequests(pod)
		for name, requestedQuantity := range podRequests {
			if availableQuantity, ok := available[name]; ok {
				availableQuantity.Sub(requestedQuantity)
				available[name] = availableQuantity
			}
		}
	}

	return allocatable, available
}
