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

package tainttoleration

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func clusterWithTaints(clusterName string, taints []corev1.Taint) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Spec: fedcorev1a1.FederatedClusterSpec{
			Taints: taints,
		},
	}
}

func suWithTolerations(suName string, tolerations []corev1.Toleration) *framework.SchedulingUnit {
	return &framework.SchedulingUnit{
		Name:        suName,
		Tolerations: tolerations,
	}
}

func TestTaintTolerationScore(t *testing.T) {
	tests := []struct {
		name         string
		su           *framework.SchedulingUnit
		clusters     []*fedcorev1a1.FederatedCluster
		expectedList framework.ClusterScoreList
	}{
		// basic test case
		{
			name: "cluster with taints tolerated by the schedulingUnit, gets a higher score than those cluster with intolerable taints",
			su: suWithTolerations("su1", []corev1.Toleration{{
				Key:      "foo",
				Operator: corev1.TolerationOpEqual,
				Value:    "bar",
				Effect:   corev1.TaintEffectPreferNoSchedule,
			}}),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithTaints("clusterA", []corev1.Taint{{
					Key:    "foo",
					Value:  "bar",
					Effect: corev1.TaintEffectPreferNoSchedule,
				}}),
				clusterWithTaints("clusterB", []corev1.Taint{{
					Key:    "foo",
					Value:  "blah",
					Effect: corev1.TaintEffectPreferNoSchedule,
				}}),
			},
			expectedList: framework.ClusterScoreList{
				{
					Cluster: clusterWithTaints("clusterA", []corev1.Taint{{
						Key:    "foo",
						Value:  "bar",
						Effect: corev1.TaintEffectPreferNoSchedule,
					}}),
					Score: framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterB", []corev1.Taint{{
						Key:    "foo",
						Value:  "blah",
						Effect: corev1.TaintEffectPreferNoSchedule,
					}}),
					Score: 0,
				},
			},
		},
		// the count of taints that are tolerated by su, does not matter.
		{
			name: "the clusters that all of their taints are tolerated by the schedulingUnit, " +
				"get the same score, no matter how many tolerable taints a cluster has",
			su: suWithTolerations("su1", []corev1.Toleration{
				{
					Key:      "cpu-type",
					Operator: corev1.TolerationOpEqual,
					Value:    "arm64",
					Effect:   corev1.TaintEffectPreferNoSchedule,
				}, {
					Key:      "disk-type",
					Operator: corev1.TolerationOpEqual,
					Value:    "ssd",
					Effect:   corev1.TaintEffectPreferNoSchedule,
				},
			}),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithTaints("clusterA", []corev1.Taint{}),
				clusterWithTaints("clusterB", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectPreferNoSchedule,
					},
				}),
				clusterWithTaints("clusterC", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectPreferNoSchedule,
					}, {
						Key:    "disk-type",
						Value:  "ssd",
						Effect: corev1.TaintEffectPreferNoSchedule,
					},
				}),
			},
			expectedList: framework.ClusterScoreList{
				{
					Cluster: clusterWithTaints("clusterA", []corev1.Taint{}),
					Score:   framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterB", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectPreferNoSchedule,
						},
					}),
					Score: framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterC", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectPreferNoSchedule,
						}, {
							Key:    "disk-type",
							Value:  "ssd",
							Effect: corev1.TaintEffectPreferNoSchedule,
						},
					}),
					Score: framework.MaxClusterScore,
				},
			},
		},
		// the count of taints on a cluster that are not tolerated by su, matters.
		{
			name: "the more intolerable taints a cluster has, the lower score it gets.",
			su: suWithTolerations("su1", []corev1.Toleration{{
				Key:      "foo",
				Operator: corev1.TolerationOpEqual,
				Value:    "bar",
				Effect:   corev1.TaintEffectPreferNoSchedule,
			}}),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithTaints("clusterA", []corev1.Taint{}),
				clusterWithTaints("clusterB", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectPreferNoSchedule,
					},
				}),
				clusterWithTaints("clusterC", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectPreferNoSchedule,
					}, {
						Key:    "disk-type",
						Value:  "ssd",
						Effect: corev1.TaintEffectPreferNoSchedule,
					},
				}),
			},
			expectedList: framework.ClusterScoreList{
				{
					Cluster: clusterWithTaints("clusterA", []corev1.Taint{}),
					Score:   framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterB", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectPreferNoSchedule,
						},
					}),
					Score: 50,
				},
				{
					Cluster: clusterWithTaints("clusterC", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectPreferNoSchedule,
						}, {
							Key:    "disk-type",
							Value:  "ssd",
							Effect: corev1.TaintEffectPreferNoSchedule,
						},
					}),
					Score: 0,
				},
			},
		},
		// taints-tolerations priority only takes care about the taints and tolerations that have effect PreferNoSchedule
		{
			name: "only taints and tolerations that have effect PreferNoSchedule are checked by taints-tolerations priority function",
			su: suWithTolerations("su1", []corev1.Toleration{
				{
					Key:      "cpu-type",
					Operator: corev1.TolerationOpEqual,
					Value:    "arm64",
					Effect:   corev1.TaintEffectNoSchedule,
				}, {
					Key:      "disk-type",
					Operator: corev1.TolerationOpEqual,
					Value:    "ssd",
					Effect:   corev1.TaintEffectNoSchedule,
				},
			}),
			clusters: []*fedcorev1a1.FederatedCluster{
				clusterWithTaints("clusterA", []corev1.Taint{}),
				clusterWithTaints("clusterB", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectNoSchedule,
					},
				}),
				clusterWithTaints("clusterC", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectPreferNoSchedule,
					}, {
						Key:    "disk-type",
						Value:  "ssd",
						Effect: corev1.TaintEffectPreferNoSchedule,
					},
				}),
			},
			expectedList: framework.ClusterScoreList{
				{
					Cluster: clusterWithTaints("clusterA", []corev1.Taint{}),
					Score:   framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterB", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectNoSchedule,
						},
					}),
					Score: framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterC", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectPreferNoSchedule,
						}, {
							Key:    "disk-type",
							Value:  "ssd",
							Effect: corev1.TaintEffectPreferNoSchedule,
						},
					}),
					Score: 0,
				},
			},
		},
		{
			name: "Default behaviour No taints and tolerations, lands on cluster with no taints",
			// su without tolerations
			su: suWithTolerations("su1", []corev1.Toleration{}),
			clusters: []*fedcorev1a1.FederatedCluster{
				// cluster without taints
				clusterWithTaints("clusterA", []corev1.Taint{}),
				clusterWithTaints("clusterB", []corev1.Taint{
					{
						Key:    "cpu-type",
						Value:  "arm64",
						Effect: corev1.TaintEffectPreferNoSchedule,
					},
				}),
			},
			expectedList: framework.ClusterScoreList{
				{
					Cluster: clusterWithTaints("clusterA", []corev1.Taint{}),
					Score:   framework.MaxClusterScore,
				},
				{
					Cluster: clusterWithTaints("clusterB", []corev1.Taint{
						{
							Key:    "cpu-type",
							Value:  "arm64",
							Effect: corev1.TaintEffectPreferNoSchedule,
						},
					}),
					Score: 0,
				},
			},
		},
	}

	p, _ := NewTaintToleration(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotList := framework.ClusterScoreList{}
			for _, cluster := range test.clusters {
				score, result := p.(framework.ScorePlugin).Score(context.TODO(), test.su, cluster)
				if !result.IsSuccess() {
					t.Errorf("unexpected error: %v", result.AsError())
				}
				gotList = append(gotList, framework.ClusterScore{
					Cluster: cluster,
					Score:   score,
				})
			}

			p.(framework.ScorePlugin).ScoreExtensions().NormalizeScore(context.TODO(), gotList)
			for i := 0; i < len(test.expectedList); i++ {
				if test.expectedList[i].Cluster.Name != gotList[i].Cluster.Name {
					t.Errorf("expected:\n\t%s,\ngot:\n\t%s",
						test.expectedList[i].Cluster.Name, gotList[i].Cluster.Name)
				}
				if test.expectedList[i].Score != gotList[i].Score {
					t.Errorf("expected:\n\t%d,\ngot:\n\t%d",
						test.expectedList[i].Score, gotList[i].Score)
				}
			}
		})
	}
}

