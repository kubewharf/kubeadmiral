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

package adoption

import (
	"encoding/json"
	"fmt"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const (
	ConflictResolutionAnnotation         = common.DefaultPrefix + "conflict-resolution"
	ConflictResolutionInternalAnnotation = common.InternalPrefix + "conflict-resolution"
	// ClustersToAdoptAnnotation specifies the set of clusters where preexisting resources are allowed to be adopted. Defaults to no clusters.
	// It will only take effect if adoption is enabled by the conflict resolution annotation.
	ClustersToAdoptAnnotation = common.DefaultPrefix + "clusters-to-adopt"
)

type ConflictResolution string

const (
	// Conflict resolution for preexisting resources
	ConflictResolutionAdopt ConflictResolution = "adopt"
)

func ShouldAdoptPreexistingResources(obj metav1.Object) bool {
	annotations := obj.GetAnnotations()

	value, exists := annotations[ConflictResolutionInternalAnnotation]
	if !exists {
		value = annotations[ConflictResolutionAnnotation]
	}

	return value == string(ConflictResolutionAdopt)
}

type clustersToAdoptAnnotationElement struct {
	Clusters []string `json:"clusters,omitempty"`
	Regexp   string   `json:"regexp,omitempty"`
}

func FilterToAdoptCluster(obj metav1.Object, clusterName string) (bool, error) {
	annotation := obj.GetAnnotations()[ClustersToAdoptAnnotation]
	if len(annotation) == 0 {
		return false, nil
	}

	var clustersToAdopt clustersToAdoptAnnotationElement
	if err := json.Unmarshal([]byte(annotation), &clustersToAdopt); err != nil {
		return false, fmt.Errorf("failed to unmarshal %s annotation %w", ClustersToAdoptAnnotation, err)
	}

	for _, name := range clustersToAdopt.Clusters {
		if name == clusterName {
			return true, nil
		}
	}

	if len(clustersToAdopt.Regexp) != 0 {
		clustersToAdoptRegexp, err := regexp.Compile(clustersToAdopt.Regexp)
		if err != nil {
			return false, fmt.Errorf("failed to compile regexp for to adopt clusters: %w", err)
		}
		return clustersToAdoptRegexp.MatchString(clusterName), nil
	}

	return false, nil
}
