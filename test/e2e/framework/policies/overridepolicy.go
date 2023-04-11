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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
)

func OverridePolicyForClustersWithPatches(
	baseName string,
	clusterOverriders map[string]fedcorev1a1.Overriders,
) *fedcorev1a1.OverridePolicy {
	name := fmt.Sprintf("%s-%s", baseName, rand.String(12))
	policy := &fedcorev1a1.OverridePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: fedcorev1a1.GenericOverridePolicySpec{
			OverrideRules: []fedcorev1a1.OverrideRule{},
		},
	}

	for clusterName, overriders := range clusterOverriders {
		policy.Spec.OverrideRules = append(policy.Spec.OverrideRules, fedcorev1a1.OverrideRule{
			TargetClusters: &fedcorev1a1.TargetClusters{
				ClusterNames: []string{clusterName},
			},
			Overriders: &overriders,
		})
	}

	return policy
}

func SetOverridePolicy(obj metav1.Object, policyName string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[override.OverridePolicyNameLabel] = policyName
	obj.SetLabels(labels)
}
