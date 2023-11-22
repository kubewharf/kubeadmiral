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
	"errors"
	"fmt"
	"sort"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	podutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
	resourceutil "github.com/kubewharf/kubeadmiral/pkg/util/resource"
	unstructuredutil "github.com/kubewharf/kubeadmiral/pkg/util/unstructured"
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
	replicasPath := unstructuredutil.ToSlashPath(ftc.Spec.PathDefinition.ReplicasSpec)

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

func getResourceRequest(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
) (framework.Resource, error) {
	gvk := ftc.GetSourceTypeGVK()
	podSpec, err := podutil.GetResourcePodSpec(fedObject, gvk)
	if err != nil {
		if errors.Is(err, podutil.ErrUnknownTypeToGetPodSpec) {
			return framework.Resource{}, nil
		}
		return framework.Resource{}, err
	}
	resource := resourceutil.GetPodResourceRequests(podSpec)
	return *framework.NewResource(resource), nil
}

func getCustomMigrationInfoBytes(
	existingMigrationConfigBytes string,
	fedObj fedcorev1a1.GenericFederatedObject,
	ftc *fedcorev1a1.FederatedTypeConfig,
) (string, error) {
	existingMigrationConfig := &framework.MigrationConfig{}
	err := json.Unmarshal([]byte(existingMigrationConfigBytes), existingMigrationConfig)
	if err != nil {
		return "", err
	}

	err = validateMigrationConfig(existingMigrationConfig, fedObj, ftc)
	if err != nil {
		return "", err
	}

	customMigrationInfo := convertToCustomMigrationInfo(existingMigrationConfig)
	customMigrationInfoBytes, err := json.Marshal(customMigrationInfo)
	if err != nil {
		return "", err
	}

	return string(customMigrationInfoBytes), nil
}

func validateMigrationConfig(
	migrationConfig *framework.MigrationConfig,
	fedObj fedcorev1a1.GenericFederatedObject,
	ftc *fedcorev1a1.FederatedTypeConfig,
) error {
	clusters := sets.New[string]()

	if len(migrationConfig.ReplicasMigrations) > 0 {
		sourceObj, err := fedObj.GetSpec().GetTemplateAsUnstructured()
		if err != nil {
			return err
		}
		value, err := unstructuredutil.GetInt64FromPath(sourceObj, ftc.Spec.PathDefinition.ReplicasSpec, nil)
		if err != nil || value == nil {
			return fmt.Errorf("workload does not support the replica migration configurations")
		}
	}

	for _, replicasMigration := range migrationConfig.ReplicasMigrations {
		if replicasMigration.Cluster == "" {
			return fmt.Errorf("cluster cannot be empty")
		}
		if replicasMigration.LimitedCapacity == nil {
			return fmt.Errorf("limitedCapacity cannot be empty")
		}
		if *replicasMigration.LimitedCapacity < 0 {
			return fmt.Errorf("limitedCapacity cannot be less than zero")
		}
		if clusters.Has(replicasMigration.Cluster) {
			return fmt.Errorf("multiple migration configurations cannot be configured for the same cluster")
		}
		clusters.Insert(replicasMigration.Cluster)
	}

	for _, workloadMigration := range migrationConfig.WorkloadMigrations {
		if workloadMigration.Cluster == "" {
			return fmt.Errorf("cluster cannot be empty")
		}
		if workloadMigration.ValidUntil == nil {
			return fmt.Errorf("validUntil cannot be empty")
		}
		if clusters.Has(workloadMigration.Cluster) {
			return fmt.Errorf("multiple migration configurations cannot be configured for the same cluster")
		}
		clusters.Insert(workloadMigration.Cluster)
	}

	return nil
}

func convertToCustomMigrationInfo(migrationConfig *framework.MigrationConfig) *framework.CustomMigrationInfo {
	ret := &framework.CustomMigrationInfo{}

	for _, replicasMigration := range migrationConfig.ReplicasMigrations {
		if ret.LimitedCapacity == nil {
			ret.LimitedCapacity = make(map[string]int64)
		}
		ret.LimitedCapacity[replicasMigration.Cluster] = *replicasMigration.LimitedCapacity
	}

	for _, workloadMigration := range migrationConfig.WorkloadMigrations {
		ret.UnavailableClusters = append(ret.UnavailableClusters,
			framework.UnavailableCluster{Cluster: workloadMigration.Cluster, ValidUntil: *workloadMigration.ValidUntil})
	}
	sort.Slice(ret.UnavailableClusters, func(i, j int) bool {
		return ret.UnavailableClusters[i].Cluster < ret.UnavailableClusters[j].Cluster
	})
	return ret
}
