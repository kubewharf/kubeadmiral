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

package policies

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
)

func PropagationPolicyForClustersWithPlacements(
	baseName string,
	clusters []*fedcorev1a1.FederatedCluster,
) *fedcorev1a1.PropagationPolicy {
	name := fmt.Sprintf("%s-%s", baseName, rand.String(12))
	policy := &fedcorev1a1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: fedcorev1a1.PropagationPolicySpec{
			SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
			Placements:     []fedcorev1a1.DesiredPlacement{},
		},
	}

	for _, c := range clusters {
		policy.Spec.Placements = append(policy.Spec.Placements, fedcorev1a1.DesiredPlacement{Cluster: c.Name})
	}

	return policy
}

func SetPropagationPolicy(obj metav1.Object, policy *fedcorev1a1.PropagationPolicy) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[scheduler.PropagationPolicyNameLabel] = policy.Name
	obj.SetLabels(labels)
}

func SetTolerationsForTaints(policy *fedcorev1a1.PropagationPolicy, taints []corev1.Taint) {
	for _, taint := range taints {
		toleration := corev1.Toleration{
			Key:      taint.Key,
			Operator: corev1.TolerationOpEqual,
			Value:    taint.Value,
			Effect:   taint.Effect,
		}
		policy.Spec.Tolerations = append(policy.Spec.Tolerations, toleration)
	}
}
