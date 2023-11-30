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

package preferencebinpack

import (
	"context"

	"github.com/davecgh/go-spew/spew"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework/plugins/names"
)

type PreferenceBinPack struct{}

var _ framework.ReplicasPlugin = &PreferenceBinPack{}

func NewPreferenceBinPack(frameworkHandle framework.Handle) (framework.Plugin, error) {
	return &PreferenceBinPack{}, nil
}

func (pl *PreferenceBinPack) Name() string {
	return names.PreferenceBinPack
}

func (pl *PreferenceBinPack) ReplicaScheduling(
	ctx context.Context,
	su *framework.SchedulingUnit,
	clusters []*fedcorev1a1.FederatedCluster,
) (framework.ClusterReplicasList, *framework.Result) {
	clusterReplicasList := make(framework.ClusterReplicasList, 0)
	totalReplicas := int64(0)
	if su.DesiredReplicas != nil {
		totalReplicas = *su.DesiredReplicas
	}

	currentReplicas := map[string]int64{}
	for cluster, replicas := range su.CurrentClusters {
		if replicas != nil {
			currentReplicas[cluster] = *replicas
			continue
		}
		currentReplicas[cluster] = totalReplicas
	}

	clusterPreferences := map[string]ClusterPreferences{}
	for _, cluster := range clusters {
		pref := ClusterPreferences{
			MinReplicas: su.MinReplicas[cluster.Name],
			MaxReplicas: nil,
		}

		if maxReplicas, exists := su.MaxReplicas[cluster.Name]; exists {
			pref.MaxReplicas = pointer.Int64(maxReplicas)
		}

		// if member cluster has untolerated NoSchedule taint, no new replicas will be scheduled to this cluster
		if _, isUntolerated := framework.FindMatchingUntoleratedTaint(
			cluster.Spec.Taints,
			su.Tolerations,
			func(t *corev1.Taint) bool {
				return t.Effect == corev1.TaintEffectNoSchedule
			},
		); isUntolerated {
			if pref.MaxReplicas == nil || currentReplicas[cluster.Name] < *pref.MaxReplicas {
				pref.MaxReplicas = pointer.Int64(currentReplicas[cluster.Name])
			}
		}

		clusterPreferences[cluster.Name] = pref
	}

	estimatedCapacity := map[string]int64{}
	scheduledReplicas := map[string]int64{}
	keepUnschedulableReplicas := false
	if autoMigration := su.AutoMigration; autoMigration != nil {
		keepUnschedulableReplicas = autoMigration.KeepUnschedulableReplicas
		if info := autoMigration.Info; info != nil {
			for cluster, ec := range info.EstimatedCapacity {
				if ec >= 0 {
					estimatedCapacity[cluster] = ec
				}
			}
			scheduledReplicas = su.AutoMigration.Info.ScheduledReplicas
		}
	}

	limitedCapacity := map[string]int64{}
	if su.CustomMigration.Info != nil && su.CustomMigration.Info.LimitedCapacity != nil {
		limitedCapacity = su.CustomMigration.Info.LimitedCapacity
	}

	scheduleResult, overflow, err := Plan(
		&ReplicaSchedulingPreference{
			Clusters: clusterPreferences,
		},
		totalReplicas,
		framework.ExtractClusterNames(clusters),
		currentReplicas,
		estimatedCapacity,
		limitedCapacity,
		su.AvoidDisruption,
		keepUnschedulableReplicas,
		scheduledReplicas,
	)
	if err != nil {
		return clusterReplicasList, framework.NewResult(framework.Error)
	}

	klog.V(4).Infof(
		"[scheduling] for %q clusterPreferences: %s, estimatedCapacity: %v, scheduledReplicas: %v, currentReplicas: %v, result: %v, overflow: %v",
		su.Key(), spew.Sprint(clusterPreferences), estimatedCapacity, scheduledReplicas, currentReplicas, scheduleResult, overflow,
	)

	result := make(map[string]int64)
	for clusterName, replicas := range scheduleResult {
		result[clusterName] = replicas
	}
	for clusterName, replicas := range overflow {
		result[clusterName] += replicas
	}

	for _, cluster := range clusters {
		replicas, ok := result[cluster.Name]
		if !ok || replicas == 0 {
			continue
		}
		clusterReplicasList = append(clusterReplicasList, framework.ClusterReplicas{
			Cluster:  cluster,
			Replicas: replicas,
		})
	}
	return clusterReplicasList, framework.NewResult(framework.Success)
}
