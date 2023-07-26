//go:build exclude
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
	"sync"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/controllers/util/unstructured"
)

const (
	operationReplace = "replace"
)

func MatchedPolicyKey(obj fedcorev1a1.GenericFederatedObject, isNamespaced bool) (result common.QualifiedName, ok bool) {
	labels := obj.GetLabels()

	if policyName, exists := labels[PropagationPolicyNameLabel]; exists && isNamespaced {
		return common.QualifiedName{Namespace: obj.GetNamespace(), Name: policyName}, true
	}

	if policyName, exists := labels[ClusterPropagationPolicyNameLabel]; exists {
		return common.QualifiedName{Namespace: "", Name: policyName}, true
	}

	return common.QualifiedName{}, false
}

type ClusterClients struct {
	clients sync.Map
}

func (c *ClusterClients) Get(cluster string) dynamic.Interface {
	val, ok := c.clients.Load(cluster)
	if ok {
		return val.(dynamic.Interface)
	}
	return nil
}

func (c *ClusterClients) Set(cluster string, client dynamic.Interface) {
	c.clients.Store(cluster, client)
}

func (c *ClusterClients) Delete(cluster string) {
	c.clients.Delete(cluster)
}

func UpdateReplicasOverride(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedObject *unstructured.Unstructured,
	result map[string]int64,
) (updated bool, err error) {
	overridesMap, err := util.GetOverrides(fedObject, PrefixedGlobalSchedulerName)
	if err != nil {
		return updated, errors.Wrapf(
			err,
			"Error reading cluster overrides for %s/%s",
			fedObject.GetNamespace(),
			fedObject.GetName(),
		)
	}

	if OverrideUpdateNeeded(typeConfig, overridesMap, result) {
		err := setOverrides(typeConfig, fedObject, overridesMap, result)
		if err != nil {
			return updated, err
		}
		updated = true
	}
	return updated, nil
}

func setOverrides(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	obj *unstructured.Unstructured,
	overridesMap util.OverridesMap,
	replicasMap map[string]int64,
) error {
	if overridesMap == nil {
		overridesMap = make(util.OverridesMap)
	}
	updateOverridesMap(typeConfig, overridesMap, replicasMap)
	return util.SetOverrides(obj, PrefixedGlobalSchedulerName, overridesMap)
}

func updateOverridesMap(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	overridesMap util.OverridesMap,
	replicasMap map[string]int64,
) {
	replicasPath := utilunstructured.ToSlashPath(typeConfig.Spec.PathDefinition.ReplicasSpec)

	// Remove replicas override for clusters that are not scheduled
	for clusterName, clusterOverrides := range overridesMap {
		if _, ok := replicasMap[clusterName]; !ok {
			for i, overrideItem := range clusterOverrides {
				if overrideItem.Path == replicasPath {
					clusterOverrides = append(clusterOverrides[:i], clusterOverrides[i+1:]...)
					if len(clusterOverrides) == 0 {
						// delete empty ClusterOverrides item
						delete(overridesMap, clusterName)
					} else {
						overridesMap[clusterName] = clusterOverrides
					}
					break
				}
			}
		}
	}
	// Add/update replicas override for clusters that are scheduled
	for clusterName, replicas := range replicasMap {
		replicasOverrideFound := false
		for idx, overrideItem := range overridesMap[clusterName] {
			if overrideItem.Path == replicasPath {
				overridesMap[clusterName][idx].Value = replicas
				replicasOverrideFound = true
				break
			}
		}
		if !replicasOverrideFound {
			clusterOverrides, exist := overridesMap[clusterName]
			if !exist {
				clusterOverrides = fedtypesv1a1.OverridePatches{}
			}
			clusterOverrides = append(clusterOverrides, fedtypesv1a1.OverridePatch{Path: replicasPath, Value: replicas})
			overridesMap[clusterName] = clusterOverrides
		}
	}
}

func OverrideUpdateNeeded(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	overridesMap util.OverridesMap,
	result map[string]int64,
) bool {
	resultLen := len(result)
	checkLen := 0
	for clusterName, clusterOverridesMap := range overridesMap {
		for _, overrideItem := range clusterOverridesMap {
			path := overrideItem.Path
			rawValue := overrideItem.Value
			if path != utilunstructured.ToSlashPath(typeConfig.Spec.PathDefinition.ReplicasSpec) {
				continue
			}
			// The type of the value will be float64 due to how json
			// marshalling works for interfaces.
			floatValue, ok := rawValue.(float64)
			if !ok {
				return true
			}
			value := int64(floatValue)
			replicas, ok := result[clusterName]

			if !ok || value != replicas {
				return true
			}
			checkLen += 1
		}
	}

	return checkLen != resultLen
}
