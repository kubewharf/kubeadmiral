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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

// computeNamespacedPlacement determines placement for namespaced
// federated resources (e.g. FederatedConfigMap).
//
// If KubeFed is deployed cluster-wide, placement is the intersection
// of the placement for the federated resource and the placement of
// the federated namespace containing the resource.
//
// If KubeFed is limited to a single namespace, placement is
// determined as the intersection of resource and namespace placement
// if namespace placement exists.  If namespace placement does not
// exist, resource placement will be used verbatim.  This is possible
// because the single namespace by definition must exist on member
// clusters, so namespace placement becomes a mechanism for limiting
// rather than allowing propagation.
func computeNamespacedPlacement(
	resource, namespace *unstructured.Unstructured,
	clusters []*fedcorev1a1.FederatedCluster,
	limitedScope bool,
) (selectedClusters sets.String, err error) {
	resourceClusters, err := computePlacement(resource, clusters)
	if err != nil {
		return nil, err
	}

	if namespace == nil {
		if limitedScope {
			// Use the resource placement verbatim if no federated
			// namespace is present and KubeFed is targeting a
			// single namespace.
			return resourceClusters, nil
		}
		// Resource should not exist in any member clusters.
		return sets.String{}, nil
	}

	namespaceClusters, err := computePlacement(namespace, clusters)
	if err != nil {
		return nil, err
	}

	// If both namespace and resource placement exist, the desired
	// list of clusters is their intersection.
	return resourceClusters.Intersection(namespaceClusters), nil
}

// computePlacement determines the selected clusters for a federated
// resource.
func computePlacement(
	resource *unstructured.Unstructured,
	clusters []*fedcorev1a1.FederatedCluster,
) (selectedClusters sets.String, err error) {
	selectedNames, err := selectedClusterNames(resource, clusters)
	if err != nil {
		return nil, err
	}
	clusterNames := getClusterNames(clusters)
	return clusterNames.Intersection(selectedNames), nil
}

func selectedClusterNames(
	resource *unstructured.Unstructured,
	clusters []*fedcorev1a1.FederatedCluster,
) (sets.String, error) {
	object, err := util.UnmarshalGenericPlacements(resource)
	if err != nil {
		return nil, err
	}

	selectedNames := sets.String{}

	for _, placement := range object.Spec.Placements {
		for cluster := range placement.Placement.ClusterNames() {
			selectedNames.Insert(cluster)
		}
	}

	return selectedNames, nil
}

func getClusterNames(clusters []*fedcorev1a1.FederatedCluster) sets.String {
	clusterNames := sets.String{}
	for _, cluster := range clusters {
		clusterNames.Insert(cluster.Name)
	}
	return clusterNames
}
