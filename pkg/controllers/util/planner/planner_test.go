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

package planner

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"
)

type testCase struct {
	rsp      map[string]ClusterPreferences
	replicas int64
	clusters []string
	existing map[string]int64
	capacity map[string]int64
}

type expectedResult struct {
	plan     map[string]int64
	overflow map[string]int64
}

func estimateCapacity(currentReplicas, actualCapacity map[string]int64) map[string]int64 {
	estimatedCapacity := make(map[string]int64, len(actualCapacity))
	for cluster, c := range actualCapacity {
		if currentReplicas[cluster] > c {
			estimatedCapacity[cluster] = c
		}
	}

	return estimatedCapacity
}

func doCheck(
	t *testing.T,
	tc *testCase,
	avoidDisruption bool,
	keepUnschedulableReplicas bool,
	expected *expectedResult,
) {
	t.Helper()
	assert := assert.New(t)

	existing := tc.existing
	var plan, overflow, lastPlan, lastOverflow map[string]int64
	var err error

	converged := false
	const maxConvergenceSteps = 3
	for i := 0; i < maxConvergenceSteps; i++ {
		estimatedCapacity := estimateCapacity(existing, tc.capacity)
		plan, overflow, err = Plan(
			&ReplicaSchedulingPreference{
				Clusters: tc.rsp,
			},
			tc.replicas, tc.clusters,
			existing, estimatedCapacity, "",
			avoidDisruption, keepUnschedulableReplicas,
		)

		assert.Nil(err)
		t.Logf("Step %v: avoidDisruption=%v keepUnschedulableReplicas=%v pref=%+v existing=%v estimatedCapacity=%v plan=%v overflow=%v\n",
			i, avoidDisruption, keepUnschedulableReplicas, tc.rsp, existing, estimatedCapacity, plan, overflow)

		// nil and empty map should be treated as equal
		planConverged := len(plan) == 0 && len(lastPlan) == 0 || reflect.DeepEqual(plan, lastPlan)
		overflowConverged := len(overflow) == 0 && len(lastOverflow) == 0 || reflect.DeepEqual(overflow, lastOverflow)
		converged = planConverged && overflowConverged
		if converged {
			// Break out of the loop if converged
			break
		}

		// Not converged yet, do another round
		existing = make(map[string]int64, len(plan))
		for cluster, replicas := range plan {
			existing[cluster] += replicas
		}
		for cluster, replicas := range overflow {
			existing[cluster] += replicas
		}

		lastPlan, lastOverflow = plan, overflow
	}

	if !converged {
		t.Errorf("did not converge after %v steps", maxConvergenceSteps)
	}

	if len(plan) != 0 || len(expected.plan) != 0 {
		assert.Equal(expected.plan, plan)
	}
	if len(overflow) != 0 || len(expected.overflow) != 0 {
		assert.Equal(expected.overflow, overflow)
	}
}

func doCheckWithoutExisting(
	t *testing.T,
	tc *testCase,
	expected *expectedResult,
) {
	// The replica distribution should be the same regardless of
	// avoidDisruption and keepUnschedulableReplicas.

	t.Helper()
	doCheck(t, tc, false, false, expected)
	doCheck(t, tc, false, true, expected)
	doCheck(t, tc, true, false, expected)
	doCheck(t, tc, true, true, expected)
}

// some results may be hash dependent

func TestWithoutExisting(t *testing.T) {
	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 16, "B": 17, "C": 17}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
		},
		&expectedResult{plan: map[string]int64{"A": 25, "B": 25}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 1,
			clusters: []string{"A", "B"},
		},
		&expectedResult{plan: map[string]int64{"A": 0, "B": 1}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 1,
			clusters: []string{"A", "B", "C", "D"},
		},
		&expectedResult{plan: map[string]int64{"A": 0, "B": 0, "C": 0, "D": 1}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 1,
			clusters: []string{"A"},
		},
		&expectedResult{plan: map[string]int64{"A": 1}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 1,
			clusters: []string{},
		},
		&expectedResult{plan: map[string]int64{}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {MinReplicas: 2, Weight: 0},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 2, "B": 2, "C": 2}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {MinReplicas: 20, Weight: 0},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 20, "C": 20}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {MinReplicas: 20, Weight: 0},
				"A": {MinReplicas: 100, Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 50, "B": 0, "C": 0}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {MinReplicas: 10, Weight: 1},
				"B": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
		},
		&expectedResult{plan: map[string]int64{"A": 30, "B": 20}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {MinReplicas: 3, Weight: 2},
				"B": {MinReplicas: 3, Weight: 3},
				"C": {MinReplicas: 3, Weight: 5},
			},
			replicas: 10,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 3, "B": 3, "C": 4}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {MinReplicas: 10, Weight: 1, MaxReplicas: pointer.Int64(12)},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 12, "B": 12, "C": 12}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1, MaxReplicas: pointer.Int64(2)},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 2, "B": 2, "C": 2}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 0, MaxReplicas: pointer.Int64(2)},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 0, "B": 0, "C": 0}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 1},
				"B": {Weight: 2},
			},
			replicas: 60,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 20, "B": 40}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000},
				"B": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 50, "B": 0}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000},
				"B": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"B", "C"},
		},
		&expectedResult{plan: map[string]int64{"B": 50}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000, MaxReplicas: pointer.Int64(10)},
				"B": {Weight: 1},
				"C": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 20, "C": 20}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000, MaxReplicas: pointer.Int64(10)},
				"B": {Weight: 1},
				"C": {Weight: 1, MaxReplicas: pointer.Int64(10)},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 30, "C": 10}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000, MaxReplicas: pointer.Int64(10)},
				"B": {Weight: 1},
				"C": {Weight: 1, MaxReplicas: pointer.Int64(21)},
				"D": {Weight: 1, MaxReplicas: pointer.Int64(10)},
			},
			replicas: 71,
			clusters: []string{"A", "B", "C", "D"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 30, "C": 21, "D": 10}},
	)

	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000, MaxReplicas: pointer.Int64(10)},
				"B": {Weight: 1},
				"C": {Weight: 1, MaxReplicas: pointer.Int64(21)},
				"D": {Weight: 1, MaxReplicas: pointer.Int64(10)},
				"E": {Weight: 1},
			},
			replicas: 91,
			clusters: []string{"A", "B", "C", "D", "E"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 25, "C": 21, "D": 10, "E": 25}},
	)
}

