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

package apiresources

import (
	"context"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func clusterWithAPIResource(clusterName string, resources []fedcorev1a1.APIResource) *fedcorev1a1.FederatedCluster {
	return &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Status: fedcorev1a1.FederatedClusterStatus{
			APIResourceTypes: resources,
		},
	}
}

func suWithAPIResource(suName string, gvk schema.GroupVersionKind) *framework.SchedulingUnit {
	return &framework.SchedulingUnit{
		Name:         suName,
		GroupVersion: gvk.GroupVersion(),
		Kind:         gvk.Kind,
	}
}

func TestAPIResourcesFilter(t *testing.T) {
	tests := []struct {
		name       string
		su         *framework.SchedulingUnit
		cluster    *fedcorev1a1.FederatedCluster
		wantResult *framework.Result
	}{
		{
			name: "the cluster has the resources for schedulingUnit",
			su:   suWithAPIResource("su1", appsv1.SchemeGroupVersion.WithKind("DaemonSet")),
			cluster: clusterWithAPIResource("clusterA", []fedcorev1a1.APIResource{
				{
					Group:   "apps",
					Version: "v1",
					Kind:    "DaemonSet",
				},
			}),
			wantResult: framework.NewResult(framework.Success),
		},
		{
			name: "the cluster does not have the resources for schedulingUnit",
			su:   suWithAPIResource("su1", appsv1.SchemeGroupVersion.WithKind("DaemonSet")),
			cluster: clusterWithAPIResource("clusterA", []fedcorev1a1.APIResource{
				{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
			}),
			wantResult: framework.NewResult(framework.Unschedulable, "cluster(s) didn't support this APIVersion"),
		},
	}

	p, _ := NewAPIResources(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotStatus := p.(framework.FilterPlugin).Filter(context.TODO(), test.su, test.cluster)
			if !reflect.DeepEqual(gotStatus, test.wantResult) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantResult)
			}
		})
	}
}
