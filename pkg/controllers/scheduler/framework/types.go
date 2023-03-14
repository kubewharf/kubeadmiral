/*
Copyright 2019 The Kubernetes Authors.

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
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

type SchedulingUnit struct {
	Name                 string
	Namespace            string
	GroupVersionKind     string
	GroupVersionResource schema.GroupVersionResource

	// Only care about the requests resources
	// TODO(all), limit resources, Best Effort resources
	// The Resource and total replica could be deliever by resource type
	// or define by annotations
	// Describes the schedule request
	DesiredReplicas *int64
	ResourceRequest Resource

	// Describes the current scheduling state
	CurrentClusters map[string]*int64

	// Controls the scheduling behavior
	SchedulingMode fedcorev1a1.SchedulingMode
	StickyCluster  bool

	// Used to filter/select clusters
	ClusterSelector map[string]string
	ClusterNames    map[string]struct{}
	Affinity        *Affinity
	Tolerations     []corev1.Toleration
	MaxClusters     *int64
	MinReplicas     map[string]int64
	MaxReplicas     map[string]int64
	Weights         map[string]int64
}

// Affinity is a group of affinity scheduling rules.
type Affinity struct {
	// Describes cluster affinity scheduling rules for the scheduling unit.
	ClusterAffinity *ClusterAffinity `json:"clusterAffinity,omitempty"`
}

// ClusterAffinity is a group of node affinity scheduling rules.
type ClusterAffinity struct {
	// If the affinity requirements specified by this field are not met at
	// scheduling time, the scheduling unit will not be scheduled onto the cluster.
	RequiredDuringSchedulingIgnoredDuringExecution *ClusterSelector `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
	// The scheduler will prefer to schedule scheduling units to clusters that satisfy
	// the affinity expressions specified by this field, but it may choose
	// a cluster that violates one or more of the expressions. The cluster that is
	// most preferred is the one with the greatest sum of weights, i.e.
	// for each cluster that meets all of the scheduling requirements (resource
	// request, requiredDuringScheduling affinity expressions, etc.),
	// compute a sum by iterating through the elements of this field and adding
	// "weight" to the sum if the cluster matches the corresponding matchExpressions; the
	// cluster(s) with the highest sum are the most preferred.
	// +optional
	PreferredDuringSchedulingIgnoredDuringExecution []PreferredSchedulingTerm `json:"preferredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// ClusterSelector represents the union of the results of one or more label queries
// over a set of clusters; that is, it represents the OR of the selectors represented
// by the cluster selector terms.
type ClusterSelector struct {
	// A list of cluster selector terms. The terms are ORed.
	ClusterSelectorTerms []fedcorev1a1.ClusterSelectorTerm `json:"clusterSelectorTerms"`
}

// An empty preferred scheduling term matches all objects with implicit weight 0
// (i.e. it's a no-op). A null preferred scheduling term matches no objects (i.e. is also a no-op).
type PreferredSchedulingTerm struct {
	// Weight associated with matching the corresponding nodeSelectorTerm, in the range 1-100.
	Weight int32 `json:"weight"`
	// A node selector term, associated with the corresponding weight.
	Preference fedcorev1a1.ClusterSelectorTerm `json:"preference"`
}

func (s *SchedulingUnit) Key() string {
	return s.Namespace + "/" + s.Name
}

type ClusterScore struct {
	Cluster *fedcorev1a1.FederatedCluster
	Score   int64
}

type ClusterScoreList []ClusterScore

type PluginToClusterScore map[string]ClusterScoreList

type ClusterReplicas struct {
	Cluster  *fedcorev1a1.FederatedCluster
	Replicas int64
}

type ClusterReplicasList []ClusterReplicas

// Result indicates the result of running a plugin. It consists of a code, a
// message and (optionally) an error. When the status code is not `Success`,
// the reasons should explain why.
type Result struct {
	code    Code
	reasons []string
	err     error
}

// Code is the Status code/type which is returned from plugins.
type Code int

// These are predefined codes used in a Status.
const (
	// Success means that plugin ran correctly and found resource schedulable.
	// NOTE: A nil status is also considered as "Success".
	Success Code = iota
	// Unschedulable is used when a plugin finds the resource unschedulable.
	// The accompanying status message should explain why the it is unschedulable.
	Unschedulable
	// Error is used for internal plugin errors, unexpected input, etc.
	Error
)

// NewResult makes a result out of the given arguments and returns its pointer.
func NewResult(code Code, reasons ...string) *Result {
	s := &Result{
		code:    code,
		reasons: reasons,
	}
	if code == Error {
		s.err = errors.New(strings.Join(reasons, ","))
	}
	return s
}

// IsSuccess returns true if and only if "Result" is nil or Code is "Success".
func (s *Result) IsSuccess() bool {
	return s == nil || s.code == Success
}

// Message returns a concatenated message on reasons of the Status.
func (s *Result) Message() string {
	if s == nil {
		return ""
	}
	return strings.Join(s.reasons, ", ")
}

// AsError returns nil if the Result is a success; otherwise returns an "error" object
// with a concatenated message on reasons of the Result.
func (s *Result) AsError() error {
	if s.IsSuccess() {
		return nil
	}
	if s.err != nil {
		return s.err
	}
	return errors.New(strings.Join(s.reasons, ", "))
}
