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

package framework

import (
	"context"

	"k8s.io/client-go/dynamic"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

type Framework interface {
	RunFilterPlugins(context.Context, *SchedulingUnit, *fedcorev1a1.FederatedCluster) *Result
	RunScorePlugins(context.Context, *SchedulingUnit, []*fedcorev1a1.FederatedCluster) (PluginToClusterScore, *Result)
	RunSelectClustersPlugin(context.Context, *SchedulingUnit, ClusterScoreList) ([]*fedcorev1a1.FederatedCluster, *Result)
	RunReplicasPlugin(context.Context, *SchedulingUnit, []*fedcorev1a1.FederatedCluster) (ClusterReplicasList, *Result)
}

// Plugin is the parent type for all the scheduling framework plugins.
type Plugin interface {
	Name() string
}

// FilterPlugin is an interface for filter plugins. These filters are used to filter out clusters
// that are not fit for the resource.
type FilterPlugin interface {
	Plugin

	// Filter is called by the scheduling framework.
	Filter(context.Context, *SchedulingUnit, *fedcorev1a1.FederatedCluster) *Result
}

// ScorePlugin is an interface that must be implemented by "Score" plugins to rank
// clusters that passed the filtering phase.
type ScorePlugin interface {
	Plugin

	// Score is called on each filtered Cluster. It must return success and an integer
	// indicating the rank of the cluster. All scoring plugins must return success or
	// the pod will be rejected.
	Score(context.Context, *SchedulingUnit, *fedcorev1a1.FederatedCluster) (int64, *Result)

	// ScoreExtensions returns a ScoreExtensions interface if it implements one, or nil if it does not.
	ScoreExtensions() ScoreExtensions
}

// ScoreExtensions is an interface for Score extended functionality.
type ScoreExtensions interface {
	// NormalizeScore is called for all cluster scores produced by the same plugin's "Score"
	// method. A successful run of NormalizeScore will update the scores list and return
	// a success status.
	NormalizeScore(context.Context, ClusterScoreList) *Result
}

type SelectPlugin interface {
	Plugin

	SelectClusters(context.Context, *SchedulingUnit, ClusterScoreList) (ClusterScoreList, *Result)
}

type ReplicasPlugin interface {
	Plugin

	ReplicaScheduling(context.Context, *SchedulingUnit, []*fedcorev1a1.FederatedCluster) (ClusterReplicasList, *Result)
}

type Handle interface {
	DynamicClient() dynamic.Interface
}
