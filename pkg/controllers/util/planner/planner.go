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

package planner

import (
	"hash/fnv"
	"sort"
)

// Preferences regarding number of replicas assigned to a cluster workload object (dep, rs, ..) within
// a federated workload object.
type ClusterPreferences struct {
	// Minimum number of replicas that should be assigned to this cluster workload object. 0 by default.
	MinReplicas int64

	// Maximum number of replicas that should be assigned to this cluster workload object.
	// Unbounded if no value provided (default).
	MaxReplicas *int64

	// A number expressing the preference to put an additional replica to this cluster workload object.
	// 0 by default.
	Weight int64
}

type ReplicaSchedulingPreference struct {
	// A mapping between cluster names and preferences regarding a local workload object (dep, rs, .. ) in
	// these clusters.
	// "*" (if provided) applies to all clusters if an explicit mapping is not provided.
	// If omitted, clusters without explicit preferences should not have any replicas scheduled.
	Clusters map[string]ClusterPreferences
}

type namedClusterPreferences struct {
	clusterName string
	hash        uint32
	ClusterPreferences
}

type byWeight []*namedClusterPreferences

func (a byWeight) Len() int      { return len(a) }
func (a byWeight) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

// Preferences are sorted according by decreasing weight and increasing hash (built on top of cluster name and rs name).
// Sorting is made by a hash to avoid assigning single-replica rs to the alphabetically smallest cluster.
func (a byWeight) Less(i, j int) bool {
	return (a[i].Weight > a[j].Weight) || (a[i].Weight == a[j].Weight && a[i].hash < a[j].hash)
}

// Distribute the desired number of replicas among the given cluster according to the planner preferences.
// The function tries its best to assign each cluster the preferred number of replicas, however if
// sum of MinReplicas for all cluster is bigger than replicasToDistribute (TotalReplicas) then some cluster
// will not have all of the replicas assigned. In such case a cluster with higher weight has priority over
// cluster with lower weight (or with lexicographically smaller name in case of draw).
// It can also use the current replica count and estimated capacity to provide better planning and
// adhere to rebalance policy. To avoid prioritization of clusters with smaller lexicographical names
// a semi-random string (like replica set name) can be provided.
// Two maps are returned:
//   - a map that contains information how many replicas will be possible to run in a cluster.
//   - a map that contains information how many extra replicas would be nice to schedule in a cluster so,
//     if by chance, they are scheduled we will be closer to the desired replicas layout.
func Plan(
	rsp *ReplicaSchedulingPreference,
	totalReplicas int64,
	availableClusters []string,
	currentReplicaCount map[string]int64,
	estimatedCapacity map[string]int64,
	replicaSetKey string,
	avoidMigration bool,
	keepUnschedulableReplicas bool,
) (map[string]int64, map[string]int64, error) {
	preferences := make(map[string]*ClusterPreferences, len(availableClusters))

	for _, cluster := range availableClusters {
		if preference, found := rsp.Clusters[cluster]; found {
			preferences[cluster] = &preference
		} else if preference, found := rsp.Clusters["*"]; found {
			preferences[cluster] = &preference
		}
	}

	namedPreferences, err := getNamedPreferences(preferences, replicaSetKey)
	if err != nil {
		return nil, nil, err
	}

	// If keepUnschedulableReplicas is false,
	// the resultant plan will likely violate the preferences
	// if any cluster has limited capacity.
	// If avoidMigration is also false, a subsequent reschedule will restore
	// the replica distribution to the state before we moved the unschedulable
	// replicas. This leads to a infinite reschedule loop and is undesirable.
	// Therefore we default to keeping the unschedulable replicas if avoidMigration
	// is false.
	if !avoidMigration {
		keepUnschedulableReplicas = true
	}

	desiredPlan, desiredOverflow := getDesiredPlan(
		namedPreferences,
		estimatedCapacity,
		totalReplicas,
		keepUnschedulableReplicas,
	)

	// If we don't want to avoid migration, just return the plan computed from preferences
	if !avoidMigration {
		return desiredPlan, desiredOverflow, nil
	}

	// Try to avoid instance migration between clusters

	var currentTotalOkReplicas int64
	// currentPlan should only contain clusters in availableClusters
	currentPlan := make(map[string]int64, len(namedPreferences))
	for _, preference := range namedPreferences {
		replicas := currentReplicaCount[preference.clusterName]
		if capacity, exists := estimatedCapacity[preference.clusterName]; exists && capacity < replicas {
			replicas = capacity
		}
		currentPlan[preference.clusterName] = replicas

		currentTotalOkReplicas += replicas
	}

	var desiredTotalReplicas int64
	for _, replicas := range desiredPlan {
		desiredTotalReplicas += replicas
	}

	switch {
	case currentTotalOkReplicas == desiredTotalReplicas:
		return currentPlan, desiredOverflow, nil
	case currentTotalOkReplicas > desiredTotalReplicas:
		plan, err := scaleDown(
			currentPlan, desiredPlan,
			currentTotalOkReplicas-desiredTotalReplicas,
			replicaSetKey,
		)
		if err != nil {
			return nil, nil, err
		}
		return plan, desiredOverflow, nil
	default:
		plan, err := scaleUp(
			rsp,
			currentPlan, desiredPlan,
			desiredTotalReplicas-currentTotalOkReplicas,
			replicaSetKey,
		)
		if err != nil {
			return nil, nil, err
		}
		return plan, desiredOverflow, nil
	}
}

