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

package scheduler

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/apiresources"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusteraffinity"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/clusterresources"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/maxcluster"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/placement"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/rsp"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/tainttoleration"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/runtime"
)

func (s *Scheduler) profileForFedObject(_ *unstructured.Unstructured, handle framework.Handle) (framework.Framework, error) {
	// TODO: generate registry from fed object's type
	DefaultRegistry := runtime.Registry{
		apiresources.APIResourcesName:                           apiresources.NewAPIResources,
		tainttoleration.TaintTolerationName:                     tainttoleration.NewTaintToleration,
		clusterresources.ClusterResourcesBalancedAllocationName: clusterresources.NewClusterResourcesBalancedAllocation,
		clusterresources.ClusterResourcesFitName:                clusterresources.NewClusterResourcesFit,
		clusterresources.ClusterResourcesLeastAllocatedName:     clusterresources.NewClusterResourcesLeastAllocated,
		rsp.ClusterCapacityWeightName:                           rsp.NewClusterCapacityWeight,
		placement.PlacementFilterName:                           placement.NewPlacementFilter,
		clusteraffinity.ClusterAffinityName:                     clusteraffinity.NewClusterAffinity,
		maxcluster.MaxClusterName:                               maxcluster.NewMaxCluster,
	}

	return runtime.NewFramework(DefaultRegistry, handle)
}