func doCheckWithExisting(
	t *testing.T,
	tc *testCase,
	expected [2]*expectedResult,
) {
	// With existing, avoidDisruption should affect the distribution

	t.Helper()
	doCheck(t, tc, false, false, expected[0])
	doCheck(t, tc, false, true, expected[0])
	doCheck(t, tc, true, false, expected[1])
	doCheck(t, tc, true, true, expected[1])
}

func TestWithExisting(t *testing.T) {
	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"C": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 16, "B": 17, "C": 17}},
			{plan: map[string]int64{"A": 9, "B": 11, "C": 30}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 25, "B": 25}},
			{plan: map[string]int64{"A": 30, "B": 20}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 0, "B": 8},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8}},
			{plan: map[string]int64{"A": 7, "B": 8}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 1, "B": 8},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8}},
			{plan: map[string]int64{"A": 7, "B": 8}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 4, "B": 8},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8}},
			{plan: map[string]int64{"A": 7, "B": 8}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 7, "B": 8},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8}},
			{plan: map[string]int64{"A": 7, "B": 8}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 15, "B": 0},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8}},
			{plan: map[string]int64{"A": 15, "B": 0}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 5, "B": 10},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8}},
			{plan: map[string]int64{"A": 5, "B": 10}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 25, "B": 25}},
			{plan: map[string]int64{"A": 30, "B": 20}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 10},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 25, "B": 25}},
			{plan: map[string]int64{"A": 25, "B": 25}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 10, "B": 20},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 25, "B": 25}},
			{plan: map[string]int64{"A": 25, "B": 25}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 10, "B": 70},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 25, "B": 25}},
			{plan: map[string]int64{"A": 10, "B": 40}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 1,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 0, "B": 1}},
			{plan: map[string]int64{"A": 1, "B": 0}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 10,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 50, "B": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 5, "B": 5}},
			{plan: map[string]int64{"A": 5, "B": 5}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 499},
				"B": {Weight: 499},
				"C": {Weight: 1},
			},
			replicas: 15,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 15, "B": 15, "C": 0},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 7, "B": 8, "C": 0}},
			{plan: map[string]int64{"A": 7, "B": 8, "C": 0}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 18,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 10, "B": 1, "C": 1},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 6, "B": 6, "C": 6}},
			{plan: map[string]int64{"A": 10, "B": 4, "C": 4}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 0},
				"B": {Weight: 1},
				"C": {Weight: 1},
			},
			replicas: 18,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 10, "B": 1, "C": 7},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 0, "B": 9, "C": 9}},
			{plan: map[string]int64{"A": 10, "B": 1, "C": 7}},
		},
	)
}

func doCheckWithExistingAndCapacity(
	t *testing.T,
	tc *testCase,
	expected [4]*expectedResult,
) {
	// With existing, both avoidDisruption should affect the distribution

	t.Helper()
	doCheck(t, tc, false, false, expected[0])
	doCheck(t, tc, false, true, expected[1])
	doCheck(t, tc, true, false, expected[2])
	doCheck(t, tc, true, true, expected[3])
}

