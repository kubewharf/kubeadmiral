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
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/exp/constraints"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/util/annotation"
	podutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
	resourceutil "github.com/kubewharf/kubeadmiral/pkg/util/resource"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/util/unstructured"
)

/*
Scheduling of a federated object can be triggered by changes to:
1. the federated object itself
2. the federated object's assigned propagation policy
3. any cluster object

However, not all changes should trigger a scheduling, we limit our scheduling triggers to the following changes.

Federated object changes:
1. object creation
2. object scheduling annotation updates
3. object replica count change
4. object resource request change

Propagation policy changes:
1. policy creation
2. semantics of policy change (see PropagationPolicySpec.ReschedulePolicy.Trigger.PolicyContentChanged for more detail)

Cluster changes:
1. cluster creation
2. cluster labels change
3. cluster taints change
4. cluster apiresource changes

Simply checking for these triggers in the event handlers is insufficient. This is because when the controller restarts, all objects will be
"created" again, causing mass rescheduling for all objects. Thus, we hash the scheduling triggers and write it into the federated object's
annotations. Before reconciling a federated object, we check this hash to determine if any scheduling triggers have changed.
*/

type keyValue[K any, V any] struct {
	Key   K `json:"key"`
	Value V `json:"value"`
}

func sortMap[K constraints.Ordered, V any](m map[K]V) []keyValue[K, V] {
	ret := make([]keyValue[K, V], 0, len(m))
	for k, v := range m {
		ret = append(ret, keyValue[K, V]{Key: k, Value: v})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Key < ret[j].Key
	})
	return ret
}

type schedulingTriggers struct {
	// NOTE: Use slices instead of maps for deterministic iteration order

	SchedulingAnnotationsHash string             `json:"schedulingAnnotationsHash"`
	ReplicaCount              int64              `json:"replicaCount"`
	ResourceRequest           framework.Resource `json:"resourceRequest"`

	AutoMigrationInfo *string `json:"autoMigrationInfo,omitempty"`

	PolicyName                  string `json:"policyName"`
	PolicySchedulingContentHash string `json:"policyContentHash"`

	clusters sets.Set[string]
	// a map from each cluster to its labels
	ClusterLabelsHashes []keyValue[string, string] `json:"clusterLabelsHashes"`
	// a map from each cluster to its taints
	ClusterTaintsHashes []keyValue[string, string] `json:"clusterTaintsHashes"`
	// a map from each cluster to its apiresources
	ClusterAPIResourceTypesHashes []keyValue[string, string] `json:"clusterAPIResourceTypesHashes"`
}

func (t *schedulingTriggers) JsonMarshal() (string, error) {
	triggerBytes, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("failed to marshal scheduling trigger: %w", err)
	}
	return string(triggerBytes), nil
}

func (t *schedulingTriggers) JsonUnmarshal(v []byte) error {
	if t == nil {
		return fmt.Errorf("nil receiver")
	}
	trigger := &schedulingTriggers{}
	err := json.Unmarshal(v, trigger)
	if err != nil {
		return fmt.Errorf("failed to unmarshal scheduling trigger: %w", err)
	}
	clusters := sets.Set[string]{}
	for _, v := range trigger.ClusterLabelsHashes {
		clusters.Insert(v.Key)
	}
	for _, v := range trigger.ClusterTaintsHashes {
		clusters.Insert(v.Key)
	}
	for _, v := range trigger.ClusterAPIResourceTypesHashes {
		clusters.Insert(v.Key)
	}
	trigger.clusters = clusters

	*t = *trigger
	return nil
}

// If the member cluster is removed, we regard as a trigger of the label.
// But if a cluster joins, we think there is no trigger for the label.
func isClusterTriggerChanged(newClusters, oldClusters []keyValue[string, string]) bool {
	newLen, oldLen := len(newClusters), len(oldClusters)
	if newLen == 0 {
		return oldLen != 0
	}

	for i, j := 0, 0; i < newLen && j < oldLen; {
		if newClusters[i].Key != oldClusters[j].Key {
			i++
			if newLen-i < oldLen-j {
				return true
			}
			continue
		}
		if newClusters[i].Value != oldClusters[j].Value {
			return true
		}
		i++
		j++
	}
	return false
}

