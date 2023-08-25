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

package scheduler

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/util/unstructured"
)

const (
	overridePatchOpReplace = "replace"
)

func GetMatchedPolicyKey(obj metav1.Object) (result common.QualifiedName, ok bool) {
	labels := obj.GetLabels()
	isNamespaced := len(obj.GetNamespace()) > 0

	if policyName, exists := labels[PropagationPolicyNameLabel]; exists && isNamespaced {
		return common.QualifiedName{Namespace: obj.GetNamespace(), Name: policyName}, true
	}

	if policyName, exists := labels[ClusterPropagationPolicyNameLabel]; exists {
		return common.QualifiedName{Namespace: "", Name: policyName}, true
	}

	return common.QualifiedName{}, false
}

func UpdateReplicasOverride(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	result map[string]int64,
) (updated bool, err error) {
	replicasPath := unstructured.ToSlashPath(ftc.Spec.PathDefinition.ReplicasSpec)

	newOverrides := []fedcorev1a1.ClusterReferenceWithPatches{}
	for cluster, replicas := range result {
		replicasRaw, err := json.Marshal(replicas)
		if err != nil {
			return false, fmt.Errorf("failed to marshal replicas value: %w", err)
		}
		override := fedcorev1a1.ClusterReferenceWithPatches{
			Cluster: cluster,
			Patches: fedcorev1a1.OverridePatches{
				{
					Op:   overridePatchOpReplace,
					Path: replicasPath,
					Value: apiextensionsv1.JSON{
						Raw: replicasRaw,
					},
				},
			},
		}
		newOverrides = append(newOverrides, override)
	}

	updated = fedObject.GetSpec().SetControllerOverrides(PrefixedGlobalSchedulerName, newOverrides)
	return updated, nil
}
