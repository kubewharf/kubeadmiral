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

package clusteraffinity

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func TestClusterAffinity(t *testing.T) {
	tests := []struct {
		su          *framework.SchedulingUnit
		labels      map[string]string
		clusterName string
		name        string
		wantResult  *framework.Result
	}{
		{
			su:   &framework.SchedulingUnit{},
			name: "no selector",
		},
		{
			su: &framework.SchedulingUnit{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			name:       "missing labels",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "same labels",
		},
		{
			su: &framework.SchedulingUnit{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			labels: map[string]string{
				"foo": "bar",
				"baz": "blah",
			},
			name: "cluster labels are superset",
		},
		{
			su: &framework.SchedulingUnit{
				ClusterSelector: map[string]string{
					"foo": "bar",
					"baz": "blah",
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name:       "cluster labels are subset",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"bar", "value2"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with matchExpressions using In operator that matches the existing cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "kernel-version",
											Operator: fedcorev1a1.ClusterSelectorOpGt,
											Values:   []string{"0204"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				// We use two digit to denote major version and two digit for minor version.
				"kernel-version": "0206",
			},
			name: "Scheduling unit with matchExpressions using Gt operator that matches the existing cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "mem-type",
											Operator: fedcorev1a1.ClusterSelectorOpNotIn,
											Values:   []string{"DDR", "DDR2"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"mem-type": "DDR3",
			},
			name: "Scheduling unit with matchExpressions using NotIn operator that matches the existing cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "GPU",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"GPU": "NVIDIA-GRID-K1",
			},
			name: "Scheduling unit with matchExpressions using Exists operator that matches the existing cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"value1", "value2"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name:       "Scheduling unit with affinity that don't match cluster's labels won't schedule onto the cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: nil,
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with a nil []ClusterSelectorTerm in affinity, can't match the cluster's labels " +
				"and won't schedule onto the cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with an empty []ClusterSelectorTerm in affinity, can't match the cluster's labels " +
				"and won't schedule onto the cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name:       "Scheduling unit with empty MatchExpressions is not a valid value will match no objects and won't schedule onto the cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with no Affinity will schedule onto a cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: nil,
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with Affinity but nil NodeSelector will schedule onto a cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "GPU",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
										},
										{
											Key:      "GPU",
											Operator: fedcorev1a1.ClusterSelectorOpNotIn,
											Values:   []string{"AMD", "INTEL"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"GPU": "NVIDIA-GRID-K1",
			},
			name: "Scheduling unit with multiple matchExpressions ANDed that matches the existing cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "GPU",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
										},
										{
											Key:      "GPU",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"AMD", "INTEL"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"GPU": "NVIDIA-GRID-K1",
			},
			name:       "Scheduling unit with multiple matchExpressions ANDed that doesn't match the existing cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"bar", "value2"},
										},
									},
								},
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "diffkey",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"wrong", "value2"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with multiple NodeSelectorTerms ORed in affinity, matches the cluster's labels and " +
				"will schedule onto the cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
										},
									},
								},
							},
						},
					},
				},
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name: "Scheduling unit with an Affinity and a Scheduling unitSpec.NodeSelector(the old thing that we are deprecating) " +
				"both are satisfied, will schedule onto the cluster",
		},
		{
			su: &framework.SchedulingUnit{
				ClusterSelector: map[string]string{
					"foo": "bar",
				},
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpExists,
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "barrrrrr",
			},
			name: "Scheduling unit with an Affinity matches cluster's labels but the Scheduling unitSpec.ClusterSelector" +
				"(the old thing that we are deprecating) " +
				"is not satisfied, won't schedule onto the cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpNotIn,
											Values:   []string{"invalid value: ___@#$%^"},
										},
									},
								},
							},
						},
					},
				},
			},
			labels: map[string]string{
				"foo": "bar",
			},
			name:       "Scheduling unit with an invalid value in Affinity term won't be scheduled onto the cluster",
			wantResult: framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "metadata.name",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"cluster_1"},
										},
									},
								},
							},
						},
					},
				},
			},
			clusterName: "cluster_1",
			name:        "Scheduling unit with matchFields using In operator that matches the existing cluster",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "metadata.name",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"cluster_1"},
										},
									},
								},
							},
						},
					},
				},
			},
			clusterName: "cluster_2",
			name:        "Scheduling unit with matchFields using In operator that does not match the existing cluster",
			wantResult:  framework.NewResult(framework.Unschedulable, ErrReason),
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "metadata.name",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"cluster_1"},
										},
									},
								},
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"bar"},
										},
									},
								},
							},
						},
					},
				},
			},
			clusterName: "cluster_2",
			labels:      map[string]string{"foo": "bar"},
			name:        "Scheduling unit with two terms: matchFields does not match, but matchExpressions matches",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "metadata.name",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"cluster_1"},
										},
									},
								},
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"bar"},
										},
									},
								},
							},
						},
					},
				},
			},
			clusterName: "cluster_2",
			labels:      map[string]string{"foo": "bar"},
			name:        "Scheduling unit with one term: matchFields does not match, but matchExpressions matches",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "metadata.name",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"cluster_1"},
										},
									},
								},
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"bar"},
										},
									},
								},
							},
						},
					},
				},
			},
			clusterName: "cluster_1",
			labels:      map[string]string{"foo": "bar"},
			name:        "Scheduling unit with one term: both matchFields and matchExpressions match",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchFields: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "metadata.name",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"cluster_1"},
										},
									},
								},
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "foo",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"not-match-to-bar"},
										},
									},
								},
							},
						},
					},
				},
			},
			clusterName: "cluster_2",
			labels:      map[string]string{"foo": "bar"},
			name:        "Scheduling unit with two terms: both matchFields and matchExpressions do not match",
			wantResult:  framework.NewResult(framework.Unschedulable, ErrReason),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := fedcorev1a1.FederatedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   test.clusterName,
					Labels: test.labels,
				},
			}
			if test.wantResult == nil {
				test.wantResult = framework.NewResult(framework.Success)
			}
			p, _ := NewClusterAffinity(nil)
			gotStatus := p.(framework.FilterPlugin).Filter(context.TODO(), test.su, &cluster)
			if !reflect.DeepEqual(gotStatus, test.wantResult) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantResult)
			}
		})
	}
}