func (t *schedulingTriggers) updateAnnotationsIfTriggersChanged(
	fedObject fedcorev1a1.GenericFederatedObject,
	policy fedcorev1a1.GenericPropagationPolicy,
) (triggersChanged, annotationChanged bool, err error) {
	triggerText, err := t.JsonMarshal()
	if err != nil {
		return false, false, err
	}

	defer func() {
		if triggersChanged {
			annotationChanged, err = annotation.AddAnnotation(fedObject, SchedulingTriggersAnnotation, triggerText)
			if err != nil {
				return
			}
			_, err = annotation.RemoveAnnotation(fedObject, SchedulingDeferredReasonsAnnotation)
		}
	}()

	anno := fedObject.GetAnnotations()
	if anno == nil {
		return true, false, nil
	}

	if old, ok := anno[SchedulingTriggersAnnotation]; !ok || old == "" {
		return true, false, nil
	} else if old == triggerText {
		return false, false, nil
	} else {
		oldTrigger := &schedulingTriggers{}
		if err = oldTrigger.JsonUnmarshal([]byte(old)); err != nil {
			return false, false, err
		}
		if t.PolicyName != oldTrigger.PolicyName {
			return true, false, nil
		}

		reschedulePolicy := policy.GetSpec().ReschedulePolicy
		if getIsStickyClusterFromPolicy(policy) || reschedulePolicy.Trigger == nil {
			return false, false, nil
		}

		policyTrigger := reschedulePolicy.Trigger
		var deferredReasons []string
		if t.PolicySchedulingContentHash != oldTrigger.PolicySchedulingContentHash {
			if !policyTrigger.PolicyContentChanged {
				deferredReasons = append(deferredReasons, "policyContentChanged: false")
			} else {
				triggersChanged = true
			}
		}

		if isClusterTriggerChanged(t.ClusterLabelsHashes, oldTrigger.ClusterLabelsHashes) {
			if !policyTrigger.ClusterLabelsChanged {
				deferredReasons = append(deferredReasons, "clusterLabelsChanged: false")
			} else {
				triggersChanged = true
			}
		}

		if isClusterTriggerChanged(t.ClusterTaintsHashes, oldTrigger.ClusterTaintsHashes) {
			triggersChanged = true
		}

		if isClusterTriggerChanged(t.ClusterAPIResourceTypesHashes, oldTrigger.ClusterAPIResourceTypesHashes) {
			if !policyTrigger.ClusterAPIResourcesChanged {
				deferredReasons = append(deferredReasons, "clusterAPIResourcesChanged: false")
			} else {
				triggersChanged = true
			}
		}

		if t.clusters.IsSuperset(oldTrigger.clusters) && len(t.clusters) != len(oldTrigger.clusters) {
			if !policyTrigger.ClusterJoined {
				deferredReasons = append(deferredReasons, "clusterJoined: false")
			} else {
				triggersChanged = true
			}
		}

		if triggersChanged {
			return true, false, nil
		}
		annotationChanged, err = annotation.AddAnnotation(fedObject, SchedulingDeferredReasonsAnnotation, strings.Join(deferredReasons, ";"))
		return false, annotationChanged, err
	}
}

func computeSchedulingTrigger(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	policy fedcorev1a1.GenericPropagationPolicy,
	clusters []*fedcorev1a1.FederatedCluster,
) (*schedulingTriggers, error) {
	trigger := &schedulingTriggers{clusters: sets.New[string]()}

	var err error

	trigger.SchedulingAnnotationsHash, err = getSchedulingAnnotationsHash(fedObject)
	if err != nil {
		return nil, fmt.Errorf("failed to get scheduling annotations: %w", err)
	}
	if trigger.ReplicaCount, err = getReplicaCount(ftc, fedObject); err != nil {
		return nil, fmt.Errorf("failed to get object replica count: %w", err)
	}
	if trigger.ResourceRequest, err = getResourceRequest(ftc, fedObject); err != nil {
		return nil, fmt.Errorf("failed to get object resource request: %w", err)
	}

	if policy != nil {
		trigger.PolicyName = policy.GetName()
		if policy.GetSpec().AutoMigration != nil {
			// Only consider auto-migration annotation when auto-migration is enabled in the policy.
			if value, exists := fedObject.GetAnnotations()[common.AutoMigrationInfoAnnotation]; exists {
				trigger.AutoMigrationInfo = &value
			}
		}
		if trigger.PolicySchedulingContentHash, err = getPolicySchedulingContentHash(policy.GetSpec()); err != nil {
			return nil, fmt.Errorf("failed to get scheduling content of policy %s: %w", policy.GetName(), err)
		}
	}

	for _, cluster := range clusters {
		trigger.clusters.Insert(cluster.Name)
	}

	trigger.ClusterLabelsHashes, err = getClusterLabelsHashes(clusters)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster labels hashes: %w", err)
	}
	trigger.ClusterTaintsHashes, err = getClusterTaintsHashes(clusters)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster taints hashes: %w", err)
	}
	trigger.ClusterAPIResourceTypesHashes, err = getClusterAPIResourceTypesHashes(clusters)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster API resource types hashes: %w", err)
	}

	return trigger, nil
}

var knownSchedulingAnnotations = sets.New(
	SchedulingModeAnnotation,
	StickyClusterAnnotation,
	TolerationsAnnotations,
	PlacementsAnnotations,
	ClusterSelectorAnnotations,
	AffinityAnnotations,
	MaxClustersAnnotations,
	FollowsObjectAnnotation,
)

