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

package dispatch

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func TestRetainClusterFields(t *testing.T) {
	testCases := map[string]struct {
		retainReplicas   bool
		desiredReplicas  int64
		clusterReplicas  int64
		expectedReplicas int64
	}{
		"replicas not retained when retainReplicas=false or is not present": {
			retainReplicas:   false,
			desiredReplicas:  1,
			clusterReplicas:  2,
			expectedReplicas: 1,
		},
		"replicas retained when retainReplicas=true": {
			retainReplicas:   true,
			desiredReplicas:  1,
			clusterReplicas:  2,
			expectedReplicas: 2,
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			desiredObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": testCase.desiredReplicas,
					},
				},
			}
			clusterObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": testCase.clusterReplicas,
					},
				},
			}
			fedObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"retainReplicas": testCase.retainReplicas,
					},
				},
			}
			if err := retainReplicas(desiredObj, clusterObj, fedObj, &fedcorev1a1.FederatedTypeConfig{
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					PathDefinition: fedcorev1a1.PathDefinition{
						ReplicasSpec: "spec.replicas",
					},
				},
			}); err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			replicas, ok, err := unstructured.NestedInt64(desiredObj.Object, "spec", "replicas")
			if !ok {
				t.Fatalf("Field 'spec.replicas' not found")
			}
			if err != nil {
				t.Fatalf("An unexpected error occurred")
			}
			if replicas != testCase.expectedReplicas {
				t.Fatalf("Expected %d replicas when retainReplicas=%v, got %d", testCase.expectedReplicas, testCase.retainReplicas, replicas)
			}
		})
	}
}
