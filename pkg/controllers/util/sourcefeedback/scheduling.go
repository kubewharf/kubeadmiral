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
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/util/meta"
)

var SchedulingAnnotation = common.DefaultPrefix + "scheduling"

type Scheduling struct {
	// Generation is the generation of the source object
	// observed in the federated object when this placement is sampled.
	// This value should not be null unless in the condition
	// where the federated object is manually created by another controller.
	Generation *int64 `json:"generation"`

	// FederatedGeneration is the generation of the federated object
	// observed when this placement is sampled.
	FederatedGeneration int64 `json:"fedGeneration"`

	// Placement contains a list of FederatedCluster object names.
	Placement []string `json:"placement,omitempty"`
}

func PopulateSchedulingAnnotation(sourceObject *unstructured.Unstructured, fedObject *fedcorev1a1.GenericFederatedObject, hasChanged *bool) (err error) {
	scheduling := Scheduling{}

	srcMeta, err := meta.GetSourceObjectMeta(fedObject)
	if err != nil {
		return err
	}

	scheduling.Generation = pointer.Int64(srcMeta.GetGeneration())
	scheduling.FederatedGeneration = fedObject.GetGeneration()

	clusterNames := fedObject.GetPlacementUnion()
	if len(clusterNames) > 0 {
		for clusterName := range clusterNames {
			scheduling.Placement = append(scheduling.Placement, clusterName)
		}
		sort.Strings(scheduling.Placement)
	}

	setAnnotation(sourceObject, SchedulingAnnotation, &scheduling, hasChanged)

	return nil
}