func getNamedPreferences(
	preferences map[string]*ClusterPreferences,
	replicaSetKey string,
) ([]*namedClusterPreferences, error) {
	namedPreferences := make([]*namedClusterPreferences, 0, len(preferences))
	named := func(name string, pref *ClusterPreferences) (*namedClusterPreferences, error) {
		hasher := fnv.New32()
		if _, err := hasher.Write([]byte(name)); err != nil {
			return nil, err
		}
		if _, err := hasher.Write([]byte(replicaSetKey)); err != nil {
			return nil, err
		}

		return &namedClusterPreferences{
			clusterName:        name,
			hash:               hasher.Sum32(),
			ClusterPreferences: *pref,
		}, nil
	}

	for name, preference := range preferences {
		namedPreference, err := named(name, preference)
		if err != nil {
			return nil, err
		}
		namedPreferences = append(namedPreferences, namedPreference)
	}
	sort.Sort(byWeight(namedPreferences))
	return namedPreferences, nil
}

func getDesiredPlan(
	preferences []*namedClusterPreferences,
	estimatedCapacity map[string]int64,
	totalReplicas int64,
	keepUnschedulableReplicas bool,
) (map[string]int64, map[string]int64) {
	remainingReplicas := totalReplicas
	plan := make(map[string]int64, len(preferences))
	overflow := make(map[string]int64, len(preferences))

	// Assign each cluster the minimum number of replicas it requested.
	for _, preference := range preferences {
		min := minInt64(preference.MinReplicas, remainingReplicas)
		if capacity, hasCapacity := estimatedCapacity[preference.clusterName]; hasCapacity && capacity < min {
			overflow[preference.clusterName] = min - capacity
			min = capacity
		}
		remainingReplicas -= min
		plan[preference.clusterName] = min
	}

	modified := true
	// It is possible single pass of the loop is not enough to distribute all replicas among clusters due
	// to weight, max and rounding corner cases. In such case we iterate until either
	// there is no replicas or no cluster gets any more replicas. Every loop either distributes all remainingReplicas
	// or maxes out at least one cluster.
	for modified && remainingReplicas > 0 {
		modified = false
		weightSum := int64(0)
		for _, preference := range preferences {
			weightSum += preference.Weight
		}
		if weightSum <= 0 {
			break
		}
		newPreferences := make([]*namedClusterPreferences, 0, len(preferences))

		distributeInThisLoop := remainingReplicas
		for _, preference := range preferences {
			start := plan[preference.clusterName]
			// Distribute the remaining replicas, rounding fractions always up.
			extra := (distributeInThisLoop*preference.Weight + weightSum - 1) / weightSum
			extra = minInt64(extra, remainingReplicas)

			// In total there should be the amount that was there at start plus whatever is due
			// in this iteration
			total := start + extra

			// Check if we don't overflow the cluster, and if yes don't consider this cluster
			// in any of the following iterations.
			full := false
			if preference.MaxReplicas != nil && total > *preference.MaxReplicas {
				total = *preference.MaxReplicas
				full = true
			}
			if capacity, hasCapacity := estimatedCapacity[preference.clusterName]; hasCapacity && total > capacity {
				overflow[preference.clusterName] += total - capacity
				total = capacity
				full = true
			}
			if !full {
				newPreferences = append(newPreferences, preference)
			}

			// Only total-start replicas were actually taken.
			remainingReplicas -= total - start
			plan[preference.clusterName] = total

			// Something extra got scheduled on this cluster.
			if total > start {
				modified = true
			}
		}
		preferences = newPreferences
	}

	// If we want to keep the unschedulable replicas in their original
	// clusters, we return the overflow (which contains these
	// unschedulable replicas) as is.
	if keepUnschedulableReplicas {
		return plan, overflow
	}

	// Otherwise, trim overflow at the level
	// of replicas that the algorithm failed to place anywhere.
	newOverflow := make(map[string]int64)
	for key, value := range overflow {
		value = minInt64(value, remainingReplicas)
		if value > 0 {
			newOverflow[key] = value
		}
	}
	return plan, newOverflow
}

