// The design of this plugin is inspired by karmada-scheduler. Kudos!

package apiresources

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type APIResources struct{}

func (pl *APIResources) Name() string {
	return names.APIResources
}

func NewAPIResources(_ framework.Handle) (framework.Plugin, error) {
	return &APIResources{}, nil
}

func (pl *APIResources) Filter(ctx context.Context, su *framework.SchedulingUnit, cluster *fedcorev1a1.FederatedCluster) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	gvk := su.GroupVersion.WithKind(su.Kind)
	for _, r := range cluster.Status.APIResourceTypes {
		enabledGVK := schema.GroupVersionKind{
			Group:   r.Group,
			Version: r.Version,
			Kind:    r.Kind,
		}
		if enabledGVK == gvk {
			return framework.NewResult(framework.Success)
		}
	}
	return framework.NewResult(framework.Unschedulable, "No matched group version kind.")
}
