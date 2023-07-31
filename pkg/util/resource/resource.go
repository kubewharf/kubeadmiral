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

package resource

import (
	corev1 "k8s.io/api/core/v1"
)

func AddResources(src, dest corev1.ResourceList) {
	for k, v := range src {
		if prevVal, ok := dest[k]; ok {
			prevVal.Add(v)
			dest[k] = prevVal
		} else {
			dest[k] = v.DeepCopy()
		}
	}
}

// MaxResources sets dst to the greater of dst/src for every resource in src
func MaxResources(src, dst corev1.ResourceList) {
	for name, srcQuantity := range src {
		if dstQuantity, ok := dst[name]; !ok || srcQuantity.Cmp(dstQuantity) > 0 {
			dst[name] = srcQuantity.DeepCopy()
		}
	}
}

// podResourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers...) + overHead
func GetPodResourceRequests(podSpec *corev1.PodSpec) corev1.ResourceList {
	reqs := make(corev1.ResourceList)

	for _, container := range podSpec.Containers {
		AddResources(container.Resources.Requests, reqs)
	}

	for _, container := range podSpec.InitContainers {
		MaxResources(container.Resources.Requests, reqs)
	}

	// if PodOverhead feature is supported, add overhead for running a pod
	// to the sum of requests and to non-zero limits:
	if podSpec.Overhead != nil {
		AddResources(podSpec.Overhead, reqs)
	}

	return reqs
}
