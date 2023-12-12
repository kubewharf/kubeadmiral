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
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	unstructuredutil "github.com/kubewharf/kubeadmiral/pkg/util/unstructured"
)

func schedulingUnitForFedObject(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	policy fedcorev1a1.GenericPropagationPolicy,
) (*framework.SchedulingUnit, error) {
	template, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		return nil, fmt.Errorf("error retrieving template from FederatedObject: %w", err)
	}

	schedulingMode := getSchedulingModeFromPolicy(policy)
	schedulingModeOverride, exists := getSchedulingModeFromObject(fedObject)
	if exists {
		schedulingMode = schedulingModeOverride
	}
	var desiredReplicasOption *int64
	if schedulingMode == fedcorev1a1.SchedulingModeDivide && typeConfig.Spec.PathDefinition.ReplicasSpec == "" {
		// TODO remove this check in favor of a DivideIfPossible mode
		schedulingMode = fedcorev1a1.SchedulingModeDuplicate
	}
	if schedulingMode == fedcorev1a1.SchedulingModeDivide {
		value, err := unstructuredutil.GetInt64FromPath(template, typeConfig.Spec.PathDefinition.ReplicasSpec, nil)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return nil, fmt.Errorf("replicas path \"%s\" does not exist for FederatedObject", typeConfig.Spec.PathDefinition.ReplicasSpec)
		}

		desiredReplicasOption = value
	}

	currentReplicas, err := getCurrentReplicasFromObject(typeConfig, fedObject)
	if err != nil {
		return nil, err
	}

	sourceType := typeConfig.GetSourceType()
	schedulingUnit := &framework.SchedulingUnit{
		GroupVersion:    schema.GroupVersion{Group: sourceType.Group, Version: sourceType.Version},
		Kind:            sourceType.Kind,
		Resource:        sourceType.Name,
		Namespace:       template.GetNamespace(),
		Name:            template.GetName(),
		Labels:          template.GetLabels(),
		Annotations:     template.GetAnnotations(),
		DesiredReplicas: desiredReplicasOption,
		CurrentClusters: currentReplicas,
		AvoidDisruption: true,
	}

	schedulingUnit.ReplicasStrategy = getReplicasStrategyFromPolicy(policy)
	if autoMigration := policy.GetSpec().AutoMigration; autoMigration != nil {
		info, err := getAutoMigrationInfo(fedObject)
		if err != nil {
			return nil, err
		}
		schedulingUnit.AutoMigration = &framework.AutoMigrationSpec{
			Info:                      info,
			KeepUnschedulableReplicas: autoMigration.KeepUnschedulableReplicas,
		}
	}

	info, err := getCustomMigrationInfo(fedObject)
	if err != nil {
		return nil, err
	}
	schedulingUnit.CustomMigration = framework.CustomMigrationSpec{
		Info: info,
	}

	schedulingUnit.AvoidDisruption = getAvoidDisruptionFromPolicy(policy)

	schedulingUnit.SchedulingMode = schedulingMode

	schedulingUnit.StickyCluster = getIsStickyClusterFromPolicy(policy)
	stickyClusterOverride, exists := getIsStickyClusterFromObject(fedObject)
	if exists {
		schedulingUnit.StickyCluster = stickyClusterOverride
	}

	schedulingUnit.ClusterSelector = getClusterSelectorFromPolicy(policy)
	clusterSelectorOverride, exists := getClusterSelectorFromObject(fedObject)
	if exists {
		schedulingUnit.ClusterSelector = clusterSelectorOverride
	}

	schedulingUnit.ClusterNames = getClusterNamesFromPolicy(policy)
	clusterNameOverride, exists := getClusterNamesFromObject(fedObject)
	if exists {
		schedulingUnit.ClusterNames = clusterNameOverride
	}

	schedulingUnit.MinReplicas = getMinReplicasFromPolicy(policy)
	minReplicasOverride, exists := getMinReplicasFromObject(fedObject)
	if exists {
		schedulingUnit.MinReplicas = minReplicasOverride
	}

	schedulingUnit.MaxReplicas = getMaxReplicasFromPolicy(policy)
	maxReplicasOverride, exists := getMaxReplicasFromObject(fedObject)
	if exists {
		schedulingUnit.MaxReplicas = maxReplicasOverride
	}

	schedulingUnit.Weights = getWeightsFromPolicy(policy)
	weightsOverride, exists := getWeightsFromObject(fedObject)
	if exists {
		schedulingUnit.Weights = weightsOverride
	}

	schedulingUnit.Affinity = getAffinityFromPolicy(policy)
	affinityOverride, exists := getAffinityFromObject(fedObject)
	if exists {
		schedulingUnit.Affinity = affinityOverride
	}

	schedulingUnit.Tolerations = getTolerationsFromPolicy(policy)
	tolerationsOverride, exists := getTolerationsFromObject(fedObject)
	if exists {
		schedulingUnit.Tolerations = tolerationsOverride
	}

	schedulingUnit.MaxClusters = getMaxClustersFromPolicy(policy)
	maxClustersOverride, exists := getMaxClustersFromObject(fedObject)
	if exists {
		schedulingUnit.MaxClusters = maxClustersOverride
	}

	resourceRequest, err := getResourceRequest(typeConfig, fedObject)
	if err != nil {
		return nil, err
	}
	gpuResourceRequest := &framework.Resource{}
	if resourceRequest.HasScalarResource(framework.ResourceGPU) {
		gpuResourceRequest.SetScalar(framework.ResourceGPU, resourceRequest.ScalarResources[framework.ResourceGPU])
	}
	// now we only consider the resource request of gpu
	schedulingUnit.ResourceRequest = *gpuResourceRequest

	return schedulingUnit, nil
}

