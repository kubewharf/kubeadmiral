/*
Copyright 2019 The Kubernetes Authors.

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

package clusterresources

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type ClusterResourcesFit struct{}

func NewClusterResourcesFit(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterResourcesFit{}, nil
}

func (pl *ClusterResourcesFit) Name() string {
	return names.ClusterResourcesFit
}

// TODO(all), implement filter for socket and Best Effort resources.
func (pl *ClusterResourcesFit) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	// When a workload has been scheduled to the current cluster, the available resources of the current cluster
	// currently does not (but should) include the amount of resources requested by Pods of the current workload.
	// In the absence of ample resource buffer, rescheduling may mistakenly
	// evict the workload from the current cluster.
	// Disable this plugin for rescheduling as a temporary workaround.
	if _, alreadyScheduled := su.CurrentClusters[cluster.Name]; alreadyScheduled {
		return framework.NewResult(framework.Success)
	}

	insufficientResources := fitsRequest(su, cluster)

	if len(insufficientResources) != 0 {
		// We will keep all failure reasons.
		failureReasons := make([]string, 0, len(insufficientResources))
		for _, r := range insufficientResources {
			failureReasons = append(failureReasons, r.Reason)
		}
		return framework.NewResult(framework.Unschedulable, failureReasons...)
	}

	return framework.NewResult(framework.Success)
}

func fitsRequest(su *framework.SchedulingUnit, cluster *fedcorev1a1.FederatedCluster) []framework.InsufficientResource {
	insufficientResources := make([]framework.InsufficientResource, 0, 4)
	scRequest := getSchedulingUnitRequestResource(su)
	clusterAllocatable := getFederatedClusterAllocatableResource(cluster)
	clusterRequest := getFederatedClusterRequestResource(cluster)

	// Please implement the ignored ExtendedResource if needed.
	ignoredExtendedResources := sets.NewString()

	if scRequest.MilliCPU == 0 &&
		scRequest.Memory == 0 &&
		scRequest.EphemeralStorage == 0 &&
		len(scRequest.ScalarResources) == 0 {
		return insufficientResources
	}

	if clusterAllocatable.MilliCPU < scRequest.MilliCPU+clusterRequest.MilliCPU {
		insufficientResources = append(insufficientResources, framework.InsufficientResource{
			ResourceName: corev1.ResourceCPU,
			Reason:       "Insufficient cpu",
			Requested:    scRequest.MilliCPU,
			Used:         clusterRequest.MilliCPU,
			Capacity:     clusterAllocatable.MilliCPU,
		})
	}
	if clusterAllocatable.Memory < scRequest.Memory+clusterRequest.Memory {
		insufficientResources = append(insufficientResources, framework.InsufficientResource{
			ResourceName: corev1.ResourceMemory,
			Reason:       "Insufficient memory",
			Requested:    scRequest.Memory,
			Used:         clusterRequest.Memory,
			Capacity:     clusterAllocatable.Memory,
		})
	}

	for rName, rQuant := range scRequest.ScalarResources {
		if framework.IsExtendedResourceName(rName) {
			// If this resource is one of the extended resources that should be
			// ignored, we will skip checking it.
			if ignoredExtendedResources.Has(string(rName)) {
				continue
			}
		}
		// Note: kubelet also call this function during admission, and kubelet will construct a new node info every time
		// when admitting a Pod, so allocatable resource might be 0 if Pod use "0" scalar resource (e.g. nvidia.com/gpu = 0),
		// leading to admission failure if kubelet is restarted.
		if rQuant <= 0 {
			continue
		}
		if clusterAllocatable.ScalarResources[rName] < rQuant+clusterRequest.ScalarResources[rName] {
			insufficientResources = append(insufficientResources, framework.InsufficientResource{
				ResourceName: rName,
				Reason:       fmt.Sprintf("Insufficient %v", rName),
				Requested:    scRequest.ScalarResources[rName],
				Used:         clusterRequest.ScalarResources[rName],
				Capacity:     clusterAllocatable.ScalarResources[rName],
			})
		}
	}

	return insufficientResources
}

func getFederatedClusterAllocatableResource(cluster *fedcorev1a1.FederatedCluster) *framework.Resource {
	return framework.NewResource(cluster.Status.Resources.Allocatable)
}

func getFederatedClusterRequestResource(cluster *fedcorev1a1.FederatedCluster) *framework.Resource {
	request := framework.NewResource(cluster.Status.Resources.Allocatable)
	err := request.Sub(cluster.Status.Resources.Available)
	if err != nil {
		klog.Errorf("Failed to sub the available resources with err %s", err)
	}
	return request
}

func getSchedulingUnitRequestResource(su *framework.SchedulingUnit) *framework.Resource {
	return &su.ResourceRequest
}

func calculateResourceAllocatableRequest(
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
	resource corev1.ResourceName,
) (int64, int64) {
	scRequest := getSchedulingUnitRequestResource(su)
	clusterAllocatable := getFederatedClusterAllocatableResource(cluster)
	clusterRequest := getFederatedClusterRequestResource(cluster)

	switch resource {
	case corev1.ResourceCPU:
		return clusterAllocatable.MilliCPU, (clusterRequest.MilliCPU + scRequest.MilliCPU)
	case corev1.ResourceMemory:
		return clusterAllocatable.Memory, (clusterRequest.Memory + scRequest.Memory)

	case corev1.ResourceEphemeralStorage:
		return clusterAllocatable.EphemeralStorage, (clusterRequest.EphemeralStorage + scRequest.EphemeralStorage)
	default:
		if framework.IsScalarResourceName(resource) {
			return clusterAllocatable.ScalarResources[resource], (clusterRequest.ScalarResources[resource] + scRequest.ScalarResources[resource])
		}
	}

	return 0, 0
}

func getRelevantResources(su *framework.SchedulingUnit) []corev1.ResourceName {
	resources := make([]corev1.ResourceName, 0, len(framework.DefaultRequestedRatioResources))
	for resourceName := range framework.DefaultRequestedRatioResources {
		if resourceName == corev1.ResourceCPU || resourceName == corev1.ResourceMemory ||
			su.ResourceRequest.HasScalarResource(resourceName) {
			resources = append(resources, resourceName)
		}
	}

	return resources
}

func getAllocatableAndRequested(
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
	resources []corev1.ResourceName,
) (framework.ResourceToValueMap, framework.ResourceToValueMap) {
	requested := make(framework.ResourceToValueMap, len(resources))
	allocatable := make(framework.ResourceToValueMap, len(resources))
	for _, resourceName := range resources {
		allocatable[resourceName], requested[resourceName] = calculateResourceAllocatableRequest(su, cluster, resourceName)
	}

	return allocatable, requested
}
