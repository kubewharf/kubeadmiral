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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func Test_getCustomMigrationInfo(t *testing.T) {
	tests := []struct {
		name      string
		fedObject fedcorev1a1.GenericFederatedObject
		want      *framework.CustomMigrationInfo
	}{
		{
			name: "get custom migration info",
			fedObject: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						common.AppliedMigrationConfigurationAnnotation: "{\"limitedCapacity\":{\"cluster1\":1,\"cluster2\":2}}",
					},
				},
			},
			want: &framework.CustomMigrationInfo{
				LimitedCapacity: map[string]int64{
					"cluster1": 1,
					"cluster2": 2,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, _ := getCustomMigrationInfo(test.fedObject); !reflect.DeepEqual(got, test.want) {
				t.Errorf("getCustomMigrationInfo() = %v, want %v", got, test.want)
			}
		})
	}
}

func Test_getPrioritiesFromPolicy(t *testing.T) {
	tests := []struct {
		name           string
		policy         fedcorev1a1.GenericPropagationPolicy
		wantPriorities map[string]int64
	}{
		{
			name: "placement is nil",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{},
			},
			wantPriorities: nil,
		},
		{
			name: "placement is not nil",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					Placements: []fedcorev1a1.DesiredPlacement{
						{
							Cluster: "test1",
							Preferences: fedcorev1a1.Preferences{
								Priority: pointer.Int64(1),
							},
						},
						{
							Cluster: "test2",
							Preferences: fedcorev1a1.Preferences{
								Priority: pointer.Int64(2),
							},
						},
					},
				},
			},
			wantPriorities: map[string]int64{
				"test1": 1,
				"test2": 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if priorities := getPrioritiesFromPolicy(tt.policy); !reflect.DeepEqual(tt.wantPriorities, priorities) {
				t.Errorf("getPrioritiesFromPolicy(), want %v, but got %v", tt.wantPriorities, priorities)
			}
		})
	}
}

func Test_getReplicasStrategyFromPolicy(t *testing.T) {
	spread := fedcorev1a1.ReplicasStrategySpread
	binpack := fedcorev1a1.ReplicasStrategyBinpack
	tests := []struct {
		name                 string
		policy               fedcorev1a1.GenericPropagationPolicy
		wantReplicasStrategy fedcorev1a1.ReplicasStrategy
	}{
		{
			name: "replicasStrategy is nil",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ReplicasStrategy: nil,
				},
			},
			wantReplicasStrategy: fedcorev1a1.ReplicasStrategySpread,
		},
		{
			name: "replicasStrategy is spread",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ReplicasStrategy: &spread,
				},
			},
			wantReplicasStrategy: fedcorev1a1.ReplicasStrategySpread,
		},
		{
			name: "replicasStrategy is binpack",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ReplicasStrategy: &binpack,
				},
			},
			wantReplicasStrategy: fedcorev1a1.ReplicasStrategyBinpack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if replicaStrategy := getReplicasStrategyFromPolicy(tt.policy); !reflect.DeepEqual(tt.wantReplicasStrategy,
				replicaStrategy) {
				t.Errorf("getReplicasStrategyFromPolicy(), want %v, but got %v", tt.wantReplicasStrategy,
					replicaStrategy)
			}
		})
	}
}

func Test_getPrioritiesFromObject(t *testing.T) {
	tests := []struct {
		name           string
		fedObj         fedcorev1a1.GenericFederatedObject
		wantPriorities map[string]int64
		wantBool       bool
	}{
		{
			name: "annotations is nil",
			fedObj: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
			wantPriorities: nil,
			wantBool:       false,
		},
		{
			name: "annotation is empty",
			fedObj: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			wantPriorities: nil,
			wantBool:       false,
		},
		{
			name: "unmarshal annotation failed",
			fedObj: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PlacementsAnnotations: "{{\"cluster\": \"test1\", \"preferences\":{\"priority\": \"1312\"}}}",
					},
				},
			},
			wantPriorities: nil,
			wantBool:       false,
		},
		{
			name: "priority is less than 0",
			fedObj: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PlacementsAnnotations: "{{\"cluster\": \"test1\", \"preferences\":{\"priority\": -1}}}",
					},
				},
			},
			wantPriorities: nil,
			wantBool:       false,
		},
		{
			name: "get priorities success",
			fedObj: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PlacementsAnnotations: "{{\"cluster\": \"test1\", \"preferences\":{\"priority\": 1}}}",
					},
				},
			},
			wantPriorities: nil,
			wantBool:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priorities, exists := getPrioritiesFromObject(tt.fedObj)
			if !reflect.DeepEqual(tt.wantPriorities, priorities) || exists != tt.wantBool {
				t.Errorf("getPrioritiesFromPolicy(), want (%v, %v), but got (%v, %v)",
					tt.wantPriorities, tt.wantBool, priorities, exists)
			}
		})
	}
}
