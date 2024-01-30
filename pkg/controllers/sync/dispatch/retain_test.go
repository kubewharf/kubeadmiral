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
	"reflect"
	"testing"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
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
			fedObj := &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: make(map[string]string),
				},
			}
			if testCase.retainReplicas {
				fedObj.GetAnnotations()[common.RetainReplicasAnnotation] = common.AnnotationValueTrue
			}
			if err := retainReplicas(desiredObj, clusterObj, fedObj, "spec.replicas"); err != nil {
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

func TestMergeStringMaps(t *testing.T) {
	testCases := map[string]struct {
		template       map[string]string
		observed       map[string]string
		lastPropagated sets.Set[string]
		expected       map[string]string
	}{
		"nil annotations": {
			template:       nil,
			observed:       nil,
			lastPropagated: nil,
			expected:       map[string]string{},
		},
		"template and observed have same key-values": {
			template: map[string]string{
				"old": "old",
			},
			observed: map[string]string{
				"old": "old",
			},
			lastPropagated: sets.New("old"),
			expected: map[string]string{
				"old": "old",
			},
		},
		"template has a new key": {
			template: map[string]string{
				"old": "old",
				"new": "new",
			},
			observed: map[string]string{
				"old": "old",
			},
			lastPropagated: sets.New("old"),
			expected: map[string]string{
				"new": "new",
				"old": "old",
			},
		},
		"template updated value for an existing key": {
			template: map[string]string{
				"old": "new_value",
			},
			observed: map[string]string{
				"old": "old",
			},
			lastPropagated: sets.New("old"),
			expected: map[string]string{
				"old": "new_value",
			},
		},
		"template removed an existing key": {
			template: map[string]string{},
			observed: map[string]string{
				"old": "old",
			},
			lastPropagated: sets.New("old"),
			expected:       map[string]string{},
		},
		"cluster object has a new key": {
			template: map[string]string{
				"old": "old",
			},
			observed: map[string]string{
				"new": "new",
				"old": "old",
			},
			lastPropagated: sets.New("old"),
			expected: map[string]string{
				"new": "new",
				"old": "old",
			},
		},
		"cluster updated value for an existing key": {
			template: map[string]string{
				"old": "old",
			},
			observed: map[string]string{
				"old": "new_value",
			},
			lastPropagated: sets.New("old"),
			expected: map[string]string{
				"old": "old",
			},
		},
		"cluster removed an existing key": {
			template: map[string]string{
				"old": "old",
			},
			observed:       map[string]string{},
			lastPropagated: sets.New("old"),
			expected: map[string]string{
				"old": "old",
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)

			merged := mergeStringMaps(testCase.template, testCase.observed, testCase.lastPropagated)
			g.Expect(merged).To(gomega.Equal(testCase.expected))
		})
	}
}

func Test_retainContainer(t *testing.T) {
	type args struct {
		desiredContainer map[string]interface{}
		clusterContainer map[string]interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "retain nil resources",
			args: args{
				desiredContainer: map[string]interface{}{
					"name": "container-1",
					"resources": map[string]interface{}{
						"cpu":    "500m",
						"memory": "512Mi",
					},
				},
				clusterContainer: map[string]interface{}{
					"name": "container-1",
				},
			},
		},
		{
			name: "retain empty resources",
			args: args{
				desiredContainer: map[string]interface{}{
					"name": "container-1",
					"resources": map[string]interface{}{
						"cpu":    "500m",
						"memory": "512Mi",
					},
				},
				clusterContainer: map[string]interface{}{
					"name":      "container-1",
					"resources": map[string]interface{}{},
				},
			},
		},
		{
			name: "retain non-empty resources",
			args: args{
				desiredContainer: map[string]interface{}{
					"name": "container-1",
					"resources": map[string]interface{}{
						"cpu":    "500m",
						"memory": "512Mi",
					},
				},
				clusterContainer: map[string]interface{}{
					"name": "container-1",
					"resources": map[string]interface{}{
						"cpu":    "100m",
						"memory": "100Mi",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := retainContainer(tt.args.desiredContainer, tt.args.clusterContainer); (err != nil) != tt.wantErr {
				t.Errorf("retainContainer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(tt.args.desiredContainer, tt.args.clusterContainer) {
				t.Errorf("retainContainer did not retain the resources field correctly")
			}
		})
	}
}

func Test_retainPodFields(t *testing.T) {
	type args struct {
		desiredObj *unstructured.Unstructured
		clusterObj *unstructured.Unstructured
	}

	noDNSConfigPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
		},
	}
	nilPodUnstructuredMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&noDNSConfigPod)
	nilPodUnstructured := &unstructured.Unstructured{
		Object: nilPodUnstructuredMap,
	}

	dnsConfigPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
		},
		Spec: corev1.PodSpec{
			DNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"server1", "server2"},
			},
		},
	}
	dnsConfigPodUnstructuredMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&dnsConfigPod)
	dnsConfigPodUnstructured := &unstructured.Unstructured{
		Object: dnsConfigPodUnstructuredMap,
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "retain non-empty pod",
			args: args{
				desiredObj: nilPodUnstructured,
				clusterObj: nilPodUnstructured,
			},
			wantErr: false,
		},
		{
			name: "retain dns config pod",
			args: args{
				desiredObj: nilPodUnstructured,
				clusterObj: dnsConfigPodUnstructured,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := retainPodFields(tt.args.desiredObj, tt.args.clusterObj); (err != nil) != tt.wantErr {
				t.Errorf("retainPodFields() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
