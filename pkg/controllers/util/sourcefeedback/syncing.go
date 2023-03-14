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

package sourcefeedback

import (
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const SyncingAnnotation = common.DefaultPrefix + "syncing"

type Syncing struct {
	// Generation is the generation of the source object
	// observed in the federated object during this sync operation.
	// This value should not be null unless in the condition
	// where the federated object is manually created by another controller.
	Generation *int64 `json:"generation"`

	// FederatedGeneration is the generation of the federated object
	// observed during this sync operation.
	FederatedGeneration int64 `json:"fedGeneration"`

	Clusters []SyncingCluster `json:"clusters"`
}

type SyncingCluster struct {
	// Name is the name of the cluster that this entry describes.
	Name string `json:"name"`
	// Status is the cluster propagation status string.
	Status fedtypesv1a1.PropagationStatus `json:"status"`
}

func PopulateSyncingAnnotation(
	fedObject *unstructured.Unstructured,
	clusterStatusMap map[string]fedtypesv1a1.PropagationStatus,
	hasChanged *bool,
) (err error) {
	syncing := Syncing{}

	generation, exists, err := unstructured.NestedInt64(
		fedObject.Object,
		common.SpecField,
		common.TemplateField,
		common.MetadataField,
		common.GenerationField,
	)
	if err != nil {
		return err
	}
	if exists {
		generation := generation
		syncing.Generation = &generation
	}

	syncing.FederatedGeneration = fedObject.GetGeneration()

	syncing.Clusters = make([]SyncingCluster, 0, len(clusterStatusMap))

	for clusterName, clusterStatus := range clusterStatusMap {
		syncing.Clusters = append(syncing.Clusters, SyncingCluster{
			Name:   clusterName,
			Status: clusterStatus,
		})
	}

	sort.Slice(syncing.Clusters, func(i, j int) bool {
		return syncing.Clusters[i].Name < syncing.Clusters[j].Name
	})

	setAnnotation(fedObject, SyncingAnnotation, &syncing, hasChanged)

	return nil
}
