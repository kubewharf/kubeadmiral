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

package clusterresource

import (
	corev1 "k8s.io/api/core/v1"
)

// AggregateResources returns
//   - allocatable resources from the nodes and,
//   - available resources after considering allocations to the given pods.
func AggregateResources(
	nodes []*corev1.Node,
	pods []*corev1.Pod,
) (corev1.ResourceList, corev1.ResourceList) {
	allocatable := make(corev1.ResourceList)
	for _, node := range nodes {
		if !IsNodeSchedulable(node) {
			continue
		}

		addResources(node.Status.Allocatable, allocatable)
	}

	// Don't consider pod resource for now
	delete(allocatable, corev1.ResourcePods)

	available := allocatable.DeepCopy()
	usage := AggregatePodUsage(pods, func(pod *corev1.Pod) *corev1.Pod { return pod })

	for name, quantity := range available {
		// `quantity` is a copy here; pointer methods do not mutate `available[name]`
		quantity.Sub(usage[name])
		available[name] = quantity
	}

	return allocatable, available
}

func AggregatePodUsage[T any](pods []T, podFunc func(T) *corev1.Pod) corev1.ResourceList {
	list := make(corev1.ResourceList)

	for _, pod := range pods {
		pod := podFunc(pod)

		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		podRequests := getPodResourceRequests(pod)
		for name, requestedQuantity := range podRequests {
			if q, exists := list[name]; exists {
				requestedQuantity.Add(q)
			}
			list[name] = requestedQuantity
		}
	}

	return list
}

// IsNodeSchedulable returns true if node is ready and schedulable, otherwise false.
func IsNodeSchedulable(node *corev1.Node) bool {
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
