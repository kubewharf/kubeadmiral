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

	"golang.org/x/exp/constraints"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/controllers/util/unstructured"
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
2. generation change (spec update)

Cluster changes:
1. cluster creation
2. cluster ready condition change
3. cluster labels change
2. cluster taints change

Simply checking for these triggers in the event handlers is insufficient. This is because when the controller restarts, all objects will be
"created" again, causing mass rescheduling for all objects. Thus, we hash the scheduling triggers and write it into the federated object's
annotations. Before reconciling a federated object, we check this hash to determine if any scheduling triggers have changed.
*/

type keyValue[K any, V any] struct {
	Key   K `json:"key"`
	Value V `json:"value"`
}

// SortableMap is a simple implementation of map that can be to be used for hashing. The native golang map cannot be used for hashing as it
// is unordered and can produce non-deterministic hashes.
type SortableMap[K constraints.Ordered, V any] struct {
	KeyValues []keyValue[K, V] `json:"keyValues"`
}

func (m *SortableMap[K, V]) Put(key K, value V) {
	found := false
	for _, kv := range m.KeyValues {
		if kv.Key == key {
			kv.Value = value
			found = true
			break
		}
	}

	if !found {
		m.KeyValues = append(m.KeyValues, keyValue[K, V]{Key: key, Value: value})
	}
}

func (m *SortableMap[K, V]) Sort() {
	sort.Slice(m.KeyValues, func(i, j int) bool {
		return m.KeyValues[i].Key < m.KeyValues[j].Key
	})
}

func NewSortableMap[K constraints.Ordered, V any]() SortableMap[K, V] {
	return SortableMap[K, V]{
		KeyValues: []keyValue[K, V]{},
	}
}

type schedulingTriggers struct {
	SchedulingAnnotations SortableMap[string, string] `json:"schedulingAnnotations"`
	ReplicaCount          int64                       `json:"replicaCount"`
	ResourceRequest       framework.Resource          `json:"resourceRequest"`

	PolicyName       string `json:"policyName"`
	PolicyGeneration int64  `json:"policyGeneration"`

	// a map from each cluster to its ready condition
	ClusterReady SortableMap[string, bool] `json:"clusterReady"`
	// a map from each cluster to its labels
	ClusterLabels SortableMap[string, SortableMap[string, string]] `json:"clusterLabels"`
	// a map from each cluster to its taints
	ClusterTaints SortableMap[string, []corev1.Taint] `json:"clusterTaints"`
}

