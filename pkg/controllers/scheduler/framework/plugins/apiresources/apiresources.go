// The design of this plugin is heavily inspired by karmada-scheduler. Kudos!

package apiresources

import (
	"context"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

const (
	APIResourcesName = "APIResources"
)

type APIResources struct{}

func (pl *APIResources) Name() string {
	return APIResourcesName
}

func NewAPIResources(_ framework.Handle) (framework.Plugin, error) {
	return &APIResources{}, nil
}

func (pl *APIResources) Filter(ctx context.Context, su *framework.SchedulingUnit, cluster *fedcorev1a1.FederatedCluster) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	GVK := getGroupVersionKind(su)
	enableGVKs := getEnabledGroupVersionKinds(cluster)
	for _, enableGVK := range enableGVKs {
		if enableGVK == GVK {
			return framework.NewResult(framework.Success)
		}
	}
	return framework.NewResult(framework.Unschedulable, "No matched group version kind.")
}

func getEnabledGroupVersionKinds(cluster *fedcorev1a1.FederatedCluster) []string {
	gvks := []string{}
	for _, r := range cluster.Status.APIResourceTypes {
		gvks = append(gvks, framework.GVKString(r))
	}
	return gvks
}

func getGroupVersionKind(su *framework.SchedulingUnit) string {
	return su.GroupVersionKind
}
