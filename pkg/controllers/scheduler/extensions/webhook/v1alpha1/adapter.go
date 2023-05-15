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

package v1alpha1

import (
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	schedwebhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func ConvertSchedulingUnit(su *framework.SchedulingUnit) *schedwebhookv1a1.SchedulingUnit {
	currentClusters := []string{}
	currentReplicaDistribution := map[string]int64{}
	for cluster, replicas := range su.CurrentClusters {
		currentClusters = append(currentClusters, cluster)
		if su.SchedulingMode == fedcorev1a1.SchedulingModeDivide {
			if replicas == nil {
				// NOTE(hawjia): this should never happen, we should probably redesign the internal scheduling unit to
				// make this case impossible.
				currentReplicaDistribution[cluster] = 0
			} else {
				currentReplicaDistribution[cluster] = *replicas
			}
		}
	}

	placements := []fedcorev1a1.Placement{}
	for cluster := range su.ClusterNames {
		var weight *int64
		if w, ok := su.Weights[cluster]; ok {
			weight = &w
		}

		var maxReplicas *int64
		if max, ok := su.MaxReplicas[cluster]; ok {
			maxReplicas = &max
		}

		placement := fedcorev1a1.Placement{
			Cluster: cluster,
			Preferences: fedcorev1a1.Preferences{
				MinReplicas: su.MinReplicas[cluster],
				MaxReplicas: maxReplicas,
				Weight:      weight,
			},
		}

		placements = append(placements, placement)
	}

	var affinity []fedcorev1a1.ClusterSelectorTerm
	if su.Affinity != nil && su.Affinity.ClusterAffinity != nil &&
		su.Affinity.ClusterAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		affinity = su.Affinity.ClusterAffinity.RequiredDuringSchedulingIgnoredDuringExecution.ClusterSelectorTerms
	}

	return &schedwebhookv1a1.SchedulingUnit{
		APIVersion: su.GroupVersion.String(),
		Kind:       su.Kind,
		Resource:   su.Resource,

		Namespace:   su.Namespace,
		Name:        su.Name,
		Labels:      su.Labels,
		Annotations: su.Annotations,

		SchedulingMode:             su.SchedulingMode,
		DesiredReplicas:            su.DesiredReplicas,
		ResourceRequest:            su.ResourceRequest.ResourceList(),
		CurrentClusters:            currentClusters,
		CurrentReplicaDistribution: currentReplicaDistribution,
		ClusterSelector:            su.ClusterSelector,
		ClusterAffinity:            affinity,
		Tolerations:                su.Tolerations,
		MaxClusters:                su.MaxClusters,
		Placements:                 placements,
	}
}