func getSchedulingAnnotationsHash(fedObject fedcorev1a1.GenericFederatedObject) (string, error) {
	result := map[string]string{}
	for k, v := range fedObject.GetAnnotations() {
		if knownSchedulingAnnotations.Has(k) {
			result[k] = v
		}
	}
	return hashResult(sortMap(result))
}

func hashResult(v any) (string, error) {
	hashBytes, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to compute scheduling trigger hash: %w", err)
	}

	hash := fnv.New32()
	if _, err = hash.Write(hashBytes); err != nil {
		return "", fmt.Errorf("failed to compute scheduling trigger hash: %w", err)
	}
	result := strconv.FormatInt(int64(hash.Sum32()), 10)

	return result, nil
}

func getReplicaCount(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
) (int64, error) {
	if len(ftc.Spec.PathDefinition.ReplicasSpec) == 0 {
		return 0, nil
	}

	template, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		return 0, err
	}

	value, err := utilunstructured.GetInt64FromPath(template, ftc.Spec.PathDefinition.ReplicasSpec, nil)
	if err != nil || value == nil {
		return 0, err
	}

	return *value, nil
}

func getResourceRequest(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
) (framework.Resource, error) {
	gvk := ftc.GetSourceTypeGVK()
	podSpec, err := podutil.GetResourcePodSpec(fedObject, gvk)
	if err != nil {
		if errors.Is(err, podutil.ErrUnknownTypeToGetPodTemplate) {
			// TODO: update once we have a proper way to obtian resource request from federated objects
			return framework.Resource{}, nil
		}
		return framework.Resource{}, err
	}
	resource := resourceutil.GetPodResourceRequests(podSpec)
	return *framework.NewResource(resource), nil
}

func getPolicySchedulingContentHash(policySpec *fedcorev1a1.PropagationPolicySpec) (string, error) {
	policySpec = policySpec.DeepCopy()
	policySpec.DisableFollowerScheduling = false
	policySpec.AutoMigration = nil
	return hashResult(policySpec)
}

func getClusterLabelsHashes(clusters []*fedcorev1a1.FederatedCluster) ([]keyValue[string, string], error) {
	ret := make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		hash, err := hashResult(sortMap(cluster.GetLabels()))
		if err != nil {
			return nil, err
		}
		ret[cluster.Name] = hash
	}
	return sortMap(ret), nil
}

func getClusterTaintsHashes(clusters []*fedcorev1a1.FederatedCluster) ([]keyValue[string, string], error) {
	ret := make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		taints := make([]corev1.Taint, len(cluster.Spec.Taints))
		for i, t := range cluster.Spec.Taints {
			// NOTE: we omit the TimeAdded field as only changes in Key, Value and Effect should trigger rescheduling
			taints[i] = corev1.Taint{
				Key:    t.Key,
				Value:  t.Value,
				Effect: t.Effect,
			}
		}

		// NOTE: we must sort the taint slice before inserting to ensure deterministic hashing
		sort.Slice(taints, func(i, j int) bool {
			lhs, rhs := taints[i], taints[j]
			switch {
			case lhs.Key != rhs.Key:
				return lhs.Key < rhs.Key
			case lhs.Value != rhs.Value:
				return lhs.Value < rhs.Value
			case lhs.Effect != rhs.Effect:
				return lhs.Value < rhs.Value
			default:
				return false
			}
		})
		hash, err := hashResult(taints)
		if err != nil {
			return nil, err
		}
		ret[cluster.Name] = hash
	}
	return sortMap(ret), nil
}

func getClusterAPIResourceTypesHashes(clusters []*fedcorev1a1.FederatedCluster) ([]keyValue[string, string], error) {
	ret := make(map[string]string, len(clusters))

	for _, cluster := range clusters {
		types := make([]fedcorev1a1.APIResource, len(cluster.Status.APIResourceTypes))
		copy(types, cluster.Status.APIResourceTypes)

		// NOTE: we must sort the slice to ensure deterministic hashing
		sort.Slice(types, func(i, j int) bool {
			lhs, rhs := types[i], types[j]
			switch {
			case lhs.Group != rhs.Group:
				return lhs.Group < rhs.Group
			case lhs.Version != rhs.Version:
				return lhs.Version < rhs.Version
			case lhs.Kind != rhs.Kind:
				return lhs.Kind != rhs.Kind
			case lhs.PluralName != rhs.PluralName:
				return lhs.PluralName < rhs.PluralName
			case lhs.Scope != rhs.Scope:
				return lhs.Scope < rhs.Scope
			default:
				return false
			}
		})

		hash, err := hashResult(types)
		if err != nil {
			return nil, err
		}
		ret[cluster.Name] = hash
	}
	return sortMap(ret), nil
}
