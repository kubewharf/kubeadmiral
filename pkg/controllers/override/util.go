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

package override

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/clusterselector"
)

/*
lookForMatchedPolicies looks for OverridePolicy and/or ClusterOverridePolicy
that match the obj in the stores.

  - A federated object with a namespace-scoped target can reference
    both an OverridePolicy from its containing namespace and a ClusterOverridePolicy.
    If both are found, ClusterOverridePolicy is applied before OverridePolicy.
  - A federated object with a cluster-scoped target can only reference a ClusterOverridePolicy.

Returns the policy if found, whether a recheck is needed on error, and encountered error if any.
*/
func lookForMatchedPolicies(
	obj *unstructured.Unstructured,
	isNamespaced bool,
	overridePolicyStore cache.Store,
	clusterOverridePolicyStore cache.Store,
) ([]fedcorev1a1.GenericOverridePolicy, bool, error) {
	policies := make([]fedcorev1a1.GenericOverridePolicy, 0)

	labels := obj.GetLabels()

	clusterPolicyName, clusterPolicyNameExists := labels[ClusterOverridePolicyNameLabel]
	if clusterPolicyNameExists {
		if len(clusterPolicyName) == 0 {
			return nil, false, fmt.Errorf("policy name cannot be empty")
		}

		matchedPolicyObj, exists, err := clusterOverridePolicyStore.GetByKey(clusterPolicyName)
		if err != nil {
			return nil, true, err
		}
		if !exists {
			return nil, false, fmt.Errorf("ClusterOverridePolicy %s not found", clusterPolicyName)
		}
		matchedPolicy, ok := matchedPolicyObj.(*fedcorev1a1.ClusterOverridePolicy)
		if !ok {
			return nil, false, fmt.Errorf("object retrieved from store is not a ClusterOverridePolicy")
		}
		policies = append(policies, matchedPolicy)
	}

	policyName, policyNameExists := labels[OverridePolicyNameLabel]
	if isNamespaced && policyNameExists {
		if len(policyName) == 0 {
			return nil, false, fmt.Errorf("policy name cannot be empty")
		}

		key := obj.GetNamespace() + "/" + policyName
		matchedPolicyObj, exists, err := overridePolicyStore.GetByKey(key)
		if err != nil {
			return nil, true, err
		}
		if !exists {
			return nil, false, fmt.Errorf("OverridePolicy %s not found", key)
		}
		matchedPolicy, ok := matchedPolicyObj.(*fedcorev1a1.OverridePolicy)
		if !ok {
			return nil, false, fmt.Errorf("object retrieved from store is not an OverridePolicy")
		}
		policies = append(policies, matchedPolicy)
	}

	return policies, false, nil
}

func parseOverrides(
	policy fedcorev1a1.GenericOverridePolicy,
	clusters []*fedcorev1a1.FederatedCluster,
) (util.OverridesMap, error) {
	overridesMap := make(util.OverridesMap)

	for _, cluster := range clusters {
		patches := make(fedtypesv1a1.OverridePatches, 0)

		spec := policy.GetSpec()
		for i, rule := range spec.OverrideRules {
			matched, err := isClusterMatched(rule.TargetClusters, cluster)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to match cluster %q to policy %q's overrideRules[%v]: %w",
					cluster.Name,
					policy.GetName(),
					i,
					err,
				)
			}

			if !matched {
				continue
			}

			for _, overrider := range rule.Overriders.JsonPatch {
				patch, err := policyJsonPatchOverriderToOverridePatch(&overrider)
				if err != nil {
					return nil, err
				}
				patches = append(patches, *patch)
			}
		}

		if len(patches) > 0 {
			overridesMap[cluster.Name] = patches
		}
	}

	return overridesMap, nil
}

func mergeOverrides(dest, src util.OverridesMap) util.OverridesMap {
	if dest == nil {
		dest = make(util.OverridesMap)
	}

	for clusterName, srcOverrides := range src {
		dest[clusterName] = append(dest[clusterName], srcOverrides...)
	}

	return dest
}

func isClusterMatched(targetClusters *fedcorev1a1.TargetClusters, cluster *fedcorev1a1.FederatedCluster) (bool, error) {
	// An empty targetClusters matches all clusters.
	if targetClusters == nil {
		return true, nil
	}

	// If targetClusters is specified, we consider clusterNames, clusterSelector, and clusterAffinity.
	// The 3 criteria are ANDed, i.e., for a cluster to be matched, it must be matched by all 3 criteria.

	if !isClusterMatchedByClusterNames(targetClusters.Clusters, cluster) {
		return false, nil
	}

	matched, err := isClusterMatchedByClusterSelector(targetClusters.ClusterSelector, cluster)
	if err != nil || !matched {
		return matched, err
	}

	matched, err = isClusterMatchedByClusterAffinity(targetClusters.ClusterAffinity, cluster)
	if err != nil || !matched {
		return matched, err
	}

	return true, nil
}

func isClusterMatchedByClusterNames(clusterNames []string, cluster *fedcorev1a1.FederatedCluster) bool {
	// empty clusterNames matches any cluster
	if len(clusterNames) == 0 {
		return true
	}

	// if clusterNames is non-empty, then cluster.Name must appear in clusterNames for it to be matched
	for _, clusterName := range clusterNames {
		if clusterName == cluster.Name {
			return true
		}
	}

	return false
}

func isClusterMatchedByClusterSelector(
	clusterSelector map[string]string,
	cluster *fedcorev1a1.FederatedCluster,
) (bool, error) {
	// prefer metav1.LabelSelectorAsSelector over labels.SelectorFromSet as the latter gobbles up any validation errors
	// ref: https://github.com/kubernetes/kubernetes/pull/89747
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: clusterSelector,
	})
	if err != nil {
		return false, fmt.Errorf("failed to interpret clusterSelector: %w", err)
	}
	return selector.Matches(labels.Set(cluster.Labels)), nil
}

func isClusterMatchedByClusterAffinity(
	clusterAffinity []fedcorev1a1.ClusterSelectorTerm,
	cluster *fedcorev1a1.FederatedCluster,
) (bool, error) {
	// empty clusterAffinity matches any cluster
	if len(clusterAffinity) == 0 {
		return true, nil
	}

	return clusterselector.MatchClusterSelectorTerms(clusterAffinity, cluster)
}

func policyJsonPatchOverriderToOverridePatch(
	overrider *fedcorev1a1.JsonPatchOverrider,
) (*fedtypesv1a1.OverridePatch, error) {
	overridePatch := &fedtypesv1a1.OverridePatch{
		Op:   overrider.Operator,
		Path: overrider.Path,
	}

	// JsonPatch overrider's value is of apiextensionsv1.JSON type, which must be unmarshalled first.
	if len(overrider.Value.Raw) > 0 {
		err := json.Unmarshal(overrider.Value.Raw, &overridePatch.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal jsonpatch overrider value %q: %w", &overrider.Value.Raw, err)
		}
	}

	return overridePatch, nil
}