func getCurrentReplicasFromObject(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
) (map[string]*int64, error) {
	placements := fedObject.GetSpec().GetControllerPlacement(PrefixedGlobalSchedulerName)
	overrides := fedObject.GetSpec().GetControllerOverrides(PrefixedGlobalSchedulerName)

	res := make(map[string]*int64, len(placements))

	for _, placement := range placements {
		res[placement.Cluster] = nil
	}

	replicasPath := unstructuredutil.ToSlashPath(ftc.Spec.PathDefinition.ReplicasSpec)

	for _, override := range overrides {
		if _, ok := res[override.Cluster]; !ok {
			continue
		}

		for _, patch := range override.Patches {
			if patch.Path == replicasPath && (patch.Op == overridePatchOpReplace || patch.Op == "") {
				replicas := new(int64)
				if err := json.Unmarshal(patch.Value.Raw, replicas); err != nil {
					continue
				}
				res[override.Cluster] = replicas
				break
			}
		}
	}

	return res, nil
}

func getSchedulingModeFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) fedcorev1a1.SchedulingMode {
	if policy.GetSpec().SchedulingMode == fedcorev1a1.SchedulingModeDuplicate {
		return fedcorev1a1.SchedulingModeDuplicate
	}
	if policy.GetSpec().SchedulingMode == fedcorev1a1.SchedulingModeDivide {
		return fedcorev1a1.SchedulingModeDivide
	}
	return DefaultSchedulingMode
}

func getReplicasStrategyFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) fedcorev1a1.ReplicasStrategy {
	if policy.GetSpec().ReplicasStrategy == nil {
		return fedcorev1a1.ReplicasStrategySpread
	}

	if *policy.GetSpec().ReplicasStrategy == fedcorev1a1.ReplicasStrategyBinpack {
		return fedcorev1a1.ReplicasStrategyBinpack
	}

	return fedcorev1a1.ReplicasStrategySpread
}

func getSchedulingModeFromObject(fedObject fedcorev1a1.GenericFederatedObject) (fedcorev1a1.SchedulingMode, bool) {
	annotations := fedObject.GetAnnotations()
	if annotations == nil {
		return "", false
	}

	annotation, exists := annotations[SchedulingModeAnnotation]
	if !exists {
		return "", false
	}

	switch annotation {
	case string(fedcorev1a1.SchedulingModeDuplicate):
		return fedcorev1a1.SchedulingModeDuplicate, true
	case string(fedcorev1a1.SchedulingModeDivide):
		return fedcorev1a1.SchedulingModeDivide, true
	}

	klog.Errorf(
		"Invalid value %s for scheduling mode annotation (%s) on fed object %s",
		annotation,
		SchedulingModeAnnotation,
		fedObject.GetName(),
	)
	return "", false
}

func getAutoMigrationInfo(fedObject fedcorev1a1.GenericFederatedObject) (*framework.AutoMigrationInfo, error) {
	value, exists := fedObject.GetAnnotations()[common.AutoMigrationInfoAnnotation]
	if !exists {
		return nil, nil
	}

	autoMigrationInfo := new(framework.AutoMigrationInfo)
	if err := json.Unmarshal([]byte(value), autoMigrationInfo); err != nil {
		return nil, err
	}
	return autoMigrationInfo, nil
}

