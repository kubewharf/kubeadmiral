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
	corev1 "k8s.io/api/core/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func IsClusterReady(clusterStatus *fedcorev1a1.FederatedClusterStatus) bool {
	for _, condition := range clusterStatus.Conditions {
		if condition.Type == fedcorev1a1.ClusterReady {
			if condition.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

func IsClusterJoined(clusterStatus *fedcorev1a1.FederatedClusterStatus) bool {
	for _, condition := range clusterStatus.Conditions {
		if condition.Type == fedcorev1a1.ClusterJoined {
			if condition.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}
