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

package core

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	fedcore "github.com/kubewharf/kubeadmiral/pkg/apis/core"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/runtime"
)

type naiveReplicasPlugin struct{}

func (n *naiveReplicasPlugin) Name() string {
	return "NaiveReplicas"
}

func (n *naiveReplicasPlugin) ReplicaScheduling(
	ctx context.Context,
	su *framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (framework.ClusterReplicasList, *framework.Result) {
	res := make(framework.ClusterReplicasList, len(clusters))
	for i, c := range clusters {
		res[i] = framework.ClusterReplicas{Cluster: c, Replicas: 1}
	}
	return res, framework.NewResult(framework.Success)
}

func newNaiveReplicas(_ framework.Handle) (framework.Plugin, error) {
	return &naiveReplicasPlugin{}, nil
}

func getFramework() framework.Framework {
	DefaultRegistry := runtime.Registry{
		"NaiveReplicas": newNaiveReplicas,
	}
	f, _ := runtime.NewFramework(DefaultRegistry, nil, &fedcore.EnabledPlugins{ReplicasPlugins: []string{"NaiveReplicas"}})
	return f
}

func TestSchedulingWithSchedulingMode(t *testing.T) {
	clusters := []*fedcorev1a1.FederatedCluster{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster1"},
			Status: fedcorev1a1.FederatedClusterStatus{
				Conditions: []fedcorev1a1.ClusterCondition{
					{
						Type:   fedcorev1a1.ClusterJoined,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster2"},
			Status: fedcorev1a1.FederatedClusterStatus{
				Conditions: []fedcorev1a1.ClusterCondition{
					{
						Type:   fedcorev1a1.ClusterJoined,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
	}
	scheduler := NewSchedulerAlgorithm()

	t.Run("Duplicate mode should skip replicas scheduling", func(t *testing.T) {
		schedulingUnit := &framework.SchedulingUnit{
			StickyCluster:   true,
			DesiredReplicas: pointer.Int64(10),
			SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
		}
		result, err := scheduler.Schedule(context.TODO(), getFramework(), *schedulingUnit, clusters)
		if err != nil {
			t.Errorf("unexpected error when scheduling: %v", err)
		}
		if len(result.SuggestedClusters) != 2 {
			t.Errorf("unexpected number of scheduled clusters %d, expected 2", len(result.SuggestedClusters))
		}
		for _, v := range result.SuggestedClusters {
			if v != nil {
				t.Errorf("unexpected replica scheduling, want nil replicas but got %d", v)
			}
		}
	})

	t.Run("Divide mode should do replicas scheduling", func(t *testing.T) {
		schedulingUnit := &framework.SchedulingUnit{
			StickyCluster:   true,
			DesiredReplicas: pointer.Int64(10),
			SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
		}
		result, err := scheduler.Schedule(context.TODO(), getFramework(), *schedulingUnit, clusters)
		if err != nil {
			t.Errorf("unexpected error when scheduling: %v", err)
		}
		if len(result.SuggestedClusters) != 2 {
			t.Errorf("unexpected number of scheduled clusters %d, expected 2", len(result.SuggestedClusters))
		}
		for _, v := range result.SuggestedClusters {
			if v == nil {
				t.Errorf("unexpected replicas scheduling skipped")
			}
		}
	})
}

func TestSchedulingWithStickyCluster(t *testing.T) {
	clusters := []*fedcorev1a1.FederatedCluster{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster1"},
			Status: fedcorev1a1.FederatedClusterStatus{
				Conditions: []fedcorev1a1.ClusterCondition{
					{
						Type:   fedcorev1a1.ClusterJoined,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster2"},
			Status: fedcorev1a1.FederatedClusterStatus{
				Conditions: []fedcorev1a1.ClusterCondition{
					{
						Type:   fedcorev1a1.ClusterJoined,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
	}
	scheduler := NewSchedulerAlgorithm()

	t.Run("should schedule the first time", func(t *testing.T) {
		schedulingUnit := &framework.SchedulingUnit{
			StickyCluster:   true,
			DesiredReplicas: pointer.Int64(10),
			SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
		}
		result, err := scheduler.Schedule(context.TODO(), getFramework(), *schedulingUnit, clusters)
		if err != nil {
			t.Errorf("unexpected error when scheduling: %v", err)
		}
		expectedResult := map[string]*int64{
			"cluster1": pointer.Int64(1),
			"cluster2": pointer.Int64(1),
		}
		if !reflect.DeepEqual(result.SuggestedClusters, expectedResult) {
			t.Errorf("expected stickycluster to schedule for the first time")
		}
	})

	t.Run("should not reschedule after first time", func(t *testing.T) {
		currentReplicas := map[string]*int64{
			"cluster1": pointer.Int64(60),
		}
		schedulingUnit := &framework.SchedulingUnit{
			StickyCluster:   true,
			DesiredReplicas: pointer.Int64(10),
			SchedulingMode:  fedcorev1a1.SchedulingModeDivide,
			CurrentClusters: currentReplicas,
		}
		result, err := scheduler.Schedule(context.TODO(), getFramework(), *schedulingUnit, clusters)
		if err != nil {
			t.Errorf("unexpected error when scheduling: %v", err)
		}
		if !reflect.DeepEqual(result.SuggestedClusters, currentReplicas) {
			t.Errorf("expected stickycluster to not reschedule after first time")
		}
	})
}
