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

package preferencebinpack

import (
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

// ClusterPreferences regarding number of replicas assigned to a cluster workload object (dep, rs, ..) within
// a federated workload object.
type ClusterPreferences struct {
	// Minimum number of replicas that should be assigned to this cluster workload object. 0 by default.
	MinReplicas int64

	// Maximum number of replicas that should be assigned to this cluster workload object.
	// Unbounded if no value provided (default).
	MaxReplicas *int64
}

type ReplicaSchedulingPreference struct {
	Clusters map[string]ClusterPreferences
}

type namedClusterPreferences struct {
	clusterName string
	ClusterPreferences
}

func Plan(
	rsp *ReplicaSchedulingPreference,
	totalReplicas int64,
	availableClusters []string,
	currentReplicaCount map[string]int64,
	estimatedCapacity map[string]int64,
	limitedCapacity map[string]int64,
	avoidDisruption bool,
	keepUnschedulableReplicas bool,
	scheduledReplicas map[string]int64, // Replicas that have been scheduled in the member clusters
) (map[string]int64, map[string]int64, error) {
	if !shouldPlan(
		availableClusters,
		currentReplicaCount,
		limitedCapacity,
		estimatedCapacity,
		scheduledReplicas,
	) {
		return currentReplicaCount, nil, nil
	}

	namedPreferences := make([]*namedClusterPreferences, 0, len(availableClusters))
	for _, cluster := range availableClusters {
		namedPreferences = append(namedPreferences, &namedClusterPreferences{
			clusterName:        cluster,
			ClusterPreferences: rsp.Clusters[cluster],
		})
	}

	if !avoidDisruption {
		keepUnschedulableReplicas = true
	}

	desiredPlan, desiredOverflow := getDesiredPlan(
		namedPreferences,
		estimatedCapacity,
		limitedCapacity,
		totalReplicas,
	)

	var currentTotalOkReplicas, currentTotalScheduledReplicas int64
	// currentPlan should only contain clusters in availableClusters
	currentPlan := make(map[string]int64, len(namedPreferences))
	for _, preference := range namedPreferences {
		replicas := currentReplicaCount[preference.clusterName]

		if capacity, exists := estimatedCapacity[preference.clusterName]; exists && capacity < replicas {
			replicas = capacity
		}

		limitedReplicas, limitedExists := limitedCapacity[preference.clusterName]
		if limitedExists && limitedReplicas < replicas {
			replicas = limitedReplicas
		}

		currentPlan[preference.clusterName] = replicas

		currentTotalOkReplicas += replicas

		if scheduled, exist := scheduledReplicas[preference.clusterName]; exist {
			if limitedExists && limitedReplicas < scheduled {
				scheduled = limitedReplicas
			}
			currentTotalScheduledReplicas += scheduled
		}
	}

	if !keepUnschedulableReplicas && currentTotalScheduledReplicas == totalReplicas {
		klog.V(4).Infof("Trim overflow replicas when getting desired number of scheduled replicas")
		return scheduledReplicas, nil, nil
	}

	// If we don't need to avoid migration, just return the plan computed from preferences
	if !avoidDisruption {
		return desiredPlan, desiredOverflow, nil
	}

	var desiredTotalReplicas int64
	for _, replicas := range desiredPlan {
		desiredTotalReplicas += replicas
	}

	// Cap the overflow at currently unscheduled replicas.
	if !keepUnschedulableReplicas {
		newOverflow := make(map[string]int64)
		for key, value := range desiredOverflow {
			value = framework.MinInt64(value, totalReplicas-currentTotalScheduledReplicas)
			if value > 0 {
				newOverflow[key] = value
			}
		}

		desiredOverflow = newOverflow
	}

	klog.V(4).Infof("Desired plan: %v and overflow: %v before scale", desiredPlan, desiredOverflow)

	// Try to avoid instance migration between clusters
	switch {
	case currentTotalOkReplicas == desiredTotalReplicas:
		return currentPlan, desiredOverflow, nil
	case currentTotalOkReplicas > desiredTotalReplicas:
		plan, err := scaleDown(
			currentPlan, desiredPlan,
			currentTotalOkReplicas-totalReplicas,
			availableClusters,
		)
		if err != nil {
			return nil, nil, err
		}
		klog.V(4).Infof("ScaleDown plan: %v", plan)
		return plan, desiredOverflow, nil
	default:
		plan, err := scaleUp(
			rsp,
			currentPlan, desiredPlan,
			limitedCapacity,
			totalReplicas-currentTotalOkReplicas,
			availableClusters,
		)
		if err != nil {
			return nil, nil, err
		}
		klog.V(4).Infof("ScaleUp plan: %v", plan)
		return plan, desiredOverflow, nil
	}
}

func getDesiredPlan(
	preferences []*namedClusterPreferences,
	estimatedCapacity map[string]int64,
	limitedCapacity map[string]int64,
	totalReplicas int64,
) (map[string]int64, map[string]int64) {
	remainingReplicas := totalReplicas
	plan := make(map[string]int64, len(preferences))
	overflow := make(map[string]int64, len(preferences))

	// Assign each cluster the minimum number of replicas it requested.
	for _, preference := range preferences {
		min := framework.MinInt64(preference.MinReplicas, remainingReplicas)
		if capacity, hasCapacity := limitedCapacity[preference.clusterName]; hasCapacity && capacity < min {
			min = capacity
		}
		if capacity, hasCapacity := estimatedCapacity[preference.clusterName]; hasCapacity && capacity < min {
			overflow[preference.clusterName] = min - capacity
			min = capacity
		}
		remainingReplicas -= min
		plan[preference.clusterName] = min
	}

	for _, preference := range preferences {
		start := plan[preference.clusterName]

		// In total there should be the amount that was there at start plus whatever is due
		// in this iteration
		total := start + remainingReplicas

		if preference.MaxReplicas != nil && total > *preference.MaxReplicas {
			total = *preference.MaxReplicas
		}
		if capacity, hasCapacity := limitedCapacity[preference.clusterName]; hasCapacity && total > capacity {
			total = capacity
		}
		if capacity, hasCapacity := estimatedCapacity[preference.clusterName]; hasCapacity && total > capacity {
			overflow[preference.clusterName] += total - capacity
			total = capacity
		}

		// Only total-start replicas were actually taken.
		remainingReplicas -= total - start
		plan[preference.clusterName] = total
	}

	return plan, overflow
}

func scaleUp(
	rsp *ReplicaSchedulingPreference,
	currentReplicaCount, desiredReplicaCount map[string]int64,
	limitedCapacity map[string]int64,
	scaleUpCount int64,
	availableClusters []string,
) (map[string]int64, error) {
	namedPreferences := make([]*namedClusterPreferences, 0, len(availableClusters))
	for _, cluster := range availableClusters {
		current := currentReplicaCount[cluster]
		desired := desiredReplicaCount[cluster]
		if desired > current {
			pref := &namedClusterPreferences{
				clusterName: cluster,
			}
			pref.MinReplicas = rsp.Clusters[cluster].MinReplicas
			if rsp.Clusters[cluster].MaxReplicas != nil {
				// note that max is always positive because MaxReplicas >= desired > current
				max := *rsp.Clusters[cluster].MaxReplicas - current
				pref.MaxReplicas = &max
			}
			namedPreferences = append(namedPreferences, pref)
		}
	}

	// no estimatedCapacity and hence no overflow
	replicasToScaleUp, _ := getDesiredPlan(namedPreferences, nil, limitedCapacity, scaleUpCount)
	for cluster, count := range replicasToScaleUp {
		currentReplicaCount[cluster] += count
	}

	return currentReplicaCount, nil
}

func scaleDown(
	currentReplicaCount, desiredReplicaCount map[string]int64,
	scaleDownCount int64,
	availableClusters []string,
) (map[string]int64, error) {
	namedPreferences := make([]*namedClusterPreferences, 0, len(availableClusters))
	for _, cluster := range availableClusters {
		current := currentReplicaCount[cluster]
		desired := desiredReplicaCount[cluster]
		if desired < current {
			namedPreferences = append([]*namedClusterPreferences{{
				clusterName: cluster,
				ClusterPreferences: ClusterPreferences{
					MaxReplicas: &current,
				},
			}}, namedPreferences...)
		}
	}

	// no estimatedCapacity and hence no overflow
	replicasToScaleDown, _ := getDesiredPlan(namedPreferences, nil, nil, scaleDownCount)
	for cluster, count := range replicasToScaleDown {
		currentReplicaCount[cluster] -= count
	}

	return currentReplicaCount, nil
}

func shouldPlan(
	availableClusters []string,
	currentReplicaCount, limitedCapacity,
	estimatedCapacity, scheduledReplicas map[string]int64,
) bool {
	if isScheduledClustersRemoved(availableClusters, currentReplicaCount) {
		return true
	}

	for cluster, replicas := range currentReplicaCount {
		if capacity, exists := limitedCapacity[cluster]; exists && replicas > capacity {
			return true
		}
	}

	return isEstimatedCapacityAvailable(currentReplicaCount, estimatedCapacity, scheduledReplicas)
}

func isScheduledClustersRemoved(
	availableClusters []string,
	currentReplicaCount map[string]int64,
) bool {
	availableClustersSets := sets.New(availableClusters...)
	for cluster := range currentReplicaCount {
		if !availableClustersSets.Has(cluster) {
			return true
		}
	}
	return false
}

func isEstimatedCapacityAvailable(
	currentReplicaCount, estimatedCapacity, scheduledReplicas map[string]int64,
) (available bool) {
	defer func() {
		if !available {
			klog.V(4).Infof("Current estimate capacity is unavailable")
		}
	}()

	// If `capacity != replicas`, some replicas are still being scheduled. We defer planning until the status of all replicas are known.
	for cluster, replicas := range scheduledReplicas {
		if capacity, exist := estimatedCapacity[cluster]; exist && capacity != replicas {
			return false
		} else if !exist && replicas != currentReplicaCount[cluster] {
			return false
		}
	}

	return true
}