func getCustomMigrationInfo(fedObject fedcorev1a1.GenericFederatedObject) (*framework.CustomMigrationInfo, error) {
	value, exists := fedObject.GetAnnotations()[common.AppliedMigrationConfigurationAnnotation]
	if !exists {
		return nil, nil
	}

	customMigrationInfo := new(framework.CustomMigrationInfo)
	if err := json.Unmarshal([]byte(value), customMigrationInfo); err != nil {
		return nil, err
	}
	return customMigrationInfo, nil
}

func getIsStickyClusterFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) bool {
	rp := policy.GetSpec().ReschedulePolicy
	return rp != nil && rp.DisableRescheduling
}

func getAvoidDisruptionFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) bool {
	rp := policy.GetSpec().ReschedulePolicy
	return rp != nil && rp.ReplicaRescheduling != nil && rp.ReplicaRescheduling.AvoidDisruption
}

func getIsStickyClusterFromObject(object fedcorev1a1.GenericFederatedObject) (bool, bool) {
	// TODO: consider passing in the annotations directly to prevent incurring a deep copy for each call
	annotations := object.GetAnnotations()
	if annotations == nil {
		return false, false
	}

	annotation, exists := annotations[StickyClusterAnnotation]
	if !exists {
		return false, false
	}

	if annotation == StickyClusterAnnotationTrue {
		return true, true
	}
	if annotation == StickyClusterAnnotationFalse {
		return false, true
	}

	klog.Errorf(
		"Invalid value %s for sticky cluster annotation (%s) on fed object %s",
		annotation,
		StickyClusterAnnotation,
		object.GetName(),
	)
	return false, false
}

func getClusterSelectorFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) map[string]string {
	return policy.GetSpec().ClusterSelector
}

func getClusterSelectorFromObject(object fedcorev1a1.GenericFederatedObject) (map[string]string, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[ClusterSelectorAnnotations]
	if !exists {
		return nil, false
	}

	clusterSelector := make(map[string]string)
	err := json.Unmarshal([]byte(annotation), &clusterSelector)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal cluster selector annotation (%s) on fed object %s with err %s",
			ClusterSelectorAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	return clusterSelector, true
}

func getAffinityFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) *framework.Affinity {
	spec := policy.GetSpec()
	if spec.ClusterAffinity == nil || len(spec.ClusterAffinity) == 0 {
		return nil
	}

	affinity := &framework.Affinity{
		ClusterAffinity: &framework.ClusterAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
				ClusterSelectorTerms: spec.ClusterAffinity,
			},
		},
	}

	return affinity
}

func getAffinityFromObject(object fedcorev1a1.GenericFederatedObject) (*framework.Affinity, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[AffinityAnnotations]
	if !exists {
		return nil, false
	}

	affinity := &framework.Affinity{}
	err := json.Unmarshal([]byte(annotation), affinity)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal affinity annotation (%s) on fed object %s with err %s",
			AffinityAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	return affinity, true
}

func getTolerationsFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) []corev1.Toleration {
	return policy.GetSpec().Tolerations
}

func getTolerationsFromObject(object fedcorev1a1.GenericFederatedObject) ([]corev1.Toleration, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[TolerationsAnnotations]
	if !exists {
		return nil, false
	}

	tolerations := []corev1.Toleration{}
	err := json.Unmarshal([]byte(annotation), &tolerations)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal tolerations annotation (%s) on fed object %s with err %s",
			TolerationsAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	return tolerations, true
}

func getMaxClustersFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) *int64 {
	return policy.GetSpec().MaxClusters
}

func getMaxClustersFromObject(object fedcorev1a1.GenericFederatedObject) (*int64, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[MaxClustersAnnotations]
	if !exists {
		return nil, false
	}

	maxClusters, err := strconv.Atoi(annotation)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal max clusters annotation (%s) on fed object %s with err %s",
			MaxClustersAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	// we need to do additional validation vs getting from policy which relies on CRD validation by apiserver
	if maxClusters < 0 {
		klog.Errorf(
			"Invalid value for max clusters annotation (%s) on fed object %s: %d",
			MaxClustersAnnotations,
			object.GetName(),
			maxClusters,
		)
		return nil, false
	}

	result := int64(maxClusters)
	return &result, true
}

func getWeightsFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) map[string]int64 {
	if policy.GetSpec().Placements == nil {
		return nil
	}

	weights := map[string]int64{}
	for _, placement := range policy.GetSpec().Placements {
		if placement.Preferences.Weight != nil {
			weights[placement.Cluster] = *placement.Preferences.Weight
		}
	}

	return weights
}

