/*
Copyright 2018 The Kubernetes Authors.

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

package util

import (
	"fmt"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const (
	// annotations for deployment
	LatestReplicasetNameAnnotation               = "latestreplicaset.kubeadmiral.io/name"
	LatestReplicasetReplicasAnnotation           = "latestreplicaset.kubeadmiral.io/replicas"
	LatestReplicasetAvailableReplicasAnnotation  = "latestreplicaset.kubeadmiral.io/available-replicas"
	LatestReplicasetReadyReplicasAnnotation      = "latestreplicaset.kubeadmiral.io/ready-replicas"
	LatestReplicasetObservedGenerationAnnotation = "latestreplicaset.kubeadmiral.io/observed-generation"
)

const (
	// annotations for federatedDeploymentStatus
	LatestReplicasetDigestsAnnotation = common.DefaultPrefix + "latest-replicaset-digests"
	AggregatedUpdatedReplicas         = common.DefaultPrefix + "aggregated-updated-replicas"
)

// FederatedResource is a generic representation of a federated type
type FederatedResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	ClusterStatus []ResourceClusterStatus `json:"clusterStatus,omitempty"`
}

// ResourceClusterStatus defines the status of federated resource within a cluster
type ResourceClusterStatus struct {
	ClusterName     string                 `json:"clusterName,omitempty"`
	CollectedFields map[string]interface{} `json:"collectedFields,omitempty"`
}

type LatestReplicasetDigest struct {
	ClusterName        string `json:"clusterName,omitempty"`
	ReplicasetName     string `json:"replicasetName,omitempty"`
	Replicas           int64  `json:"replicas,omitempty"`
	AvailableReplicas  int64  `json:"availableReplicas,omitempty"`
	ReadyReplicas      int64  `json:"readyReplicas,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	SourceGeneration   int64  `json:"sourceGeneration,omitempty"`
}

type ReplicaSetDigest struct {
	CurrentRevision    string
	UpdatedReplicas    int64
	Generation         int64
	ObservedGeneration int64
}

func LatestReplicasetDigestFromObject(clusterName string, object *unstructured.Unstructured) (LatestReplicasetDigest, []error) {
	errs := []error{}

	annotations := object.GetAnnotations()

	replicasetName, _ := stringEntry(annotations, LatestReplicasetNameAnnotation, &errs)
	replicas := intEntry(annotations, LatestReplicasetReplicasAnnotation, &errs)
	availableReplicas := intEntry(annotations, LatestReplicasetAvailableReplicasAnnotation, &errs)
	readyReplicas := intEntry(annotations, LatestReplicasetReadyReplicasAnnotation, &errs)
	sourceGeneration := intEntry(annotations, common.SourceGenerationAnnotation, &errs)

	observedGeneration, found, err := unstructured.NestedInt64(object.Object, "status", "observedGeneration")
	if err != nil || !found {
		errs = append(errs, fmt.Errorf(
			"failed to get observedGeneration of cluster resource object %s %s/%s for cluster %q",
			object.GetKind(), object.GetNamespace(), object.GetName(), clusterName,
		))
	}

	return LatestReplicasetDigest{
		ClusterName:        clusterName,
		ReplicasetName:     replicasetName,
		Replicas:           replicas,
		AvailableReplicas:  availableReplicas,
		ReadyReplicas:      readyReplicas,
		ObservedGeneration: observedGeneration,
		SourceGeneration:   sourceGeneration,
	}, errs
}

func stringEntry(m map[string]string, key string, errs *[]error) (string, bool) {
	if str, exists := m[key]; exists {
		return str, true
	} else {
		*errs = append(*errs, fmt.Errorf("annotation %s does not exist", key))
		return "", false
	}
}

func intEntry(m map[string]string, key string, errs *[]error) int64 {
	if str, exists := stringEntry(m, key, errs); exists {
		i, err := strconv.Atoi(str)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("annotation %s is invalid: %w", key, err))
			return 0
		}

		return int64(i)
	} else {
		return 0
	}
}

func ReplicaSetDigestFromObject(utd *unstructured.Unstructured) (*ReplicaSetDigest, error) {
	observedGeneration, found, err := unstructured.NestedInt64(utd.Object, "status", "observedGeneration")
	if err != nil || !found {
		return nil, fmt.Errorf("failed to retrieve observedGeneration: %t, %v", found, err)
	}
	updatedReplicas, found, err := unstructured.NestedInt64(utd.Object, "status", "updatedReplicas")
	if err != nil || !found {
		return nil, fmt.Errorf("failed to retrieve updatedReplicas: %t, %v", found, err)
	}
	return &ReplicaSetDigest{
		CurrentRevision:    utd.GetAnnotations()[common.CurrentRevisionAnnotation],
		UpdatedReplicas:    updatedReplicas,
		Generation:         utd.GetGeneration(),
		ObservedGeneration: observedGeneration,
	}, nil
}
