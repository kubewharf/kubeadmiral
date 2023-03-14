package apiresources

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func suWithAPIResource(suName, gvk string) *framework.SchedulingUnit {
	return &framework.SchedulingUnit{
		Name:             suName,
		GroupVersionKind: gvk,
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
			su:   suWithAPIResource("su1", "apps/v1/DaemonSet"),
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
			su:   suWithAPIResource("su1", "apps/v1/DaemonSet"),
			cluster: clusterWithAPIResource("clusterA", []fedcorev1a1.APIResource{
				{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
			}),
			wantResult: framework.NewResult(framework.Unschedulable, "No matched group version kind."),
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
