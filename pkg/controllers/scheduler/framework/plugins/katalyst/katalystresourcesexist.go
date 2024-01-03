/*
Copyright 2024 The KubeAdmiral Authors.

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

package katalyst

import (
	"context"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type KatalystResourcesExist struct{}

func NewKatalystResourcesExist(_ framework.Handle) (framework.Plugin, error) {
	return &KatalystResourcesExist{}, nil
}

func (pl *KatalystResourcesExist) Name() string {
	return names.KatalystResourcesExist
}

func (pl *KatalystResourcesExist) Filter(
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

	scRequest := &su.ResourceRequest
	clusterAllocatable := framework.NewResource(cluster.Status.Resources.Allocatable)
	if len(framework.GetKatalystResources(scRequest)) != 0 &&
		len(framework.GetKatalystResources(clusterAllocatable)) == 0 {
		return framework.NewResult(framework.Unschedulable, "Katalyst resource does not exist")
	}
	return framework.NewResult(framework.Success)
}
