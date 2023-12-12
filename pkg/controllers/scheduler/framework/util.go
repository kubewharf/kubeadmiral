/*
Copyright 2015 The Kubernetes Authors.

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

package framework

import (
	"context"
	"fmt"
	"math"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

const (
	ResourceGPU = corev1.ResourceName("nvidia.com/gpu")
)

// For each of these resources, a pod that doesn't request the resource explicitly
// will be treated as having requested the amount indicated below, for the purpose
// of computing priority only. This ensures that when scheduling zero-request pods, such
// pods will not all be scheduled to the machine with the smallest in-use request,
// and that when scheduling regular pods, such pods will not see zero-request pods as
// consuming no resources whatsoever. We chose these values to be similar to the
// resources that we give to cluster addon pods (#10653). But they are pretty arbitrary.
// As described in #11713, we use request instead of limit to deal with resource requirements.
const (
	// DefaultMilliCPURequest defines default milli cpu request number.
	DefaultMilliCPURequest int64 = 100 // 0.1 core
	// DefaultMemoryRequest defines default memory request size.
	DefaultMemoryRequest int64 = 200 * 1024 * 1024 // 200 MB
)

const (
	// MaxClusterScore is the maximum score a Score plugin is expected to return.
	MaxClusterScore int64 = 100
	// MinClusterScore is the minimum score a Score plugin is expected to return.
	MinClusterScore int64 = 0
	// MaxTotalScore is the maximum total score.
	MaxTotalScore int64 = math.MaxInt64
)

// TODO(feature), make the RequestedRatioResources configable

// DefaultRequestedRatioResources is an empirical value derived from practice.
var DefaultRequestedRatioResources = ResourceToWeightMap{corev1.ResourceMemory: 1, corev1.ResourceCPU: 1, ResourceGPU: 4}

type (
	ResourceToValueMap  map[corev1.ResourceName]int64
	ResourceToWeightMap map[corev1.ResourceName]int64
	ResourceName        string
)

// Resource is a collection of compute resource.
type Resource struct {
	MilliCPU         int64 `json:"millicpu"`
	Memory           int64 `json:"memory"`
	EphemeralStorage int64 `json:"ephemeralStorage"`
	// ScalarResources
	ScalarResources map[corev1.ResourceName]int64 `json:"scalarResources"`
}

// InsufficientResource describes what kind of resource limit is hit and caused the pod to not fit the node.
type InsufficientResource struct {
	ResourceName corev1.ResourceName
	// We explicitly have a parameter for reason to avoid formatting a message on the fly
	// for common resources, which is expensive for cluster autoscaler simulations.
	Reason    string
	Requested int64
	Used      int64
	Capacity  int64
}

// NewResource creates a Resource from ResourceList
func NewResource(rl corev1.ResourceList) *Resource {
	r := &Resource{}
	r.Add(rl)
	return r
}

// Add adds ResourceList into Resource.
func (r *Resource) Add(rl corev1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuant := range rl {
		switch rName {
		case corev1.ResourceCPU:
			r.MilliCPU += rQuant.MilliValue()
		case corev1.ResourceMemory:
			r.Memory += rQuant.Value()
		case corev1.ResourceEphemeralStorage:
			// if the local storage capacity isolation feature gate is disabled, pods request 0 disk.
			// NOTE(wsfdl), I don't think we need to care about the storage.
			r.EphemeralStorage += rQuant.Value()
		default:
			if IsScalarResourceName(rName) {
				r.AddScalar(rName, rQuant.Value())
			}
		}
	}
}

// Sub is used to subtract two resources.
// Return error when the minuend is less than the subtrahend.
func (r *Resource) Sub(rl corev1.ResourceList) error {
	for rName, rQuant := range rl {
		switch rName {
		case corev1.ResourceCPU:
			cpu := rQuant.MilliValue()
			if r.MilliCPU < cpu {
				return fmt.Errorf("cpu difference is less than 0, remain %d, got %d", r.MilliCPU, cpu)
			}
			r.MilliCPU -= cpu
		case corev1.ResourceMemory:
			mem := rQuant.Value()
			if r.Memory < mem {
				return fmt.Errorf("memory difference is less than 0, remain %d, got %d", r.Memory, mem)
			}
			r.Memory -= mem
		case corev1.ResourceEphemeralStorage:
			ephemeralStorage := rQuant.Value()
			if r.EphemeralStorage < ephemeralStorage {
				return fmt.Errorf(
					"allowed storage number difference is less than 0, remain %d, got %d",
					r.EphemeralStorage,
					ephemeralStorage,
				)
			}
			r.EphemeralStorage -= ephemeralStorage
		default:
			if IsScalarResourceName(rName) {
				rScalar, ok := r.ScalarResources[rName]
				scalar := rQuant.Value()
				if !ok && scalar > 0 {
					return fmt.Errorf("scalar resources %s does not exist, got %d", rName, scalar)
				}
				if rScalar < scalar {
					return fmt.Errorf(
						"scalar resources %s difference is less than 0, remain %d, got %d",
						rName,
						rScalar,
						scalar,
					)
				}
				r.ScalarResources[rName] = rScalar - scalar
			}
		}
	}
	return nil
}

// ResourceList returns a resource list of this resource.
func (r *Resource) ResourceList() corev1.ResourceList {
	result := corev1.ResourceList{
		corev1.ResourceCPU:              *resource.NewMilliQuantity(r.MilliCPU, resource.DecimalSI),
		corev1.ResourceMemory:           *resource.NewQuantity(r.Memory, resource.BinarySI),
		corev1.ResourceEphemeralStorage: *resource.NewQuantity(r.EphemeralStorage, resource.BinarySI),
	}
	for rName, rQuant := range r.ScalarResources {
		if IsHugePageResourceName(rName) {
			result[rName] = *resource.NewQuantity(rQuant, resource.BinarySI)
		} else {
			result[rName] = *resource.NewQuantity(rQuant, resource.DecimalSI)
		}
	}
	return result
}

// Clone returns a copy of this resource.
func (r *Resource) Clone() *Resource {
	res := &Resource{
		MilliCPU:         r.MilliCPU,
		Memory:           r.Memory,
		EphemeralStorage: r.EphemeralStorage,
	}
	if r.ScalarResources != nil {
		res.ScalarResources = make(map[corev1.ResourceName]int64)
		for k, v := range r.ScalarResources {
			res.ScalarResources[k] = v
		}
	}
	return res
}

// AddScalar adds a resource by a scalar value of this resource.
func (r *Resource) AddScalar(name corev1.ResourceName, quantity int64) {
	r.SetScalar(name, r.ScalarResources[name]+quantity)
}

// SetScalar sets a resource by a scalar value of this resource.
func (r *Resource) SetScalar(name corev1.ResourceName, quantity int64) {
	// Lazily allocate scalar resource map.
	if r.ScalarResources == nil {
		r.ScalarResources = map[corev1.ResourceName]int64{}
	}
	r.ScalarResources[name] = quantity
}

// SetMaxResource compares with ResourceList and takes max value for each Resource.
func (r *Resource) SetMaxResource(rl corev1.ResourceList) {
	if r == nil {
		return
	}

	for rName, rQuantity := range rl {
		switch rName {
		case corev1.ResourceMemory:
			if mem := rQuantity.Value(); mem > r.Memory {
				r.Memory = mem
			}
		case corev1.ResourceCPU:
			if cpu := rQuantity.MilliValue(); cpu > r.MilliCPU {
				r.MilliCPU = cpu
			}
		case corev1.ResourceEphemeralStorage:
			if ephemeralStorage := rQuantity.Value(); ephemeralStorage > r.EphemeralStorage {
				r.EphemeralStorage = ephemeralStorage
			}
		default:
			if IsScalarResourceName(rName) {
				value := rQuantity.Value()
				if value > r.ScalarResources[rName] {
					r.SetScalar(rName, value)
				}
			}
		}
	}
}

// HasScalarResource checks if Resource has the given scalar resource.
func (r *Resource) HasScalarResource(name corev1.ResourceName) bool {
	if r.ScalarResources == nil {
		return false
	}

	if _, exists := r.ScalarResources[name]; exists {
		return true
	}
	return false
}

// resourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers) + overHead
//
//nolint:unused
func calculateResource(pod *corev1.Pod) (res Resource, non0CPU int64, non0Mem int64) {
	resPtr := &res
	for _, c := range pod.Spec.Containers {
		resPtr.Add(c.Resources.Requests)
		non0CPUReq, non0MemReq := GetNonzeroRequests(&c.Resources.Requests)
		non0CPU += non0CPUReq
		non0Mem += non0MemReq
		// No non-zero resources for GPUs or opaque resources.
	}

	for _, ic := range pod.Spec.InitContainers {
		resPtr.SetMaxResource(ic.Resources.Requests)
		non0CPUReq, non0MemReq := GetNonzeroRequests(&ic.Resources.Requests)
		if non0CPU < non0CPUReq {
			non0CPU = non0CPUReq
		}

		if non0Mem < non0MemReq {
			non0Mem = non0MemReq
		}
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil {
		resPtr.Add(pod.Spec.Overhead)
		if _, found := pod.Spec.Overhead[corev1.ResourceCPU]; found {
			non0CPU += pod.Spec.Overhead.Cpu().MilliValue()
		}

		if _, found := pod.Spec.Overhead[corev1.ResourceMemory]; found {
			non0Mem += pod.Spec.Overhead.Memory().Value()
		}
	}

	return res, non0CPU, non0Mem
}

func GetResourceRequest(pod *corev1.Pod) *Resource {
	result := &Resource{}
	for _, container := range pod.Spec.Containers {
		result.Add(container.Resources.Requests)
	}

	// take max_resource(sum_pod, any_init_container)
	for _, container := range pod.Spec.InitContainers {
		result.SetMaxResource(container.Resources.Requests)
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil {
		result.Add(pod.Spec.Overhead)
	}

	return result
}

// GetNonzeroRequests returns the default cpu and memory resource request if none is found or
// what is provided on the request.
func GetNonzeroRequests(requests *corev1.ResourceList) (int64, int64) {
	return GetNonzeroRequestForResource(corev1.ResourceCPU, requests),
		GetNonzeroRequestForResource(corev1.ResourceMemory, requests)
}

// GetNonzeroRequestForResource returns the default resource request if none is found or
// what is provided on the request.
func GetNonzeroRequestForResource(resource corev1.ResourceName, requests *corev1.ResourceList) int64 {
	switch resource {
	case corev1.ResourceCPU:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[corev1.ResourceCPU]; !found {
			return DefaultMilliCPURequest
		}
		return requests.Cpu().MilliValue()
	case corev1.ResourceMemory:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[corev1.ResourceMemory]; !found {
			return DefaultMemoryRequest
		}
		return requests.Memory().Value()
	case corev1.ResourceEphemeralStorage:
		quantity, found := (*requests)[corev1.ResourceEphemeralStorage]
		if !found {
			return 0
		}
		return quantity.Value()
	default:
		if IsScalarResourceName(resource) {
			quantity, found := (*requests)[resource]
			if !found {
				return 0
			}
			return quantity.Value()
		}
	}
	return 0
}

func PreCheck(ctx context.Context, su *SchedulingUnit, cluster *fedcorev1a1.FederatedCluster) error {
	if su == nil {
		return fmt.Errorf("invalid scheduling unit")
	}
	if cluster == nil {
		return fmt.Errorf("invalid federated cluster")
	}
	return nil
}

// Extended and Hugepages resources
func IsScalarResourceName(name corev1.ResourceName) bool {
	return IsExtendedResourceName(name) || IsHugePageResourceName(name) ||
		IsPrefixedNativeResource(name) || IsAttachableVolumeResourceName(name)
}

// IsExtendedResourceName returns true if:
// 1. the resource name is not in the default namespace;
// 2. resource name does not have "requests." prefix,
// to avoid confusion with the convention in quota
// 3. it satisfies the rules in IsQualifiedName() after converted into quota resource name
func IsExtendedResourceName(name corev1.ResourceName) bool {
	if IsNativeResource(name) || strings.HasPrefix(string(name), corev1.DefaultResourceRequestsPrefix) {
		return false
	}
	// Ensure it satisfies the rules in IsQualifiedName() after converted into quota resource name
	nameForQuota := fmt.Sprintf("%s%s", corev1.DefaultResourceRequestsPrefix, string(name))
	if errs := validation.IsQualifiedName(nameForQuota); len(errs) != 0 {
		return false
	}
	return true
}

// IsPrefixedNativeResource returns true if the resource name is in the
// *kubernetes.io/ namespace.
func IsPrefixedNativeResource(name corev1.ResourceName) bool {
	return strings.Contains(string(name), corev1.ResourceDefaultNamespacePrefix)
}

// IsNativeResource returns true if the resource name is in the
// *kubernetes.io/ namespace. Partially-qualified (unprefixed) names are
// implicitly in the kubernetes.io/ namespace.
func IsNativeResource(name corev1.ResourceName) bool {
	return !strings.Contains(string(name), "/") ||
		IsPrefixedNativeResource(name)
}

func IsAttachableVolumeResourceName(name corev1.ResourceName) bool {
	return strings.HasPrefix(string(name), corev1.ResourceAttachableVolumesPrefix)
}

// IsHugePageResourceName returns true if the resource name has the huge page
// resource prefix.
func IsHugePageResourceName(name corev1.ResourceName) bool {
	return strings.HasPrefix(string(name), corev1.ResourceHugePagesPrefix)
}

// TolerationsTolerateTaint checks if taint is tolerated by any of the tolerations.
func TolerationsTolerateTaint(tolerations []corev1.Toleration, taint *corev1.Taint) bool {
	for i := range tolerations {
		if tolerations[i].ToleratesTaint(taint) {
			return true
		}
	}
	return false
}

type taintsFilterFunc func(*corev1.Taint) bool

// FindMatchingUntoleratedTaint checks if the given tolerations tolerates
// all the filtered taints, and returns the first taint without a toleration
// Returns true if there is an untolerated taint
// Returns false if all taints are tolerated
func FindMatchingUntoleratedTaint(
	taints []corev1.Taint,
	tolerations []corev1.Toleration,
	inclusionFilter taintsFilterFunc,
) (corev1.Taint, bool) {
	filteredTaints := getFilteredTaints(taints, inclusionFilter)
	for _, taint := range filteredTaints {
		taint := taint
		if !TolerationsTolerateTaint(tolerations, &taint) {
			return taint, true
		}
	}
	return corev1.Taint{}, false
}

// getFilteredTaints returns a list of taints satisfying the filter predicate
func getFilteredTaints(taints []corev1.Taint, inclusionFilter taintsFilterFunc) []corev1.Taint {
	if inclusionFilter == nil {
		return taints
	}
	filteredTaints := []corev1.Taint{}
	for _, taint := range taints {
		taint := taint
		if !inclusionFilter(&taint) {
			continue
		}
		filteredTaints = append(filteredTaints, taint)
	}
	return filteredTaints
}

// DefaultNormalizeScore generates a Normalize Score function that can normalize the
// scores to [0, maxPriority]. If reverse is set to true, it reverses the scores by
// subtracting it from maxPriority.
func DefaultNormalizeScore(maxPriority int64, reverse bool, scores ClusterScoreList) *Result {
	var maxCount int64
	for i := range scores {
		if scores[i].Score > maxCount {
			maxCount = scores[i].Score
		}
	}

	if maxCount == 0 {
		if reverse {
			for i := range scores {
				scores[i].Score = maxPriority
			}
		}
		return nil
	}

	for i := range scores {
		score := scores[i].Score

		score = maxPriority * score / maxCount
		if reverse {
			score = maxPriority - score
		}

		scores[i].Score = score
	}
	return nil
}

func MinInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func ExtractClusterNames(clusters []*fedcorev1a1.FederatedCluster) []string {
	ret := make([]string, len(clusters))
	for i := range clusters {
		ret[i] = clusters[i].Name
	}
	return ret
}
