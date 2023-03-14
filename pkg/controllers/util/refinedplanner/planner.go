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

package refinedplanner

import (
	"hash/fnv"
	"sort"

	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/planner"
)

type ReplicaSchedulingPreference struct {

	// Total number of pods desired across federated clusters.
	// Replicas specified in the spec for target deployment template or replicaset
	// template will be discarded/overridden when scheduling preferences are
	// specified.
	TotalReplicas int64

	// If set to true then already scheduled and running replicas may be moved to other clusters
	// in order to match current state to the specified preferences. Otherwise, if set to false,
	// up and running replicas will not be moved.
	// +optional
	Rebalance bool

	// A mapping between cluster names and preferences regarding a local workload object (dep, rs, .. ) in
	// these clusters.
	// "*" (if provided) applies to all clusters if an explicit mapping is not provided.
	// If omitted, clusters without explicit preferences should not have any replicas scheduled.
	// +optional
	Clusters map[string]planner.ClusterPreferences
}

// Planner decides how many out of the given replicas should be placed in each of the
// federated clusters.
type Planner struct {
	preferences *ReplicaSchedulingPreference
}

type namedClusterPreferences struct {
	clusterName string
	hash        uint32
	planner.ClusterPreferences
}

type byWeight []*namedClusterPreferences

func (a byWeight) Len() int      { return len(a) }
func (a byWeight) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

// Preferences are sorted according by decreasing weight and increasing hash (built on top of cluster name and rs name).
// Sorting is made by a hash to avoid assigning single-replica rs to the alphabetically smallest cluster.
func (a byWeight) Less(i, j int) bool {
	return (a[i].Weight > a[j].Weight) || (a[i].Weight == a[j].Weight && a[i].hash < a[j].hash)
}

func NewPlanner(preferences *ReplicaSchedulingPreference) *Planner {
	return &Planner{
		preferences: preferences,
	}
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
func (p *Planner) Plan(availableClusters []string, currentReplicaCount map[string]int64,
	estimatedCapacity map[string]int64, replicaSetKey string) (map[string]int64, map[string]int64, error) {
	preferences := make(map[string]*planner.ClusterPreferences, len(availableClusters))

	for _, cluster := range availableClusters {
		if preference, found := p.preferences.Clusters[cluster]; found {
			preferences[cluster] = &preference
		} else if preference, found := p.preferences.Clusters["*"]; found {
			preferences[cluster] = &preference
		}
	}

	namedPreferences, err := p.getNamedPreferences(preferences, replicaSetKey)
	if err != nil {
		return nil, nil, err
	}

	desiredReplicaCount := p.getDesiredPlan(namedPreferences, p.preferences.TotalReplicas)
	if p.preferences.Rebalance {
		return desiredReplicaCount, nil, nil
	}
	klog.Infof("desiredReplicaCount: %v\n", desiredReplicaCount)

	// when rebalance is disabled, try to avoid instance migration between clusters
	var currentTotalCount int64
	plan := make(map[string]int64, len(namedPreferences))
	for _, preference := range namedPreferences {
		count := currentReplicaCount[preference.clusterName]
		plan[preference.clusterName] = count
		currentTotalCount += count
	}
	var desiredTotalCount int64
	for _, count := range desiredReplicaCount {
		desiredTotalCount += count
	}

	if currentTotalCount == desiredTotalCount {
		return plan, nil, nil
	} else if currentTotalCount > desiredTotalCount {
		return p.scaleDown(plan, desiredReplicaCount, currentTotalCount-desiredTotalCount, replicaSetKey)
	} else {
		return p.scaleUp(plan, desiredReplicaCount, desiredTotalCount-currentTotalCount, replicaSetKey)
	}
}

func (p *Planner) getNamedPreferences(
	preferences map[string]*planner.ClusterPreferences,
	replicaSetKey string,
) ([]*namedClusterPreferences, error) {
	namedPreferences := make([]*namedClusterPreferences, 0, len(preferences))
	named := func(name string, pref *planner.ClusterPreferences) (*namedClusterPreferences, error) {
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

func (p *Planner) getDesiredPlan(preferences []*namedClusterPreferences, totalReplicas int64) map[string]int64 {
	remainingReplicas := totalReplicas
	plan := make(map[string]int64, len(preferences))

	// Assign each cluster the minimum number of replicas it requested.
	for _, preference := range preferences {
		min := minInt64(preference.MinReplicas, remainingReplicas)
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
			return plan
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
	return plan
}

func (p *Planner) scaleUp(
	currentReplicaCount, desiredReplicaCount map[string]int64,
	scaleUpCount int64,
	replicaSetKey string,
) (map[string]int64, map[string]int64, error) {
	preferences := make(map[string]*planner.ClusterPreferences, len(desiredReplicaCount))
	for cluster, desired := range desiredReplicaCount {
		// only pick clusters which have less replicas than desired to sale up, thus replica migration between clusters can be avoid
		current := currentReplicaCount[cluster]
		if desired > current {
			preferences[cluster] = &planner.ClusterPreferences{
				Weight: desired - current,
			}
			if p.preferences.Clusters[cluster].MaxReplicas != nil {
				// note that max is always positive because MaxReplicas >= desired > current
				max := *p.preferences.Clusters[cluster].MaxReplicas - current
				preferences[cluster].MaxReplicas = &max
			}
		}
	}

	named, err := p.getNamedPreferences(preferences, replicaSetKey)
	if err != nil {
		return nil, nil, err
	}
	replicasToScaleUp := p.getDesiredPlan(named, scaleUpCount)
	for cluster, count := range replicasToScaleUp {
		currentReplicaCount[cluster] += count
	}
	return currentReplicaCount, nil, nil
}

func (p *Planner) scaleDown(
	currentReplicaCount, desiredReplicaCount map[string]int64,
	scaleDownCount int64,
	replicaSetKey string,
) (map[string]int64, map[string]int64, error) {
	preferences := make(map[string]*planner.ClusterPreferences, len(desiredReplicaCount))
	for cluster, desired := range desiredReplicaCount {
		// only pick clusters which have more replicas than desired to scale down, thus replica migration between clusters can be avoid
		current := currentReplicaCount[cluster]
		if desired < current {
			preferences[cluster] = &planner.ClusterPreferences{
				Weight:      current - desired,
				MaxReplicas: &current,
			}
		}
	}
	named, err := p.getNamedPreferences(preferences, replicaSetKey)
	if err != nil {
		return nil, nil, err
	}
	replicasToScaleDown := p.getDesiredPlan(named, scaleDownCount)
	for cluster, count := range replicasToScaleDown {
		currentReplicaCount[cluster] -= count
	}
	return currentReplicaCount, nil, nil
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
