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

package clusteraffinity

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/clusterselector"
)

const (
	// ClusterAffinityName is the name of the plugin used in the plugin registry and configurations.
	ClusterAffinityName = "ClusterAffinity"

	// ErrReason for node affinity/selector not matching.
	ErrReason = "cluster(s) didn't match cluster selector"
)

type ClusterAffinity struct{}

func NewClusterAffinity(_ framework.FrameworkHandle) (framework.Plugin, error) {
	return &ClusterAffinity{}, nil
}

func (pl *ClusterAffinity) Name() string {
	return ClusterAffinityName
}

// Filter invoked at the filter extension point.
func (pl *ClusterAffinity) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	if len(su.ClusterSelector) > 0 {
		selector := labels.SelectorFromSet(su.ClusterSelector)
		if !selector.Matches(labels.Set(cluster.Labels)) {
			return framework.NewResult(framework.Unschedulable, ErrReason)
		}
	}

	// 1. nil ClusterSelector matches all clusters (i.e. does not filter out any clusters)
	// 2. nil []ClusterSelectorTerm (equivalent to non-nil empty ClusterSelector) matches no clusters
	// 3. zero-length non-nil []ClusterSelectorTerm matches no clusters also, just for simplicity
	// 4. nil []ClusterSelectorRequirement (equivalent to non-nil empty ClusterSelectorTerm) matches no clusters
	// 5. zero-length non-nil []ClusterSelectorRequirement matches no clusters also, just for simplicity
	// 6. non-nil empty ClusterSelectorRequirement is not allowed
	affinity := su.Affinity
	if affinity != nil && affinity.ClusterAffinity != nil {
		clusterAffinity := affinity.ClusterAffinity
		// if no required ClusterAffinity requirements, will do no-op, means select all nodes.
		// TODO: Replace next line with subsequent commented-out line when implement RequiredDuringSchedulingRequiredDuringExecution.
		if clusterAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			// if nodeAffinity.RequiredDuringSchedulingRequiredDuringExecution == nil &&
			//     nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			return framework.NewResult(framework.Success)
		}

		// Match cluster selector for requiredDuringSchedulingIgnoredDuringExecution.
		if clusterAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			clusterSelectorTerms := clusterAffinity.RequiredDuringSchedulingIgnoredDuringExecution.ClusterSelectorTerms
			matched, err := clusterselector.MatchClusterSelectorTerms(clusterSelectorTerms, cluster)
			if err != nil || !matched {
				return framework.NewResult(framework.Unschedulable, ErrReason)
			}
		}
	}
	return framework.NewResult(framework.Success)
}

func (pl *ClusterAffinity) Score(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (int64, *framework.Result) {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	var score int64
	affinity := su.Affinity
	// A nil element of PreferredDuringSchedulingIgnoredDuringExecution matches no objects.
	// An element of PreferredDuringSchedulingIgnoredDuringExecution that refers to an
	// empty PreferredSchedulingTerm matches all objects.
	if affinity != nil && affinity.ClusterAffinity != nil &&
		affinity.ClusterAffinity.PreferredDuringSchedulingIgnoredDuringExecution != nil {
		// Match PreferredDuringSchedulingIgnoredDuringExecution term by term.
		for i := range affinity.ClusterAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			preferredSchedulingTerm := &affinity.ClusterAffinity.PreferredDuringSchedulingIgnoredDuringExecution[i]
			if preferredSchedulingTerm.Weight == 0 {
				continue
			}

			// TODO: Avoid computing it for all clusters if this becomes a performance problem.
			clusterSelector, err := clusterselector.ClusterSelectorRequirementsAsSelector(
				preferredSchedulingTerm.Preference.MatchExpressions,
			)
			if err != nil {
				return 0, framework.NewResult(framework.Error, err.Error())
			}

			if clusterSelector.Matches(labels.Set(cluster.Labels)) {
				score += int64(preferredSchedulingTerm.Weight)
			}
		}
	}

	return score, framework.NewResult(framework.Success)
}

// NormalizeScore invoked after scoring all nodes.
func (pl *ClusterAffinity) NormalizeScore(ctx context.Context, scores framework.ClusterScoreList) *framework.Result {
	return framework.DefaultNormalizeScore(framework.MaxClusterScore, false, scores)
}

func (pl *ClusterAffinity) ScoreExtensions() framework.ScoreExtensions {
	return pl
}
