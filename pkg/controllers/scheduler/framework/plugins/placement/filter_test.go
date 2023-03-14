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

package placement

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func makeCluster(clusterName string) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
	}
}

func TestPlacementFilterPlugin(t *testing.T) {
	tests := []struct {
		name           string
		su             *framework.SchedulingUnit
		cluster        *fedcorev1a1.FederatedCluster
		expectedResult *framework.Result
	}{
		{
			"cluster should not be filtered when clusterNames is empty",
			&framework.SchedulingUnit{
				ClusterNames: map[string]struct{}{},
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Success),
		},
		{
			"cluster should not be filtered when clusterNames is nil",
			&framework.SchedulingUnit{
				ClusterNames: nil,
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Success),
		},
		{
			"cluster should be filtered when name not in clusterNames",
			&framework.SchedulingUnit{
				ClusterNames: map[string]struct{}{
					"cluster2": {},
					"cluster3": {},
				},
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Error),
		},
		{
			"cluster should not be filtered when name in clusterNames",
			&framework.SchedulingUnit{
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
				},
			},
			makeCluster("cluster1"),
			framework.NewResult(framework.Success),
		},
	}

	p, _ := NewPlacementFilter(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := p.(framework.FilterPlugin).Filter(context.TODO(), test.su, test.cluster)
			if result.IsSuccess() != test.expectedResult.IsSuccess() {
				t.Errorf("result does not match: %v, want %v", result, test.expectedResult)
			}
		})
	}
}