func TestTaintTolerationFilter(t *testing.T) {
	tests := []struct {
		name       string
		su         *framework.SchedulingUnit
		cluster    *fedcorev1a1.FederatedCluster
		wantResult *framework.Result
	}{
		{
			name: "A schedulingUnit having no tolerations can't be scheduled onto a cluster with nonempty taints",
			su:   suWithTolerations("su1", []corev1.Toleration{}),
			cluster: clusterWithTaints(
				"clusterA",
				[]corev1.Taint{{Key: "dedicated", Value: "user1", Effect: "NoSchedule"}},
			),
			wantResult: framework.NewResult(framework.Unschedulable,
				"cluster(s) had taint {dedicated: user1}, that the schedulingUnit didn't tolerate"),
		},
		{
			name: "A schedulingUnit which can be scheduled on a dedicated cluster assigned to user1 with effect NoSchedule",
			su: suWithTolerations(
				"su1",
				[]corev1.Toleration{{Key: "dedicated", Value: "user1", Effect: "NoSchedule"}},
			),
			cluster: clusterWithTaints(
				"clusterA",
				[]corev1.Taint{{Key: "dedicated", Value: "user1", Effect: "NoSchedule"}},
			),
			wantResult: framework.NewResult(framework.Success),
		},
		{
			name: "A schedulingUnit which can't be scheduled on a dedicated cluster assigned to user2 with effect NoSchedule",
			su: suWithTolerations(
				"su1",
				[]corev1.Toleration{{Key: "dedicated", Operator: "Equal", Value: "user2", Effect: "NoSchedule"}},
			),
			cluster: clusterWithTaints(
				"clusterA",
				[]corev1.Taint{{Key: "dedicated", Value: "user1", Effect: "NoSchedule"}},
			),
			wantResult: framework.NewResult(framework.Unschedulable,
				"cluster(s) had taint {dedicated: user1}, that the schedulingUnit didn't tolerate"),
		},
		{
			name: "A schedulingUnit can be scheduled onto the cluster, with a toleration uses operator " +
				"Exists that tolerates the taints on the cluster",
			su: suWithTolerations(
				"su1",
				[]corev1.Toleration{{Key: "foo", Operator: "Exists", Effect: "NoSchedule"}},
			),
			cluster:    clusterWithTaints("clusterA", []corev1.Taint{{Key: "foo", Value: "bar", Effect: "NoSchedule"}}),
			wantResult: framework.NewResult(framework.Success),
		},
		{
			name: "A schedulingUnit has multiple tolerations, cluster has multiple taints," +
				"all the taints are tolerated, su can be scheduled onto the cluster",
			su: suWithTolerations("su1", []corev1.Toleration{
				{Key: "dedicated", Operator: "Equal", Value: "user2", Effect: "NoSchedule"},
				{Key: "foo", Operator: "Exists", Effect: "NoSchedule"},
			}),
			cluster: clusterWithTaints("clusterA", []corev1.Taint{
				{Key: "dedicated", Value: "user2", Effect: "NoSchedule"},
				{Key: "foo", Value: "bar", Effect: "NoSchedule"},
			}),
			wantResult: framework.NewResult(framework.Success),
		},
		{
			name: "A schedulingUnit has a toleration that keys and values match the taint on the cluster, but (non-empty) effect doesn't match, " +
				"can't be scheduled onto the cluster",
			su: suWithTolerations(
				"su1",
				[]corev1.Toleration{{Key: "foo", Operator: "Equal", Value: "bar", Effect: "PreferNoSchedule"}},
			),
			cluster: clusterWithTaints("clusterA", []corev1.Taint{{Key: "foo", Value: "bar", Effect: "NoSchedule"}}),
			wantResult: framework.NewResult(framework.Unschedulable,
				"cluster(s) had taint {foo: bar}, that the schedulingUnit didn't tolerate"),
		},
		{
			name: "The schedulingUnit has a toleration that keys and values match the taint on the cluster, the effect of toleration is empty, " +
				"and the effect of taint is NoSchedule. schedulingUnit can be scheduled onto the cluster",
			su:         suWithTolerations("su1", []corev1.Toleration{{Key: "foo", Operator: "Equal", Value: "bar"}}),
			cluster:    clusterWithTaints("clusterA", []corev1.Taint{{Key: "foo", Value: "bar", Effect: "NoSchedule"}}),
			wantResult: framework.NewResult(framework.Success),
		},
		{
			name: "The schedulingUnit has a toleration that key and value don't match the taint on the cluster, " +
				"but the effect of taint on cluster is PreferNoSchedule. schedulingUnit can be scheduled onto the cluster",
			su: suWithTolerations(
				"su1",
				[]corev1.Toleration{{Key: "dedicated", Operator: "Equal", Value: "user2", Effect: "NoSchedule"}},
			),
			cluster: clusterWithTaints(
				"clusterA",
				[]corev1.Taint{{Key: "dedicated", Value: "user1", Effect: "PreferNoSchedule"}},
			),
			wantResult: framework.NewResult(framework.Success),
		},
		{
			name: "The schedulingUnit has no toleration, " +
				"but the effect of taint on cluster is PreferNoSchedule. schedulingUnit can be scheduled onto the cluster",
			su: suWithTolerations("su1", []corev1.Toleration{}),
			cluster: clusterWithTaints(
				"clusterA",
				[]corev1.Taint{{Key: "dedicated", Value: "user1", Effect: "PreferNoSchedule"}},
			),
			wantResult: framework.NewResult(framework.Success),
		},
	}

	p, _ := NewTaintToleration(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotStatus := p.(framework.FilterPlugin).Filter(context.TODO(), test.su, test.cluster)
			if !reflect.DeepEqual(gotStatus, test.wantResult) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantResult)
			}
		})
	}
}
