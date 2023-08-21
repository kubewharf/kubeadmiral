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

package clusterready

import (
	"context"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
)

type ClusterReady struct{}

func NewClusterReady(_ framework.Handle) (framework.Plugin, error) {
	return &ClusterReady{}, nil
}

func (pl *ClusterReady) Name() string {
	return names.ClusterReady
}

func (pl *ClusterReady) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	// Prevent scheduling to unready cluster unless it is already scheduled to.
	_, alreadyScheduled := su.CurrentClusters[cluster.Name]
	if !alreadyScheduled && !clusterutil.IsClusterReady(&cluster.Status) {
		return framework.NewResult(framework.Unschedulable, "cluster is unready")
	}

	return framework.NewResult(framework.Success)
}
