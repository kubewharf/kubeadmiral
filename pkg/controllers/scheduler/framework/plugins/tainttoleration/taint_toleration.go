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
	"fmt"

	corev1 "k8s.io/api/core/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

const (
	TaintTolerationName = "TaintToleration"
)

type TaintToleration struct{}

func NewTaintToleration(_ framework.FrameworkHandle) (framework.Plugin, error) {
	return &TaintToleration{}, nil
}

func (pl *TaintToleration) Name() string {
	return TaintTolerationName
}

func (pl *TaintToleration) Filter(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) *framework.Result {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	taints, err := getFederatedClusterTaints(cluster)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	tolerations, err := getSchedulingUnitTolerations(su)
	if err != nil {
		return framework.NewResult(framework.Error, err.Error())
	}

	isClusterScheduled := false
	for c := range su.CurrentClusters {
		if c == cluster.Name {
			isClusterScheduled = true
			break
		}
	}

	filterPredicate := func(t *corev1.Taint) bool {
		// if the cluster is already scheduled, we ignore NoSchedule taints and only care about NoExecute
		if isClusterScheduled {
			return t.Effect == corev1.TaintEffectNoExecute
		}
		return t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute
	}

	taint, isUntolerated := framework.FindMatchingUntoleratedTaint(taints, tolerations, filterPredicate)
	if !isUntolerated {
		return framework.NewResult(framework.Success)
	}

	errReason := fmt.Sprintf("cluster(s) had taint {%s: %s}, that the schedulingUnit didn't tolerate",
		taint.Key, taint.Value)

	return framework.NewResult(framework.Unschedulable, errReason)
}

func (pl *TaintToleration) Score(
	ctx context.Context,
	su *framework.SchedulingUnit,
	cluster *fedcorev1a1.FederatedCluster,
) (int64, *framework.Result) {
	err := framework.PreCheck(ctx, su, cluster)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	taints, err := getFederatedClusterTaints(cluster)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	tolerationsPreferNoSchedule, err := getTolerationsPreferNoSchedule(su)
	if err != nil {
		return 0, framework.NewResult(framework.Error, err.Error())
	}

	score := int64(countIntolerableTaintsPreferNoSchedule(taints, tolerationsPreferNoSchedule))
	return score, framework.NewResult(framework.Success)
}

// NormalizeScore invoked after scoring all nodes.
func (pl *TaintToleration) NormalizeScore(ctx context.Context, scores framework.ClusterScoreList) *framework.Result {
	return framework.DefaultNormalizeScore(framework.MaxClusterScore, true, scores)
}

func (pl *TaintToleration) ScoreExtensions() framework.ScoreExtensions {
	return pl
}

func getFederatedClusterTaints(cluster *fedcorev1a1.FederatedCluster) ([]corev1.Taint, error) {
	return cluster.Spec.Taints, nil
}

func getSchedulingUnitTolerations(su *framework.SchedulingUnit) ([]corev1.Toleration, error) {
	return su.Tolerations, nil
}

// getTolerationsPreferNoSchedule gets the list of all Tolerations with Effect PreferNoSchedule or with no effect.
func getTolerationsPreferNoSchedule(su *framework.SchedulingUnit) ([]corev1.Toleration, error) {
	tolerationList := make([]corev1.Toleration, 0)
	for _, toleration := range su.Tolerations {
		// Empty effect means all effects which includes PreferNoSchedule, so we need to collect it as well.
		if len(toleration.Effect) == 0 || toleration.Effect == corev1.TaintEffectPreferNoSchedule {
			tolerationList = append(tolerationList, toleration)
		}
	}
	return tolerationList, nil
}

// CountIntolerableTaintsPreferNoSchedule gives the count of intolerable taints of a pod with effect PreferNoSchedule
func countIntolerableTaintsPreferNoSchedule(
	taints []corev1.Taint,
	tolerations []corev1.Toleration,
) (intolerableTaints int) {
	for i := range taints {
		// check only on taints that have effect PreferNoSchedule
		if taints[i].Effect != corev1.TaintEffectPreferNoSchedule {
			continue
		}
		if !framework.TolerationsTolerateTaint(tolerations, &taints[i]) {
			intolerableTaints++
		}
	}
	return
}
