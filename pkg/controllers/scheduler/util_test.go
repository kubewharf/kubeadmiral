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
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func TestMatchedPolicyKey(t *testing.T) {
	testCases := map[string]struct {
		objectNamespace string
		ppLabelValue    *string
		cppLabelValue   *string

		expectedPolicyFound     bool
		expectedPolicyName      string
		expectedPolicyNamespace string
	}{
		"namespaced object does not reference any policy": {
			objectNamespace:     "default",
			expectedPolicyFound: false,
		},
		"cluster-scoped object does not reference any policy": {
			expectedPolicyFound: false,
		},
		"namespaced object references pp in same namespace": {
			objectNamespace:         "default",
			ppLabelValue:            pointer.String("pp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "pp1",
			expectedPolicyNamespace: "default",
		},
		"namespaced object references cpp": {
			objectNamespace:         "default",
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
		"namespaced object references both pp and cpp": {
			objectNamespace:         "default",
			ppLabelValue:            pointer.String("pp1"),
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "pp1",
			expectedPolicyNamespace: "default",
		},
		"cluster-scoped object references pp": {
			ppLabelValue:        pointer.String("pp1"),
			expectedPolicyFound: false,
		},
		"cluster-scoped object references cpp": {
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
		"cluster-scoped object references both pp and cpp": {
			ppLabelValue:            pointer.String("pp1"),
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: make(map[string]interface{})}
			object.SetNamespace(testCase.objectNamespace)
			labels := map[string]string{}
			if testCase.ppLabelValue != nil {
				labels[PropagationPolicyNameLabel] = *testCase.ppLabelValue
			}
			if testCase.cppLabelValue != nil {
				labels[ClusterPropagationPolicyNameLabel] = *testCase.cppLabelValue
			}
			object.SetLabels(labels)

			policy, found := GetMatchedPolicyKey(object)
			if found != testCase.expectedPolicyFound {
				t.Fatalf("found = %v, but expectedPolicyFound = %v", found, testCase.expectedPolicyFound)
			}

			if !found {
				return
			}

			if policy.Name != testCase.expectedPolicyName {
				t.Fatalf("policyName = %s, but expectedPolicyName = %s", policy.Name, testCase.expectedPolicyName)
			}
			if policy.Namespace != testCase.expectedPolicyNamespace {
				t.Fatalf("policyNamespace = %v, but expectedPolicyNamespace = %v", policy.Namespace, testCase.expectedPolicyNamespace)
			}
		})
	}
}

func TestUpdatePendingSyncClusters(t *testing.T) {
	type args struct {
		fedObject   fedcorev1a1.GenericFederatedObject
		result      map[string]*int64
		annotations map[string]string
	}
	tests := []struct {
		name            string
		args            args
		want            bool
		wantErr         bool
		wantAnnotations map[string]string
	}{
		{
			name: "no cluster, no annotation",
			args: args{
				fedObject:   &fedcorev1a1.FederatedObject{},
				result:      nil,
				annotations: map[string]string{},
			},
			want:            false,
			wantErr:         false,
			wantAnnotations: map[string]string{},
		},
		{
			name: "1 old cluster, no annotation",
			args: args{
				fedObject: &fedcorev1a1.FederatedObject{Status: fedcorev1a1.GenericFederatedObjectStatus{
					Clusters: []fedcorev1a1.PropagationStatus{
						{
							Cluster:                "1",
							Status:                 fedcorev1a1.ClusterPropagationOK,
							LastObservedGeneration: 1,
						},
					},
				}},
				result:      map[string]*int64{"1": new(int64)},
				annotations: map[string]string{},
			},
			want:            true,
			wantErr:         false,
			wantAnnotations: map[string]string{common.PendingSyncClustersAnnotation: `["1"]`},
		},
		{
			name: "1 old cluster, no update on annotation",
			args: args{
				fedObject: &fedcorev1a1.FederatedObject{Status: fedcorev1a1.GenericFederatedObjectStatus{
					Clusters: []fedcorev1a1.PropagationStatus{
						{
							Cluster:                "1",
							Status:                 fedcorev1a1.ClusterPropagationOK,
							LastObservedGeneration: 1,
						},
					},
				}},
				result:      map[string]*int64{"1": new(int64)},
				annotations: map[string]string{common.PendingSyncClustersAnnotation: `["1"]`},
			},
			want:            false,
			wantErr:         false,
			wantAnnotations: map[string]string{common.PendingSyncClustersAnnotation: `["1"]`},
		},
		{
			name: "1 old cluster, replace cluster",
			args: args{
				fedObject: &fedcorev1a1.FederatedObject{Status: fedcorev1a1.GenericFederatedObjectStatus{
					Clusters: []fedcorev1a1.PropagationStatus{
						{
							Cluster:                "1",
							Status:                 fedcorev1a1.ClusterPropagationOK,
							LastObservedGeneration: 1,
						},
					},
				}},
				result:      map[string]*int64{"2": new(int64)},
				annotations: map[string]string{common.PendingSyncClustersAnnotation: `["1"]`},
			},
			want:            true,
			wantErr:         false,
			wantAnnotations: map[string]string{common.PendingSyncClustersAnnotation: `["1","2"]`},
		},
		{
			name: "1 old cluster, add 1 cluster",
			args: args{
				fedObject: &fedcorev1a1.FederatedObject{Status: fedcorev1a1.GenericFederatedObjectStatus{
					Clusters: []fedcorev1a1.PropagationStatus{
						{
							Cluster:                "1",
							Status:                 fedcorev1a1.ClusterPropagationOK,
							LastObservedGeneration: 1,
						},
					},
				}},
				result:      map[string]*int64{"1": new(int64), "0": new(int64)},
				annotations: map[string]string{common.PendingSyncClustersAnnotation: `["1"]`},
			},
			want:            true,
			wantErr:         false,
			wantAnnotations: map[string]string{common.PendingSyncClustersAnnotation: `["0","1"]`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UpdatePendingSyncClusters(tt.args.fedObject, tt.args.result, tt.args.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdatePendingSyncClusters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Fatalf("UpdatePendingSyncClusters() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.args.annotations, tt.wantAnnotations) {
				t.Fatalf("UpdatePendingSyncClusters() got = %v, want %v", tt.args.annotations, tt.wantAnnotations)
			}
		})
	}
}
