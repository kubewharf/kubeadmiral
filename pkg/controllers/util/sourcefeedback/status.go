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
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

var StatusAnnotation = common.DefaultPrefix + "status"

// Status is JSON-serialized into the status annotation of source objects
// that enabled status aggregator.
type Status struct {
	Clusters []StatusCluster `json:"clusters"`
}

type StatusCluster struct {
	// Name is the name of the cluster that this entry describes.
	Name string `json:"name"`

	// Generation is the generation of the source object that got dispatched to the member cluster.
	// Zero if the target object has no source-generation annotation.
	Generation int64 `json:"generation"`
}

func PopulateStatusAnnotation(sourceObj *unstructured.Unstructured, clusterObjs map[string]interface{}, hasChanged *bool) {
	status := Status{
		Clusters: make([]StatusCluster, 0, len(clusterObjs)),
	}

	for cluster, obj := range clusterObjs {
		obj := obj.(metav1.Object)
		generationString := obj.GetAnnotations()[common.SourceGenerationAnnotation]
		generation, err := strconv.ParseInt(generationString, 10, 64)
		if err != nil {
			klog.Errorf("invalid generation string %q: %v", generationString, err)
			generation = 0
		}

		status.Clusters = append(status.Clusters, StatusCluster{
			Name:       cluster,
			Generation: generation,
		})
	}

	sort.Slice(status.Clusters, func(i, j int) bool {
		return status.Clusters[i].Name < status.Clusters[j].Name
	})

	setAnnotation(sourceObj, StatusAnnotation, &status, hasChanged)
}
