//go:build exclude
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

package sync

import (
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

// computePlacement determines the selected clusters for a federated
// resource.
func computePlacement(
	resource fedcorev1a1.GenericFederatedObject,
	clusters []*fedcorev1a1.FederatedCluster,
) (selectedClusters sets.Set[string]) {
	selectedNames := resource.GetSpec().GetPlacementUnion()
	clusterNames := getClusterNames(clusters)
	return clusterNames.Intersection(selectedNames)
}

func getClusterNames(clusters []*fedcorev1a1.FederatedCluster) sets.Set[string] {
	clusterNames := sets.New[string]()
	for _, cluster := range clusters {
		clusterNames.Insert(cluster.Name)
	}
	return clusterNames
}
