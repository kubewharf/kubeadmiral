/*
Copyright 2016 The Kubernetes Authors.

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

package refinedplanner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/planner"
)

func doCheck(t *testing.T, pref map[string]planner.ClusterPreferences, replicas int64, clusters []string, expected map[string]int64) {
	planer := NewPlanner(&ReplicaSchedulingPreference{
		Clusters:      pref,
		TotalReplicas: replicas,
	})
	plan, overflow, err := planer.Plan(clusters, map[string]int64{}, map[string]int64{}, "")
	assert.Nil(t, err)
	assert.EqualValues(t, expected, plan)
	assert.Equal(t, 0, len(overflow))
}

func doCheckWithExisting(t *testing.T, rebalance bool, pref map[string]planner.ClusterPreferences, replicas int64, clusters []string,
	existing map[string]int64, expected map[string]int64) {
	planer := NewPlanner(&ReplicaSchedulingPreference{
		Rebalance:     rebalance,
		Clusters:      pref,
		TotalReplicas: replicas,
	})
	plan, overflow, err := planer.Plan(clusters, existing, map[string]int64{}, "")
	assert.Nil(t, err)
	assert.Equal(t, 0, len(overflow))
	assert.EqualValues(t, expected, plan)
}

func pint(val int64) *int64 {
	return &val
}

func TestEqual(t *testing.T) {
	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B", "C"},
		// hash dependent
		map[string]int64{"A": 16, "B": 17, "C": 17})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B"},
		map[string]int64{"A": 25, "B": 25})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		1, []string{"A", "B"},
		// hash dependent
		map[string]int64{"A": 0, "B": 1})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		1, []string{"A", "B", "C", "D"},
		// hash dependent
		map[string]int64{"A": 0, "B": 0, "C": 0, "D": 1})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		1, []string{"A"},
		map[string]int64{"A": 1})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		1, []string{},
		map[string]int64{})
}

func TestEqualWithExisting(t *testing.T) {
	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B", "C"},
		map[string]int64{"C": 30},
		map[string]int64{"A": 9, "B": 11, "C": 30})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B"},
		map[string]int64{"A": 30},
		map[string]int64{"A": 30, "B": 20})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		15, []string{"A", "B"},
		map[string]int64{"A": 0, "B": 8},
		map[string]int64{"A": 7, "B": 8})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		15, []string{"A", "B"},
		map[string]int64{"A": 1, "B": 8},
		map[string]int64{"A": 7, "B": 8})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		15, []string{"A", "B"},
		map[string]int64{"A": 4, "B": 8},
		map[string]int64{"A": 7, "B": 8})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		15, []string{"A", "B"},
		map[string]int64{"A": 5, "B": 8},
		map[string]int64{"A": 7, "B": 8})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		15, []string{"A", "B"},
		map[string]int64{"A": 6, "B": 8},
		map[string]int64{"A": 7, "B": 8})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		15, []string{"A", "B"},
		map[string]int64{"A": 7, "B": 8},
		map[string]int64{"A": 7, "B": 8})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		500000, []string{"A", "B"},
		map[string]int64{"A": 300000},
		map[string]int64{"A": 300000, "B": 200000})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B"},
		map[string]int64{"A": 10},
		map[string]int64{"A": 25, "B": 25})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B"},
		map[string]int64{"A": 10, "B": 70},
		// hash dependent
		map[string]int64{"A": 10, "B": 40})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		1, []string{"A", "B"},
		map[string]int64{"A": 30},
		map[string]int64{"A": 1, "B": 0})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"*": {Weight: 1}},
		50, []string{"A", "B"},
		map[string]int64{"A": 10, "B": 20},
		map[string]int64{"A": 25, "B": 25})

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"A": {Weight: 499},
		"B": {Weight: 499},
		"C": {Weight: 1}},
		15, []string{"A", "B", "C"},
		map[string]int64{"A": 15, "B": 15, "C": 0},
		map[string]int64{"A": 7, "B": 8, "C": 0},
	)

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"A": {Weight: 1},
		"B": {Weight: 1},
		"C": {Weight: 1}},
		18, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 1, "C": 1},
		map[string]int64{"A": 10, "B": 4, "C": 4},
	)

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"A": {Weight: 1},
		"B": {Weight: 1},
		"C": {Weight: 1}},
		18, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 1, "C": 1},
		map[string]int64{"A": 10, "B": 4, "C": 4},
	)

	doCheckWithExisting(t, true, map[string]planner.ClusterPreferences{
		"A": {Weight: 1},
		"B": {Weight: 1},
		"C": {Weight: 1}},
		18, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 1, "C": 1},
		map[string]int64{"A": 6, "B": 6, "C": 6},
	)

	doCheckWithExisting(t, false, map[string]planner.ClusterPreferences{
		"A": {Weight: 0},
		"B": {Weight: 1},
		"C": {Weight: 1}},
		18, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 1, "C": 7},
		map[string]int64{"A": 10, "B": 1, "C": 7},
	)

	doCheckWithExisting(t, true, map[string]planner.ClusterPreferences{
		"A": {Weight: 0},
		"B": {Weight: 1},
		"C": {Weight: 1}},
		18, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 1, "C": 7},
		map[string]int64{"A": 0, "B": 9, "C": 9},
	)
}

func TestMin(t *testing.T) {
	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {MinReplicas: 2, Weight: 0}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 2, "B": 2, "C": 2})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {MinReplicas: 20, Weight: 0}},
		50, []string{"A", "B", "C"},
		// hash dependant.
		map[string]int64{"A": 10, "B": 20, "C": 20})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {MinReplicas: 20, Weight: 0},
		"A": {MinReplicas: 100, Weight: 1}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 50, "B": 0, "C": 0})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {MinReplicas: 10, Weight: 1, MaxReplicas: pint(12)}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 12, "B": 12, "C": 12})
}

func TestMax(t *testing.T) {
	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 1, MaxReplicas: pint(2)}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 2, "B": 2, "C": 2})

	doCheck(t, map[string]planner.ClusterPreferences{
		"*": {Weight: 0, MaxReplicas: pint(2)}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 0, "B": 0, "C": 0})
}

func TestWeight(t *testing.T) {
	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 1},
		"B": {Weight: 2}},
		60, []string{"A", "B", "C"},
		map[string]int64{"A": 20, "B": 40})

	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 10000},
		"B": {Weight: 1}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 50, "B": 0})

	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 10000},
		"B": {Weight: 1}},
		50, []string{"B", "C"},
		map[string]int64{"B": 50})

	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 10000, MaxReplicas: pint(10)},
		"B": {Weight: 1},
		"C": {Weight: 1}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 20, "C": 20})

	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 10000, MaxReplicas: pint(10)},
		"B": {Weight: 1},
		"C": {Weight: 1, MaxReplicas: pint(10)}},
		50, []string{"A", "B", "C"},
		map[string]int64{"A": 10, "B": 30, "C": 10})

	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 10000, MaxReplicas: pint(10)},
		"B": {Weight: 1},
		"C": {Weight: 1, MaxReplicas: pint(21)},
		"D": {Weight: 1, MaxReplicas: pint(10)}},
		71, []string{"A", "B", "C", "D"},
		map[string]int64{"A": 10, "B": 30, "C": 21, "D": 10})

	doCheck(t, map[string]planner.ClusterPreferences{
		"A": {Weight: 10000, MaxReplicas: pint(10)},
		"B": {Weight: 1},
		"C": {Weight: 1, MaxReplicas: pint(21)},
		"D": {Weight: 1, MaxReplicas: pint(10)},
		"E": {Weight: 1}},
		91, []string{"A", "B", "C", "D", "E"},
		map[string]int64{"A": 10, "B": 25, "C": 21, "D": 10, "E": 25})
}
