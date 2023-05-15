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

// TODO(all)
// - implement score for macroservice.
// - implement filter/score for socket service.
// - implement filter/score for GPU service.
package rsp

import (
	"context"
	"fmt"
	"math"

	"github.com/davecgh/go-spew/spew"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/planner"
)

const (
	supplyLimitProportion         = 1.4
	sumWeight             float64 = 1000
)

const (
	availableResource   string = "available"
	allocatableResource string = "allocatable"
)

var ErrNoCPUResource = errors.New("no cpu resource")

type ClusterCapacityWeight struct{}

var _ framework.ReplicasPlugin = &ClusterCapacityWeight{}

func NewClusterCapacityWeight(frameworkHandle framework.Handle) (framework.Plugin, error) {
	return &ClusterCapacityWeight{}, nil
}

func (pl *ClusterCapacityWeight) Name() string {
	return names.ClusterCapacityWeight
}

func (pl *ClusterCapacityWeight) ReplicaScheduling(
	ctx context.Context,
	su *framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (framework.ClusterReplicasList, *framework.Result) {
	clusterReplicasList := make(framework.ClusterReplicasList, 0)
	dynamicSchedulingEnabled := len(su.Weights) == 0

	var schedulingWeights map[string]int64
	if dynamicSchedulingEnabled {
		clusterAvailables := QueryClusterResource(clusters, availableResource)
		if len(clusters) != len(clusterAvailables) {
			return clusterReplicasList, framework.NewResult(framework.Error)
		}

		weightLimit, err := CalcWeightLimit(clusters, supplyLimitProportion)
		if err != nil {
			return clusterReplicasList, framework.NewResult(
				framework.Error,
				errors.Wrapf(err, "CalcWeightLimit failed").Error(),
			)
		}

		schedulingWeights, err = AvailableToPercentage(clusterAvailables, weightLimit)
		if err != nil {
			return clusterReplicasList, framework.NewResult(
				framework.Error,
				errors.Wrapf(err, "AvailableToPercentage failed").Error(),
			)
		}
	} else {
		schedulingWeights = su.Weights
	}

	clusterPreferences := map[string]planner.ClusterPreferences{}
	for _, cluster := range clusters {
		pref := planner.ClusterPreferences{
			Weight:      schedulingWeights[cluster.Name],
			MinReplicas: su.MinReplicas[cluster.Name],
			MaxReplicas: nil,
		}

		if maxReplicas, exists := su.MaxReplicas[cluster.Name]; exists {
			pref.MaxReplicas = pointer.Int64Ptr(maxReplicas)
		}

		clusterPreferences[cluster.Name] = pref
	}

	totalReplicas := int64(0)
	if su.DesiredReplicas != nil {
		totalReplicas = *su.DesiredReplicas
	}

	currentReplicas := map[string]int64{}
	for cluster, replicas := range su.CurrentClusters {
		if replicas != nil {
			currentReplicas[cluster] = *replicas
			continue
		}
		currentReplicas[cluster] = totalReplicas
	}

	var estimatedCapacity map[string]int64
	keepUnschedulableReplicas := false
	if autoMigration := su.AutoMigration; autoMigration != nil {
		keepUnschedulableReplicas = autoMigration.KeepUnschedulableReplicas
		if info := autoMigration.Info; info != nil {
			estimatedCapacity = info.EstimatedCapacity
		}
	}

	scheduleResult, overflow, err := planner.Plan(
		&planner.ReplicaSchedulingPreference{
			Clusters: clusterPreferences,
		},
		totalReplicas,
		ExtractClusterNames(clusters),
		currentReplicas,
		estimatedCapacity,
		su.Key(),
		su.AvoidDisruption,
		keepUnschedulableReplicas,
	)
	if err != nil {
		return clusterReplicasList, framework.NewResult(framework.Error)
	}

	klog.V(4).Infof(
		"[scheduling] for %q clusterPreferences: %s, estimatedCapacity: %v, currentReplicas: %v, result: %v",
		su.Key(), spew.Sprint(clusterPreferences), estimatedCapacity, currentReplicas, scheduleResult,
	)

	result := make(map[string]int64)
	for clusterName, replicas := range scheduleResult {
		result[clusterName] = replicas
	}
	for clusterName, replicas := range overflow {
		result[clusterName] += replicas
	}

	for _, cluster := range clusters {
		replicas, ok := result[cluster.Name]
		if !ok || replicas == 0 {
			continue
		}
		clusterReplicasList = append(clusterReplicasList, framework.ClusterReplicas{
			Cluster:  cluster,
			Replicas: replicas,
		})
	}
	return clusterReplicasList, framework.NewResult(framework.Success)
}

func CalcWeightLimit(
	clusters []*fedcorev1a1.FederatedCluster,
	supplyLimitRatio float64,
) (weightLimit map[string]int64, err error) {
	allocatables := QueryClusterResource(clusters, allocatableResource)
	if len(allocatables) != len(clusters) {
		err = fmt.Errorf("allocatables are incomplete: %v", allocatables)
		return
	}
	sum := 0.0
	for _, resources := range allocatables {
		cpu := resources[corev1.ResourceCPU]
		sum += float64(cpu.Value())
	}
	weightLimit = make(map[string]int64)
	if sum == 0 {
		for member := range allocatables {
			weightLimit[member] = int64(math.Round(sumWeight / float64(len(allocatables))))
		}
		return
	}
	for member, resources := range allocatables {
		cpu, ok := resources[corev1.ResourceCPU]
		if !ok {
			err = ErrNoCPUResource
			return
		}
		weightLimit[member] = int64(math.Round(float64(cpu.Value()) / sum * sumWeight * supplyLimitRatio))
	}
	return
}

func AvailableToPercentage(
	clusterAvailables map[string]corev1.ResourceList,
	weightLimit map[string]int64,
) (clusterWeights map[string]int64, err error) {
	sumAvailable := 0.0
	for _, resources := range clusterAvailables {
		cpu := resources[corev1.ResourceCPU]
		if cpu.Value() > 0.0 {
			sumAvailable += float64(cpu.Value())
		}
	}

	clusterWeights = make(map[string]int64)
	if sumAvailable == 0 {
		for member := range clusterAvailables {
			clusterWeights[member] = int64(math.Round(sumWeight / float64(len(clusterAvailables))))
		}
		return
	}

	tmpMemberWeights := make(map[string]int64)
	sumTmpWeight := int64(0)

	for member, resources := range clusterAvailables {
		cpu, ok := resources[corev1.ResourceCPU]
		if !ok {
			err = ErrNoCPUResource
			return
		}

		cpuValue := float64(cpu.Value())
		if cpuValue < 0.0 {
			cpuValue = 0.0
		}

		weight := int64(math.Round(cpuValue / sumAvailable * sumWeight))
		if weight > weightLimit[member] {
			weight = weightLimit[member]
		}
		tmpMemberWeights[member] = weight
		sumTmpWeight += weight
	}
	otherSumWeight := int64(0)
	maxWeight := int64(0)
	maxCluster := ""

	for member, tmpMemberWeight := range tmpMemberWeights {
		weight := int64(math.Round(float64(tmpMemberWeight) / float64(sumTmpWeight) * sumWeight))
		if weight > maxWeight {
			maxWeight = weight
			maxCluster = member
		}
		clusterWeights[member] = weight
		otherSumWeight += weight
	}
	clusterWeights[maxCluster] += int64(sumWeight) - otherSumWeight
	return
}

// QueryClusterResource aggregate cluster resources, accept available and allocatable.
func QueryClusterResource(clusters []*fedcorev1a1.FederatedCluster, resource string) map[string]corev1.ResourceList {
	switch resource {
	case availableResource:
		return QueryAvailable(clusters)
	case allocatableResource:
		return QueryAllocatable(clusters)
	}
	return nil
}

// QueryAvailable aggregate cluster available resource.
func QueryAvailable(clusters []*fedcorev1a1.FederatedCluster) map[string]corev1.ResourceList {
	ret := make(map[string]corev1.ResourceList)
	for _, cluster := range clusters {
		available := make(corev1.ResourceList)
		available[corev1.ResourceCPU] = resource.MustParse("0")
		available[corev1.ResourceMemory] = resource.MustParse("0")
		// sum up by resource
		for resourceName := range cluster.Status.Resources.Available {
			if val, ok := available[resourceName]; ok {
				(&val).Add(cluster.Status.Resources.Available[resourceName])
				available[resourceName] = val
			} else {
				available[resourceName] = cluster.Status.Resources.Available[resourceName]
			}
		}
		ret[cluster.GetName()] = available
	}
	return ret
}

// QueryAllocatable aggregate cluster allocatable resource.
func QueryAllocatable(clusters []*fedcorev1a1.FederatedCluster) map[string]corev1.ResourceList {
	ret := make(map[string]corev1.ResourceList)
	for _, cluster := range clusters {
		allocatable := make(corev1.ResourceList)
		allocatable[corev1.ResourceCPU] = resource.MustParse("0")
		allocatable[corev1.ResourceMemory] = resource.MustParse("0")
		// sum up by resource
		for resourceName := range cluster.Status.Resources.Allocatable {
			if val, ok := allocatable[resourceName]; ok {
				(&val).Add(cluster.Status.Resources.Allocatable[resourceName])
				allocatable[resourceName] = val
			} else {
				allocatable[resourceName] = cluster.Status.Resources.Allocatable[resourceName]
			}
		}
		ret[cluster.GetName()] = allocatable
	}
	return ret
}

func ExtractClusterNames(clusters []*fedcorev1a1.FederatedCluster) []string {
	ret := make([]string, len(clusters))
	for i := range clusters {
		ret[i] = clusters[i].Name
	}
	return ret
}