func scaleUp(
	rsp *ReplicaSchedulingPreference,
	currentReplicaCount, desiredReplicaCount map[string]int64,
	scaleUpCount int64,
	replicaSetKey string,
) (map[string]int64, error) {
	preferences := make(map[string]*ClusterPreferences, len(desiredReplicaCount))
	for cluster, desired := range desiredReplicaCount {
		// only pick clusters which have less replicas than desired to sale up, thus replica migration between clusters can be avoid
		current := currentReplicaCount[cluster]
		if desired > current {
			preferences[cluster] = &ClusterPreferences{
				Weight: desired - current,
			}
			if rsp.Clusters[cluster].MaxReplicas != nil {
				// note that max is always positive because MaxReplicas >= desired > current
				max := *rsp.Clusters[cluster].MaxReplicas - current
				preferences[cluster].MaxReplicas = &max
			}
		}
	}

	named, err := getNamedPreferences(preferences, replicaSetKey)
	if err != nil {
		return nil, err
	}
	// no estimatedCapacity and hence no overflow
	replicasToScaleUp, _ := getDesiredPlan(named, nil, scaleUpCount, false)
	for cluster, count := range replicasToScaleUp {
		currentReplicaCount[cluster] += count
	}
	return currentReplicaCount, nil
}

func scaleDown(
	currentReplicaCount, desiredReplicaCount map[string]int64,
	scaleDownCount int64,
	replicaSetKey string,
) (map[string]int64, error) {
	preferences := make(map[string]*ClusterPreferences, len(desiredReplicaCount))
	for cluster, desired := range desiredReplicaCount {
		// only pick clusters which have more replicas than desired to scale down, thus replica migration between clusters can be avoid
		current := currentReplicaCount[cluster]
		if desired < current {
			preferences[cluster] = &ClusterPreferences{
				Weight:      current - desired,
				MaxReplicas: &current,
			}
		}
	}
	named, err := getNamedPreferences(preferences, replicaSetKey)
	if err != nil {
		return nil, err
	}
	// no estimatedCapacity and hence no overflow
	replicasToScaleDown, _ := getDesiredPlan(named, nil, scaleDownCount, false)
	for cluster, count := range replicasToScaleDown {
		currentReplicaCount[cluster] -= count
	}
	return currentReplicaCount, nil
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
