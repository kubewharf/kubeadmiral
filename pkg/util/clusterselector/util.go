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

package clusterselector

import (
	"fmt"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

// ClusterSelectorRequirementsAsSelector converts the []ClusterSelectorRequirement api type into a struct that implements
// labels.Selector.
func ClusterSelectorRequirementsAsSelector(csm []fedcorev1a1.ClusterSelectorRequirement) (labels.Selector, error) {
	if len(csm) == 0 {
		return labels.Nothing(), nil
	}
	selector := labels.NewSelector()
	for _, expr := range csm {
		var op selection.Operator
		switch expr.Operator {
		case fedcorev1a1.ClusterSelectorOpIn:
			op = selection.In
		case fedcorev1a1.ClusterSelectorOpNotIn:
			op = selection.NotIn
		case fedcorev1a1.ClusterSelectorOpExists:
			op = selection.Exists
		case fedcorev1a1.ClusterSelectorOpDoesNotExist:
			op = selection.DoesNotExist
		case fedcorev1a1.ClusterSelectorOpGt:
			op = selection.GreaterThan
		case fedcorev1a1.ClusterSelectorOpLt:
			op = selection.LessThan
		default:
			return nil, fmt.Errorf("%q is not a valid cluster selector operator", expr.Operator)
		}
		r, err := labels.NewRequirement(expr.Key, op, expr.Values)
		if err != nil {
			return nil, err
		}
		selector = selector.Add(*r)
	}
	return selector, nil
}

// ClusterSelectorRequirementsAsFieldSelector converts the []ClusterSelectorRequirement core type into a struct that implements
// fields.Selector.
func ClusterSelectorRequirementsAsFieldSelector(csm []fedcorev1a1.ClusterSelectorRequirement) (fields.Selector, error) {
	if len(csm) == 0 {
		return fields.Nothing(), nil
	}

	selectors := []fields.Selector{}
	for _, expr := range csm {
		switch expr.Operator {
		case fedcorev1a1.ClusterSelectorOpIn:
			if len(expr.Values) != 1 {
				return nil, fmt.Errorf("unexpected number of value (%d) for cluster field selector operator %q",
					len(expr.Values), expr.Operator)
			}
			selectors = append(selectors, fields.OneTermEqualSelector(expr.Key, expr.Values[0]))

		case fedcorev1a1.ClusterSelectorOpNotIn:
			if len(expr.Values) != 1 {
				return nil, fmt.Errorf("unexpected number of value (%d) for cluster field selector operator %q",
					len(expr.Values), expr.Operator)
			}
			selectors = append(selectors, fields.OneTermNotEqualSelector(expr.Key, expr.Values[0]))

		default:
			return nil, fmt.Errorf("%q is not a valid cluster field selector operator", expr.Operator)
		}
	}

	return fields.AndSelectors(selectors...), nil
}

// MatchClusterSelectorTerms checks whether the cluster labels and fields match cluster selector terms.
// The terms are ORed. nil or empty term matches no objects.
func MatchClusterSelectorTerms(
	clusterSelectorTerms []fedcorev1a1.ClusterSelectorTerm,
	cluster *fedcorev1a1.FederatedCluster,
) (bool, error) {
	clusterLabels := labels.Set(cluster.Labels)
	clusterFields := fields.Set{"metadata.name": cluster.Name}

	for _, req := range clusterSelectorTerms {
		// nil or empty term selects no objects
		if len(req.MatchExpressions) == 0 && len(req.MatchFields) == 0 {
			continue
		}

		if len(req.MatchExpressions) != 0 {
			labelSelector, err := ClusterSelectorRequirementsAsSelector(req.MatchExpressions)
			if err != nil {
				return false, err
			}
			if !labelSelector.Matches(clusterLabels) {
				continue
			}
		}

		if len(req.MatchFields) != 0 {
			fieldSelector, err := ClusterSelectorRequirementsAsFieldSelector(req.MatchFields)
			if err != nil {
				return false, err
			}
			if !fieldSelector.Matches(clusterFields) {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}