func TestClusterAffinityPriority(t *testing.T) {
	label1 := map[string]string{"foo": "bar"}
	label2 := map[string]string{"key": "value"}
	label3 := map[string]string{"az": "az1"}
	label4 := map[string]string{"abc": "az11", "def": "az22"}
	label5 := map[string]string{"foo": "bar", "key": "value", "az": "az1"}

	affinity1 := framework.Affinity{
		ClusterAffinity: &framework.ClusterAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []framework.PreferredSchedulingTerm{
				{
					Weight: 2,
					Preference: fedcorev1a1.ClusterSelectorTerm{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "foo",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"bar"},
							},
						},
					},
				},
			},
		},
	}

	affinity2 := framework.Affinity{
		ClusterAffinity: &framework.ClusterAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []framework.PreferredSchedulingTerm{
				{
					Weight: 2,
					Preference: fedcorev1a1.ClusterSelectorTerm{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "foo",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"bar"},
							},
						},
					},
				},
				{
					Weight: 4,
					Preference: fedcorev1a1.ClusterSelectorTerm{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "key",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"value"},
							},
						},
					},
				},
				{
					Weight: 5,
					Preference: fedcorev1a1.ClusterSelectorTerm{
						MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
							{
								Key:      "foo",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"bar"},
							},
							{
								Key:      "key",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"value"},
							},
							{
								Key:      "az",
								Operator: fedcorev1a1.ClusterSelectorOpIn,
								Values:   []string{"az1"},
							},
						},
					},
				},
			},
		},
	}

	tests := []struct {
		su           *framework.SchedulingUnit
		clusters     []*fedcorev1a1.FederatedCluster
		name         string
		expectedList []int64
	}{
		{
			su: &framework.SchedulingUnit{},
			clusters: []*fedcorev1a1.FederatedCluster{
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster2", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster3", Labels: label3}},
			},
			expectedList: []int64{0, 0, 0},
			name:         "all clusters are same priority as ClusterAffinity is nil",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &affinity1,
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Labels: label4}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster2", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster3", Labels: label3}},
			},
			expectedList: []int64{0, 0, 0},
			name:         "no cluster matches preferred scheduling requirements in ClusterAffinity of pod so all cluster' priority is zero",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &affinity1,
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster2", Labels: label2}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster3", Labels: label3}},
			},
			expectedList: []int64{framework.MaxClusterScore, 0, 0},
			name:         "only cluster1 matches the preferred scheduling requirements of scheduling unit",
		},
		{
			su: &framework.SchedulingUnit{
				Affinity: &affinity2,
			},
			clusters: []*fedcorev1a1.FederatedCluster{
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Labels: label1}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster5", Labels: label5}},
				{ObjectMeta: metav1.ObjectMeta{Name: "cluster2", Labels: label2}},
			},
			expectedList: []int64{18, framework.MaxClusterScore, 36},
			name:         "all machines matches the preferred scheduling requirements of pod but with different priorities ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, _ := NewClusterAffinity(nil)
			gotList := framework.ClusterScoreList{}
			for _, cluster := range test.clusters {
				score, status := p.(framework.ScorePlugin).Score(context.TODO(), test.su, cluster)
				if !status.IsSuccess() {
					t.Errorf("unexpected error: %v", status)
				}
				gotList = append(gotList, framework.ClusterScore{
					Cluster: cluster,
					Score:   score,
				})
			}

			status := p.(framework.ScorePlugin).ScoreExtensions().NormalizeScore(context.TODO(), gotList)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %v", status)
			}

			if len(gotList) != len(test.expectedList) {
				t.Errorf("Then cluster score list is not the same")
			}

			for i := range gotList {
				if gotList[i].Score != test.expectedList[i] {
					t.Errorf("Unexpected cluster score, want: %d, got: %d", test.expectedList[i], gotList[i].Score)
				}
			}
		})
	}
}
