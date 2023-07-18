//go:build exclude
/*
Copyright 2014 The Kubernetes Authors.

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

package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

type ScheduleAlgorithm interface {
	Schedule(
		context.Context,
		framework.Framework,
		framework.SchedulingUnit,
		[]*fedcorev1a1.FederatedCluster,
	) (result ScheduleResult, err error)
}

type genericScheduler struct{}

type ScheduleResult struct {
	// SuggestedClusters is a map contains the recommended cluster placements and replica distribution.
	// The key is the name of the cluster and the value is the recommended number of replicas for it.
	// If the value is nil, it means that there is no recommended number of replicas for the cluster (used in Duplicate scheduling mode).
	SuggestedClusters map[string]*int64
}

func (result ScheduleResult) ClusterSet() map[string]struct{} {
	clusterSet := make(map[string]struct{}, len(result.SuggestedClusters))
	for cluster := range result.SuggestedClusters {
		clusterSet[cluster] = struct{}{}
	}
	return clusterSet
}

func (result ScheduleResult) String() string {
	var sb strings.Builder
	sb.WriteString("[")

	i := 0
	for cluster, replicas := range result.SuggestedClusters {
		sb.WriteString(cluster)
		sb.WriteString(":")
		if replicas == nil {
			sb.WriteString("nil")
		} else {
			sb.WriteString(strconv.FormatInt(*replicas, 10))
		}

		i++
		if i < len(result.SuggestedClusters) {
			sb.WriteString(", ")
		}
	}

	sb.WriteString("]")

	return sb.String()
}

func NewSchedulerAlgorithm() ScheduleAlgorithm {
	return &genericScheduler{}
}

func (g *genericScheduler) Schedule(
	ctx context.Context,
	fwk framework.Framework,
	schedulingUnit framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (result ScheduleResult, err error) {
	logger := klog.FromContext(ctx)

	// we do not reschedule if sticky cluster is enabled
	if schedulingUnit.StickyCluster && len(schedulingUnit.CurrentClusters) > 0 {
		result.SuggestedClusters = schedulingUnit.CurrentClusters
		return result, nil
	}

	feasibleClusters, err := g.findClustersThatFitWorkload(ctx, fwk, schedulingUnit, clusters)
	if err != nil {
		return result, fmt.Errorf("failed to findClustersThatFitWorkload: %w", err)
	}
	logger.V(2).
		Info("Clusters filtered", "result", spew.Sprint(feasibleClusters))
	if len(feasibleClusters) == 0 {
		return result, nil
	}

	clusterScores, err := g.scoreClusters(ctx, fwk, schedulingUnit, feasibleClusters)
	if err != nil {
		return result, fmt.Errorf("failed to scoreClusters: %w", err)
	}
	logger.V(2).
		Info("Clusters scored", "result", spew.Sprint(clusterScores))

	selectedClusters, err := g.selectClusters(ctx, fwk, schedulingUnit, clusterScores)
	if err != nil {
		return result, fmt.Errorf("failed to selectClusters: %w", err)
	}
	logger.V(2).Info("Clusters selected", "clusters", spew.Sprint(selectedClusters))

	// we skip replica scheduling if mode is Duplicate
	if schedulingUnit.SchedulingMode == fedcorev1a1.SchedulingModeDuplicate {
		logger.V(3).Info("skip replica scheduling for Duplicate scheduling mode")
		result.SuggestedClusters = make(map[string]*int64, len(selectedClusters))
		for _, cluster := range selectedClusters {
			result.SuggestedClusters[cluster.Name] = nil
		}
		return result, nil
	}

	clusterReplicaList, err := g.replicaScheduling(ctx, fwk, schedulingUnit, selectedClusters)
	if err != nil {
		return result, fmt.Errorf("failed to do replicaScheduling: %w", err)
	}
	logger.V(2).
		Info("Replicas assigned", "result", spew.Sprint(clusterReplicaList))
	result.SuggestedClusters = make(map[string]*int64, len(clusterReplicaList))
	for _, clusterReplica := range clusterReplicaList {
		result.SuggestedClusters[clusterReplica.Cluster.Name] = pointer.Int64(clusterReplica.Replicas)
	}
	return result, nil
}

func (g *genericScheduler) findClustersThatFitWorkload(
	ctx context.Context,
	fwk framework.Framework,
	schedulingUnit framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) ([]*fedcorev1a1.FederatedCluster, error) {
	logger := klog.FromContext(ctx)

	ret := make([]*fedcorev1a1.FederatedCluster, 0)
	for _, cluster := range clusters {
		if result := fwk.RunFilterPlugins(ctx, &schedulingUnit, cluster); !result.IsSuccess() {
			logger.V(2).Info("Cluster doesn't fit", "name", cluster.Name, "reason", result.AsError())
		} else {
			ret = append(ret, cluster)
		}
	}
	return ret, nil
}

func (g *genericScheduler) scoreClusters(
	ctx context.Context,
	fwk framework.Framework,
	schedulingUnit framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (framework.ClusterScoreList, error) {
	ret := make(framework.ClusterScoreList, len(clusters))
	scores, result := fwk.RunScorePlugins(ctx, &schedulingUnit, clusters)
	if !result.IsSuccess() {
		return ret, result.AsError()
	}
	for i := range clusters {
		ret[i] = framework.ClusterScore{
			Cluster: clusters[i],
			Score:   0,
		}
		for j := range scores {
			ret[i].Score += scores[j][i].Score
		}
	}
	return ret, nil
}

func (g *genericScheduler) selectClusters(
	ctx context.Context,
	fwk framework.Framework,
	schedulingUnit framework.SchedulingUnit,
	clusterScores framework.ClusterScoreList,
) ([]*fedcorev1a1.FederatedCluster, error) {
	clusters, result := fwk.RunSelectClustersPlugin(ctx, &schedulingUnit, clusterScores)
	if !result.IsSuccess() {
		return nil, result.AsError()
	}
	return clusters, nil
}

func (g *genericScheduler) replicaScheduling(
	ctx context.Context,
	fwk framework.Framework,
	schedulingUnit framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (framework.ClusterReplicasList, error) {
	clusterReplicasList, result := fwk.RunReplicasPlugin(ctx, &schedulingUnit, clusters)
	if !result.IsSuccess() {
		return nil, result.AsError()
	}
	return clusterReplicasList, nil
}