func TestWithExistingAndCapacity(t *testing.T) {
	// With existing and capacity, both avoidDisruption and keepUnschedulableReplicas
	// should affect the distribution

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 30, "B": 20},
			capacity: map[string]int64{"C": 10},
		},
		// A:16, B:17, C:17 initially, then migrate 7 in C after unschedulable
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 20, "B": 20, "C": 10},
				overflow: map[string]int64{"C": 7},
			},
			{
				plan:     map[string]int64{"A": 20, "B": 20, "C": 10},
				overflow: map[string]int64{"C": 7},
			},
			{
				plan: map[string]int64{"A": 30, "B": 20, "C": 0},
			},
			{
				plan: map[string]int64{"A": 30, "B": 20, "C": 0},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 30, "C": 20},
			capacity: map[string]int64{"C": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 20, "B": 20, "C": 10},
				overflow: map[string]int64{"C": 7},
			},
			{
				plan:     map[string]int64{"A": 20, "B": 20, "C": 10},
				overflow: map[string]int64{"C": 7},
			},
			{
				plan: map[string]int64{"A": 30, "B": 10, "C": 10},
			},
			{
				plan:     map[string]int64{"A": 30, "B": 10, "C": 10},
				overflow: map[string]int64{"C": 7},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000},
				"B": {Weight: 1},
			},
			replicas: 50,
			clusters: []string{"B", "C"},
			existing: map[string]int64{"B": 50},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"B": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"B": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"B": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"B": 10},
				overflow: map[string]int64{"B": 40},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 1},
				"B": {Weight: 5},
			},
			replicas: 60,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 20, "B": 40},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan: map[string]int64{"A": 50, "B": 10},
			},
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 40},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 1},
				"B": {Weight: 2},
			},
			replicas: 60,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 60},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 30},
			},
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 30},
			},
			{
				plan: map[string]int64{"A": 60, "B": 0},
			},
			{
				plan: map[string]int64{"A": 60, "B": 0},
			},
		},
	)

	// total capacity < desired replicas
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 1},
				"B": {Weight: 1},
			},
			replicas: 60,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 30, "B": 30},
			capacity: map[string]int64{"A": 10, "B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 10, "B": 10},
				overflow: map[string]int64{"A": 20, "B": 20},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10},
				overflow: map[string]int64{"A": 20, "B": 20},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10},
				overflow: map[string]int64{"A": 20, "B": 20},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10},
				overflow: map[string]int64{"A": 20, "B": 20},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 1},
				"B": {Weight: 2},
			},
			replicas: 60,
			clusters: []string{"A", "B"},
			existing: map[string]int64{"A": 30, "B": 40},
			capacity: map[string]int64{"A": 25, "B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 25, "B": 10},
				overflow: map[string]int64{"A": 25, "B": 30},
			},
			{
				plan:     map[string]int64{"A": 25, "B": 10},
				overflow: map[string]int64{"A": 25, "B": 30},
			},
			{
				plan:     map[string]int64{"A": 25, "B": 10},
				overflow: map[string]int64{"A": 25, "B": 25},
			},
			{
				plan:     map[string]int64{"A": 25, "B": 10},
				overflow: map[string]int64{"A": 25, "B": 30},
			},
		},
	)

	// map[string]int64{"A": 10, "B": 30, "C": 21, "D": 10})
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {Weight: 10000, MaxReplicas: pointer.Int64(10)},
				"B": {Weight: 1},
				"C": {Weight: 1, MaxReplicas: pointer.Int64(21)},
				"D": {Weight: 1, MaxReplicas: pointer.Int64(10)},
			},
			replicas: 71,
			clusters: []string{"A", "B", "C", "D"},
			existing: map[string]int64{"A": 20},
			capacity: map[string]int64{"C": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 10, "B": 41, "C": 10, "D": 10},
				overflow: map[string]int64{"C": 11},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 41, "C": 10, "D": 10},
				overflow: map[string]int64{"C": 11},
			},
			{
				plan: map[string]int64{"A": 20, "B": 33, "C": 10, "D": 8},
			},
			{
				plan:     map[string]int64{"A": 20, "B": 33, "C": 10, "D": 8},
				overflow: map[string]int64{"C": 11},
			},
		},
	)

	// capacity < minReplicas should also be recorded as overflow to prevent infinite rescheduling loop
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {MinReplicas: 20, Weight: 0},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 24},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 20, "B": 10, "C": 20},
				overflow: map[string]int64{"B": 10},
			},
			{
				plan:     map[string]int64{"A": 20, "B": 10, "C": 20},
				overflow: map[string]int64{"B": 10},
			},
			{
				plan: map[string]int64{"A": 24, "B": 10, "C": 16},
			},
			{
				plan:     map[string]int64{"A": 24, "B": 10, "C": 16},
				overflow: map[string]int64{"B": 10},
			},
		},
	)

	// Actually we'd like 20 overflow, but 25 is also fine.
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"*": {MinReplicas: 20, Weight: 1},
			},
			replicas: 60,
			clusters: []string{"A", "B"},
			existing: map[string]int64{},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 25},
			},
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 25},
			},
			{
				plan: map[string]int64{"A": 50, "B": 10},
			},
			{
				plan:     map[string]int64{"A": 50, "B": 10},
				overflow: map[string]int64{"B": 25},
			},
		},
	)
}
