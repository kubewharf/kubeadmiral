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

package v1alpha1

import (
	"sort"
	"testing"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	schedwebhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/test/gomega/custommatchers"
)

func TestConvertSchedulingUnit(t *testing.T) {
	tests := []struct {
		name           string
		su             *framework.SchedulingUnit
		expectedResult *schedwebhookv1a1.SchedulingUnit
	}{
		{
			name: "Deployment duplicate mode",
			su: &framework.SchedulingUnit{
				Name:      "test",
				Namespace: "test",
				GroupVersion: schema.GroupVersion{
					Group:   "apps",
					Version: "v1",
				},
				Kind:     "Deployment",
				Resource: "deployments",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				DesiredReplicas: pointer.Int64(10),
				ResourceRequest: framework.Resource{
					MilliCPU: 2000,
					Memory:   1000,
					ScalarResources: map[corev1.ResourceName]int64{
						"customresource": 2,
					},
				},
				CurrentClusters: map[string]*int64{
					"cluster1": nil,
					"cluster2": nil,
				},
				SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
				StickyCluster:  false,
				ClusterSelector: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "test",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
											Values:   []string{"a", "b", "c"},
										},
									},
								},
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "test",
						Operator: corev1.TolerationOpEqual,
						Value:    "test",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				MaxClusters: pointer.Int64(5),
			},
			expectedResult: &schedwebhookv1a1.SchedulingUnit{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Resource:   "deployments",
				Name:       "test",
				Namespace:  "test",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
				DesiredReplicas: pointer.Int64(10),
				ResourceRequest: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceCPU:              *resource.NewMilliQuantity(2000, resource.DecimalSI),
					corev1.ResourceMemory:           *resource.NewQuantity(1000, resource.BinarySI),
					corev1.ResourceEphemeralStorage: *resource.NewQuantity(0, resource.BinarySI),
					"customresource":                *resource.NewQuantity(2, resource.DecimalSI),
				},
				CurrentClusters:            []string{"cluster1", "cluster2"},
				CurrentReplicaDistribution: nil,
				ClusterSelector: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "test",
								Operator: fedcorev1a1.ClusterSelectorOpExists,
								Values:   []string{"a", "b", "c"},
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "test",
						Operator: corev1.TolerationOpEqual,
						Value:    "test",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				MaxClusters: pointer.Int64(5),
				Placements: []fedcorev1a1.DesiredPlacement{
					{
						Cluster: "cluster1",
					},
					{
						Cluster: "cluster2",
					},
					{
						Cluster: "cluster3",
					},
				},
			},
		},
		{
			name: "Deployment duplicate mode with nil values",
			su: &framework.SchedulingUnit{
				Name:      "test",
				Namespace: "test",
				GroupVersion: schema.GroupVersion{
					Group:   "apps",
					Version: "v1",
				},
				Kind:     "Deployment",
				Resource: "deployments",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				DesiredReplicas: nil,
				ResourceRequest: framework.Resource{},
				CurrentClusters: nil,
				SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
				StickyCluster:   false,
				ClusterSelector: nil,
				ClusterNames:    nil,
				Affinity:        nil,
				Tolerations:     nil,
				MaxClusters:     nil,
			},
			expectedResult: &schedwebhookv1a1.SchedulingUnit{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Resource:   "deployments",
				Name:       "test",
				Namespace:  "test",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
				DesiredReplicas: nil,
				ResourceRequest: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceCPU:              *resource.NewMilliQuantity(0, resource.DecimalSI),
					corev1.ResourceMemory:           *resource.NewQuantity(0, resource.BinarySI),
					corev1.ResourceEphemeralStorage: *resource.NewQuantity(0, resource.BinarySI),
				},
				CurrentClusters:            nil,
				CurrentReplicaDistribution: nil,
				ClusterSelector:            nil,
				ClusterAffinity:            nil,
				Tolerations:                nil,
				MaxClusters:                nil,
				Placements:                 nil,
			},
		},
		{
			name: "Deployment divide mode",
			su: &framework.SchedulingUnit{
				Name:      "test",
				Namespace: "test",
				GroupVersion: schema.GroupVersion{
					Group:   "apps",
					Version: "v1",
				},
				Kind:     "Deployment",
				Resource: "deployments",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				DesiredReplicas: pointer.Int64(10),
				ResourceRequest: framework.Resource{
					MilliCPU: 2000,
					Memory:   1000,
					ScalarResources: map[corev1.ResourceName]int64{
						"customresource": 2,
					},
				},
				CurrentClusters: map[string]*int64{
					"cluster1": pointer.Int64(5),
					"cluster2": pointer.Int64(7),
				},
				SchedulingMode: fedcorev1a1.SchedulingModeDivide,
				StickyCluster:  false,
				ClusterSelector: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
					"cluster3": {},
				},
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "test",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
											Values:   []string{"a", "b", "c"},
										},
									},
								},
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "test",
						Operator: corev1.TolerationOpEqual,
						Value:    "test",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				MaxClusters: pointer.Int64(5),
			},
			expectedResult: &schedwebhookv1a1.SchedulingUnit{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Resource:   "deployments",
				Name:       "test",
				Namespace:  "test",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
				DesiredReplicas: pointer.Int64(10),
				ResourceRequest: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceCPU:              *resource.NewMilliQuantity(2000, resource.DecimalSI),
					corev1.ResourceMemory:           *resource.NewQuantity(1000, resource.BinarySI),
					corev1.ResourceEphemeralStorage: *resource.NewQuantity(0, resource.BinarySI),
					"customresource":                *resource.NewQuantity(2, resource.DecimalSI),
				},
				CurrentClusters: []string{"cluster1", "cluster2"},
				CurrentReplicaDistribution: map[string]int64{
					"cluster1": 5,
					"cluster2": 7,
				},
				ClusterSelector: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				ClusterAffinity: []fedcorev1a1.ClusterSelectorTerm{
					{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "test",
								Operator: fedcorev1a1.ClusterSelectorOpExists,
								Values:   []string{"a", "b", "c"},
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "test",
						Operator: corev1.TolerationOpEqual,
						Value:    "test",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				MaxClusters: pointer.Int64(5),
				Placements: []fedcorev1a1.DesiredPlacement{
					{
						Cluster: "cluster1",
					},
					{
						Cluster: "cluster2",
					},
					{
						Cluster: "cluster3",
					},
				},
			},
		},
		{
			name: "Secret duplicate mode",
			su: &framework.SchedulingUnit{
				Name:      "test",
				Namespace: "test",
				GroupVersion: schema.GroupVersion{
					Group:   "core",
					Version: "v1",
				},
				Kind:     "Secret",
				Resource: "secrets",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				DesiredReplicas: nil,
				ResourceRequest: framework.Resource{},
				CurrentClusters: map[string]*int64{
					"cluster1": nil,
					"cluster2": nil,
					"cluster3": nil,
					"cluster4": nil,
					"cluster5": nil,
				},
				SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
				StickyCluster:   false,
				ClusterSelector: map[string]string{},
				ClusterNames:    nil,
				Affinity:        &framework.Affinity{},
				Tolerations: []corev1.Toleration{
					{
						Key:      "test",
						Operator: corev1.TolerationOpEqual,
						Value:    "test",
						Effect:   corev1.TaintEffectNoExecute,
					},
				},
				MaxClusters: nil,
			},
			expectedResult: &schedwebhookv1a1.SchedulingUnit{
				APIVersion: "core/v1",
				Kind:       "Secret",
				Resource:   "secrets",
				Name:       "test",
				Namespace:  "test",
				Labels: map[string]string{
					"test-label-1-name": "test-label-1-value",
					"test-label-2-name": "test-label-2-value",
				},
				Annotations: map[string]string{
					"test-annotation-1-name": "test-annotation-1-value",
					"test-annotation-2-name": "test-annotation-2-value",
				},
				SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
				DesiredReplicas: nil,
				ResourceRequest: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceCPU:              *resource.NewMilliQuantity(0, resource.DecimalSI),
					corev1.ResourceMemory:           *resource.NewQuantity(0, resource.BinarySI),
					corev1.ResourceEphemeralStorage: *resource.NewQuantity(0, resource.BinarySI),
				},
				CurrentClusters:            []string{"cluster1", "cluster2", "cluster3", "cluster4", "cluster5"},
				CurrentReplicaDistribution: nil,
				ClusterSelector:            map[string]string{},
				ClusterAffinity:            []fedcorev1a1.ClusterSelectorTerm{},
				Tolerations: []corev1.Toleration{
					{
						Key:      "test",
						Operator: corev1.TolerationOpEqual,
						Value:    "test",
						Effect:   corev1.TaintEffectNoExecute,
					},
				},
				MaxClusters: nil,
				Placements:  nil,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted := ConvertSchedulingUnit(test.su)

			// we need to sort currentClusters due to go's maps being unordered
			sort.Slice(converted.CurrentClusters, func(i, j int) bool {
				return converted.CurrentClusters[i] < converted.CurrentClusters[j]
			})
			sort.Slice(test.expectedResult.CurrentClusters, func(i, j int) bool {
				return test.expectedResult.CurrentClusters[i] < test.expectedResult.CurrentClusters[j]
			})
			// we need to sort placements due to go's maps being unordered
			sort.Slice(converted.Placements, func(i, j int) bool {
				return converted.Placements[i].Cluster < converted.Placements[j].Cluster
			})
			sort.Slice(test.expectedResult.Placements, func(i, j int) bool {
				return test.expectedResult.Placements[i].Cluster < test.expectedResult.Placements[j].Cluster
			})

			g := gomega.NewWithT(t)
			g.Expect(converted).To(custommatchers.SemanticallyEqual(test.expectedResult))
		})
	}
}