func (s *Scheduler) computeSchedulingTriggerHash(
	fedObject *unstructured.Unstructured,
	policy fedcorev1a1.GenericPropagationPolicy,
	clusters []*fedcorev1a1.FederatedCluster,
) (string, error) {
	trigger := &schedulingTriggers{}

	var err error

	trigger.SchedulingAnnotations = getSchedulingAnnotations(fedObject)
	if trigger.ReplicaCount, err = getReplicaCount(s.typeConfig, fedObject); err != nil {
		return "", fmt.Errorf("failed to get object replica count: %w", err)
	}
	trigger.ResourceRequest = getResourceRequest(fedObject)

	if policy != nil {
		trigger.PolicyName = policy.GetName()
		trigger.PolicyGeneration = policy.GetGeneration()
	}

	trigger.ClusterReady = getClusterReady(clusters)
	trigger.ClusterLabels = getClusterLabels(clusters)
	trigger.ClusterTaints = getClusterTaints(clusters)

	triggerBytes, err := json.Marshal(trigger)
	if err != nil {
		return "", fmt.Errorf("failed to compute scheduling trigger hash: %w", err)
	}

	hash := fnv.New32()
	if _, err = hash.Write(triggerBytes); err != nil {
		return "", fmt.Errorf("failed to compute scheduling trigger hash: %w", err)
	}
	triggerHash := strconv.FormatInt(int64(hash.Sum32()), 10)

	return triggerHash, nil
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

func getSchedulingAnnotations(fedObject *unstructured.Unstructured) SortableMap[string, string] {
	ret := NewSortableMap[string, string]()
	for k, v := range fedObject.GetAnnotations() {
		if knownSchedulingAnnotations.Has(k) {
			ret.Put(k, v)
		}
	}
	ret.Sort()
	return ret
}

func getReplicaCount(typeConfig *fedcorev1a1.FederatedTypeConfig, fedObject *unstructured.Unstructured) (int64, error) {
	if len(typeConfig.Spec.PathDefinition.ReplicasSpec) == 0 {
		return 0, nil
	}

	value, err := utilunstructured.GetInt64FromPath(
		fedObject,
		typeConfig.Spec.PathDefinition.ReplicasSpec,
		common.TemplatePath,
	)
	if err != nil || value == nil {
		return 0, err
	}

	return *value, nil
}

func getResourceRequest(fedObject *unstructured.Unstructured) framework.Resource {
	// TODO: update once we have a proper way to obtian resource request from federated objects
	return framework.Resource{}
}

func getClusterReady(clusters []*fedcorev1a1.FederatedCluster) SortableMap[string, bool] {
	ret := NewSortableMap[string, bool]()
	for _, cluster := range clusters {
		ret.Put(cluster.Name, util.IsClusterReady(&cluster.Status))
	}
	ret.Sort()
	return ret
}

func getClusterLabels(clusters []*fedcorev1a1.FederatedCluster) SortableMap[string, SortableMap[string, string]] {
	ret := NewSortableMap[string, SortableMap[string, string]]()
	for _, cluster := range clusters {
		labelsMap := NewSortableMap[string, string]()
		for k, v := range cluster.GetLabels() {
			labelsMap.Put(k, v)
		}
		labelsMap.Sort()
		ret.Put(cluster.Name, labelsMap)
	}
	ret.Sort()
	return ret
}

func getClusterTaints(clusters []*fedcorev1a1.FederatedCluster) SortableMap[string, []corev1.Taint] {
	ret := NewSortableMap[string, []corev1.Taint]()
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
		ret.Put(cluster.Name, taints)
	}
	ret.Sort()
	return ret
}

// enqueueFederatedObjectsForPolicy enqueues federated objects which match the policy
func (s *Scheduler) enqueueFederatedObjectsForPolicy(policy pkgruntime.Object) {
	policyAccessor, ok := policy.(fedcorev1a1.GenericPropagationPolicy)
	if !ok {
		s.logger.Error(fmt.Errorf("policy is not a valid type (%T)", policy), "Failed to enqueue federated object for policy")
		return
	}

	s.logger.WithValues("policy", policyAccessor.GetName()).Info("Enqueue federated objects for policy")

	fedObjects, err := s.federatedObjectLister.List(labels.Everything())
	if err != nil {
		s.logger.WithValues("policy", policyAccessor.GetName()).Error(err, "Failed to enqueue federated objects for policy")
		return
	}

	for _, fedObject := range fedObjects {
		fedObject := fedObject.(*unstructured.Unstructured)
		policyKey, found := MatchedPolicyKey(fedObject, s.typeConfig.GetNamespaced())
		if !found {
			continue
		}

		if policyKey.Name == policyAccessor.GetName() && policyKey.Namespace == policyAccessor.GetNamespace() {
			s.worker.EnqueueObject(fedObject)
		}
	}
}

// enqueueFederatedObjectsForCluster enqueues all federated objects only if the cluster is joined
func (s *Scheduler) enqueueFederatedObjectsForCluster(cluster pkgruntime.Object) {
	clusterObj := cluster.(*fedcorev1a1.FederatedCluster)
	if !util.IsClusterJoined(&clusterObj.Status) {
		s.logger.WithValues("cluster", clusterObj.Name).Info("Skip enqueue federated objects for cluster, cluster not joined")
		return
	}

	s.logger.WithValues("cluster", clusterObj.Name).Info("Enqueue federated objects for cluster")

	fedObjects, err := s.federatedObjectLister.List(labels.Everything())
	if err != nil {
		s.logger.Error(err, "Failed to enqueue federated object for cluster")
		return
	}

	for _, fedObject := range fedObjects {
		s.worker.EnqueueObject(fedObject)
	}
}
