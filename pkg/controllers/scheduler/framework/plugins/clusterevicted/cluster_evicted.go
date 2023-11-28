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

package clusterevicted

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

const (
	// ErrReason for cluster evicted not matching.
	ErrReason = "cluster(s) were evicted due to custom migration configuration"
)

type ClusterEvicted struct{}

func NewClusterEvicted(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterEvicted{}, nil
}

func (pl *ClusterEvicted) Name() string {
	return names.ClusterEvicted
}

// Filter invoked at the filter extension point.
func (pl *ClusterEvicted) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	if su.CustomMigration.Info != nil {
		unavailableClusters := su.CustomMigration.Info.UnavailableClusters
		for _, unavailableCluster := range unavailableClusters {
			if cluster.Name == unavailableCluster.Cluster && metav1.Now().Time.Before(unavailableCluster.ValidUntil.Time) {
				return framework.NewResult(framework.Unschedulable, ErrReason)
			}
		}
	}

	return framework.NewResult(framework.Success)
}