func getWeightsFromObject(object fedcorev1a1.GenericFederatedObject) (map[string]int64, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[PlacementsAnnotations]
	if !exists {
		return nil, false
	}

	var placements []fedcorev1a1.DesiredPlacement
	err := json.Unmarshal([]byte(annotation), &placements)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal placements annotation (%s) on fed object %s with err %s",
			TolerationsAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	weights := map[string]int64{}
	for _, placement := range placements {
		if placement.Preferences.Weight != nil {
			weights[placement.Cluster] = *placement.Preferences.Weight
		}
	}

	// we need to do additional validation vs getting from policy which relies on CRD validation by apiserver
	for _, weight := range weights {
		if weight < 0 {
			klog.Errorf(
				"Invalid value for placements annotation (%s) on fed object %s: negative weight found",
				PlacementsAnnotations,
				object.GetName(),
			)
			return nil, false
		}
	}

	return weights, true
}

func getMinReplicasFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) map[string]int64 {
	if policy.GetSpec().Placements == nil {
		return nil
	}

	minReplicas := make(map[string]int64, len(policy.GetSpec().Placements))
	for _, placement := range policy.GetSpec().Placements {
		minReplicas[placement.Cluster] = placement.Preferences.MinReplicas
	}

	return minReplicas
}

func getMinReplicasFromObject(object fedcorev1a1.GenericFederatedObject) (map[string]int64, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[PlacementsAnnotations]
	if !exists {
		return nil, false
	}

	var placements []fedcorev1a1.DesiredPlacement
	err := json.Unmarshal([]byte(annotation), &placements)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal placements annotation (%s) on fed object %s with err %s",
			TolerationsAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	minReplicas := make(map[string]int64, len(placements))
	for _, placement := range placements {
		minReplicas[placement.Cluster] = placement.Preferences.MinReplicas
	}

	// we need to do additional validation vs getting from policy which relies on CRD validation by apiserver
	for _, replicas := range minReplicas {
		if replicas < 0 {
			klog.Errorf(
				"Invalid value for placements annotation (%s) on fed object %s: negative minReplicas found",
				PlacementsAnnotations,
				object.GetName(),
			)
			return nil, false
		}
	}

	return minReplicas, true
}

func getMaxReplicasFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) map[string]int64 {
	if policy.GetSpec().Placements == nil {
		return nil
	}

	maxReplicas := make(map[string]int64, len(policy.GetSpec().Placements))
	for _, placement := range policy.GetSpec().Placements {
		if placement.Preferences.MaxReplicas != nil {
			maxReplicas[placement.Cluster] = *placement.Preferences.MaxReplicas
		}
	}

	return maxReplicas
}

func getMaxReplicasFromObject(object fedcorev1a1.GenericFederatedObject) (map[string]int64, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[PlacementsAnnotations]
	if !exists {
		return nil, false
	}

	var placements []fedcorev1a1.DesiredPlacement
	err := json.Unmarshal([]byte(annotation), &placements)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal placements annotation (%s) on fed object %s with err %s",
			TolerationsAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	maxReplicas := make(map[string]int64, len(placements))
	for _, placement := range placements {
		if placement.Preferences.MaxReplicas != nil {
			maxReplicas[placement.Cluster] = *placement.Preferences.MaxReplicas
		}
	}

	// we need to do additional validation vs getting from policy which relies on CRD validation by apiserver
	for _, replicas := range maxReplicas {
		if replicas < 0 {
			klog.Errorf(
				"Invalid value for placements annotation (%s) on fed object %s: negative maxReplicas found",
				PlacementsAnnotations,
				object.GetName(),
			)
			return nil, false
		}
	}

	return maxReplicas, true
}

func getClusterNamesFromPolicy(policy fedcorev1a1.GenericPropagationPolicy) map[string]struct{} {
	if policy.GetSpec().Placements == nil {
		return nil
	}

	clusterNames := make(map[string]struct{}, len(policy.GetSpec().Placements))
	for _, placement := range policy.GetSpec().Placements {
		clusterNames[placement.Cluster] = struct{}{}
	}

	return clusterNames
}

func getClusterNamesFromObject(object fedcorev1a1.GenericFederatedObject) (map[string]struct{}, bool) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		return nil, false
	}

	annotation, exists := annotations[PlacementsAnnotations]
	if !exists {
		return nil, false
	}

	var placements []fedcorev1a1.ClusterReference
	err := json.Unmarshal([]byte(annotation), &placements)
	if err != nil {
		klog.Errorf(
			"Failed to unmarshal placements annotation (%s) on fed object %s with err %s",
			TolerationsAnnotations,
			object.GetName(),
			err,
		)
		return nil, false
	}

	clusterNames := make(map[string]struct{}, len(placements))
	for _, placement := range placements {
		clusterNames[placement.Cluster] = struct{}{}
	}

	return clusterNames, true
}
