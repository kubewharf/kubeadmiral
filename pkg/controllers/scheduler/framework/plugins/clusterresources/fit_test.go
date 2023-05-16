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

package clusterresources

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

var extendedResourceA = corev1.ResourceName("example.com/aaa")

func makeClusterWithScalarResource(clusterName string, scalarA int64) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Status: fedcorev1a1.FederatedClusterStatus{
			Resources: fedcorev1a1.Resources{
				Allocatable: corev1.ResourceList{
					extendedResourceA: *resource.NewQuantity(scalarA, resource.BinarySI),
				},
				Available: corev1.ResourceList{
					extendedResourceA: *resource.NewQuantity(scalarA, resource.BinarySI),
				},
			},
		},
	}
}

func makeSchedulingUnitWithScalarResource(suName string, scalarA int64) *framework.SchedulingUnit {
	return &framework.SchedulingUnit{
		Name: suName,
		ResourceRequest: framework.Resource{
			ScalarResources: map[corev1.ResourceName]int64{
				extendedResourceA: scalarA,
			},
		},
	}
}

func getErrReason(rn corev1.ResourceName) string {
	return fmt.Sprintf("Insufficient %v", rn)
}

func TestEnoughRequests(t *testing.T) {
	enoughschedulingUnitsTests := []struct {
		name       string
		su         *framework.SchedulingUnit
		cluster    *fedcorev1a1.FederatedCluster
		wantResult *framework.Result
	}{
		{
			su:         makeSchedulingUnit("su", 0, 0),
			cluster:    makeCluster("cluster1", 10, 20, 0, 0),
			name:       "no resources requested always fits",
			wantResult: framework.NewResult(framework.Success),
		},
		{
			su:         makeSchedulingUnit("su", 10, 20),
			cluster:    makeCluster("cluster1", 10, 20, 10, 20),
			name:       "equal edge case requested fits",
			wantResult: framework.NewResult(framework.Success),
		},
		{
			su:         makeSchedulingUnit("su", 1, 1),
			cluster:    makeCluster("cluster", 10, 20, 0, 0),
			name:       "too many resources fails",
			wantResult: framework.NewResult(framework.Unschedulable, getErrReason(corev1.ResourceCPU), getErrReason(corev1.ResourceMemory)),
		},
		{
			su:         makeSchedulingUnit("su", 4, 2),
			cluster:    makeCluster("cluster", 10, 20, 3, 3),
			name:       "cpu predicate resources fails",
			wantResult: framework.NewResult(framework.Unschedulable, getErrReason(corev1.ResourceCPU)),
		},
		{
			su:         makeSchedulingUnit("su", 4, 2),
			cluster:    makeCluster("cluster", 10, 20, 5, 1),
			name:       "memory predicate resources fails",
			wantResult: framework.NewResult(framework.Unschedulable, getErrReason(corev1.ResourceMemory)),
		},
		{
			su:         makeSchedulingUnitWithScalarResource("su", 1),
			cluster:    makeClusterWithScalarResource("cluster", 2),
			name:       "scalar resource predicate resources success",
			wantResult: framework.NewResult(framework.Success),
		},
		{
			su:         makeSchedulingUnitWithScalarResource("su", 1),
			cluster:    makeClusterWithScalarResource("cluster", 0),
			name:       "scalar predicate resources fails",
			wantResult: framework.NewResult(framework.Unschedulable, getErrReason(extendedResourceA)),
		},
		{
			su:         makeSchedulingUnitWithScalarResource("su", 0),
			cluster:    makeClusterWithScalarResource("cluster", 0),
			name:       "scalar predicate resources success",
			wantResult: framework.NewResult(framework.Success),
		},
		{
			su:         makeSchedulingUnitWithScalarResource("su", 1),
			cluster:    makeCluster("cluster", 2, 2, 2, 2),
			name:       "scalar predicate resources fails",
			wantResult: framework.NewResult(framework.Unschedulable, getErrReason(extendedResourceA)),
		},
		{
			su:         makeSchedulingUnitWithScalarResource("su", 0),
			cluster:    makeCluster("cluster", 2, 2, 2, 2),
			name:       "scalar predicate resources fails",
			wantResult: framework.NewResult(framework.Success),
		},
	}

	p, _ := NewClusterResourcesFit(nil)
	for _, test := range enoughschedulingUnitsTests {
		t.Run(test.name, func(t *testing.T) {
			gotStatus := p.(framework.FilterPlugin).Filter(context.TODO(), test.su, test.cluster)
			if !reflect.DeepEqual(gotStatus, test.wantResult) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantResult)
			}
		})
	}
}
