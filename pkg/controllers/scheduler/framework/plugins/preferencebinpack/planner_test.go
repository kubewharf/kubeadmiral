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

package preferencebinpack

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"
)

func Test_getDesiredPlan(t *testing.T) {
	type args struct {
		preferences       []*namedClusterPreferences
		estimatedCapacity map[string]int64
		limitedCapacity   map[string]int64
		totalReplicas     int64
	}
	tests := []struct {
		name            string
		args            args
		desiredPlan     map[string]int64
		desiredOverflow map[string]int64
	}{
		{
			name: "all pods scheduled to 1 cluster",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: nil,
				limitedCapacity:   nil,
				totalReplicas:     10,
			},
			desiredPlan: map[string]int64{
				"A": 10,
				"B": 0,
				"C": 0,
			},
			desiredOverflow: map[string]int64{},
		},
		{
			name: "1 cluster with estimated capacity",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: map[string]int64{
					"A": 5,
				},
				limitedCapacity: nil,
				totalReplicas:   10,
			},
			desiredPlan: map[string]int64{
				"A": 5,
				"B": 5,
				"C": 0,
			},
			desiredOverflow: map[string]int64{"A": 5},
		},
		{
			name: "2 cluster with estimated capacity",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: map[string]int64{
					"A": 5,
					"B": 3,
				},
				limitedCapacity: nil,
				totalReplicas:   10,
			},
			desiredPlan: map[string]int64{
				"A": 5,
				"B": 3,
				"C": 2,
			},
			desiredOverflow: map[string]int64{
				"A": 5,
				"B": 2,
			},
		},
		{
			name: "all cluster with estimated capacity",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: map[string]int64{
					"A": 5,
					"B": 3,
					"C": 1,
				},
				limitedCapacity: nil,
				totalReplicas:   10,
			},
			desiredPlan: map[string]int64{
				"A": 5,
				"B": 3,
				"C": 1,
			},
			desiredOverflow: map[string]int64{
				"A": 5,
				"B": 2,
				"C": 1,
			},
		},
		{
			name: "1 cluster with limited capacity",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: map[string]int64{"A": 5},
				limitedCapacity: map[string]int64{
					"A": 3,
				},
				totalReplicas: 10,
			},
			desiredPlan: map[string]int64{
				"A": 3,
				"B": 7,
				"C": 0,
			},
			desiredOverflow: map[string]int64{},
		},
		{
			name: "all cluster with limited capacity",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: map[string]int64{"A": 5},
				limitedCapacity: map[string]int64{
					"A": 3,
					"B": 2,
					"C": 1,
				},
				totalReplicas: 10,
			},
			desiredPlan: map[string]int64{
				"A": 3,
				"B": 2,
				"C": 1,
			},
			desiredOverflow: map[string]int64{},
		},
		{
			name: "1 cluster with maxReplicas",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName: "A",
						ClusterPreferences: ClusterPreferences{
							MaxReplicas: pointer.Int64(5),
						},
					},
					{
						clusterName:        "B",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: nil,
				limitedCapacity:   nil,
				totalReplicas:     10,
			},
			desiredPlan: map[string]int64{
				"A": 5,
				"B": 5,
				"C": 0,
			},
			desiredOverflow: map[string]int64{},
		},
		{
			name: "1 cluster with minReplicas",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName: "B",
						ClusterPreferences: ClusterPreferences{
							MinReplicas: 5,
						},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: nil,
				limitedCapacity:   nil,
				totalReplicas:     10,
			},
			desiredPlan: map[string]int64{
				"A": 5,
				"B": 5,
				"C": 0,
			},
			desiredOverflow: map[string]int64{},
		},
		{
			name: "1 cluster with minReplicas and limitCapacity",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName:        "A",
						ClusterPreferences: ClusterPreferences{},
					},
					{
						clusterName: "B",
						ClusterPreferences: ClusterPreferences{
							MinReplicas: 5,
						},
					},
					{
						clusterName:        "C",
						ClusterPreferences: ClusterPreferences{},
					},
				},
				estimatedCapacity: nil,
				limitedCapacity:   map[string]int64{"B": 2},
				totalReplicas:     10,
			},
			desiredPlan: map[string]int64{
				"A": 8,
				"B": 2,
				"C": 0,
			},
			desiredOverflow: map[string]int64{},
		},
		{
			name: "2 clusters with capacity < minReplicas",
			args: args{
				preferences: []*namedClusterPreferences{
					{
						clusterName: "A",
						ClusterPreferences: ClusterPreferences{
							MinReplicas: 2,
						},
					},
					{
						clusterName: "B",
						ClusterPreferences: ClusterPreferences{
							MinReplicas: 2,
						},
					},
					{
						clusterName: "C",
						ClusterPreferences: ClusterPreferences{
							MinReplicas: 2,
						},
					},
				},
				estimatedCapacity: map[string]int64{
					"A": 2,
					"B": 0,
					"C": 0,
				},
				limitedCapacity: nil,
				totalReplicas:   10,
			},
			desiredPlan: map[string]int64{
				"A": 2,
				"B": 0,
				"C": 0,
			},
			desiredOverflow: map[string]int64{
				"A": 8,
				"B": 8,
				"C": 8,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDesiredPlan, gotDesiredOverflow := getDesiredPlan(
				tt.args.preferences,
				tt.args.estimatedCapacity,
				tt.args.limitedCapacity,
				tt.args.totalReplicas,
			)
			if !reflect.DeepEqual(gotDesiredPlan, tt.desiredPlan) {
				t.Errorf("getDesiredPlan() gotDesiredPlan = %v, desiredPlan %v", gotDesiredPlan, tt.desiredPlan)
			}
			if !reflect.DeepEqual(gotDesiredOverflow, tt.desiredOverflow) {
				t.Errorf("getDesiredPlan() gotDesiredOverflow = %v, desiredOverflow %v", gotDesiredOverflow, tt.desiredOverflow)
			}
		})
	}
}

type testCase struct {
	rsp               map[string]ClusterPreferences
	replicas          int64
	clusters          []string
	existing          map[string]int64
	capacity          map[string]int64
	limitedCapacity   map[string]int64
	scheduledReplicas map[string]int64
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
			existing, estimatedCapacity, tc.limitedCapacity,
			avoidDisruption, keepUnschedulableReplicas, tc.scheduledReplicas,
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

func TestWithoutExisting(t *testing.T) {
	// all pods scheduled to 1 cluster
	doCheckWithoutExisting(t,
		&testCase{
			rsp:      map[string]ClusterPreferences{},
			replicas: 50,
			clusters: []string{"A", "B"},
		},
		&expectedResult{plan: map[string]int64{"A": 50, "B": 0}},
	)

	// 1 cluster with minReplicas
	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"B": {MinReplicas: 10},
			},
			replicas: 50,
			clusters: []string{"A", "B"},
		},
		&expectedResult{plan: map[string]int64{"A": 40, "B": 10}},
	)

	// 2 clusters with maxReplicas
	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {MaxReplicas: pointer.Int64(10)},
				"B": {MaxReplicas: pointer.Int64(21)},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 21, "C": 19}},
	)

	// 1 cluster with maxReplicas and 1 cluster with minReplicas
	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {MaxReplicas: pointer.Int64(10)},
				"C": {MinReplicas: 10},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 30, "C": 10}},
	)

	// 1 cluster with maxReplicas, 1 cluster with minReplicas and 1 cluster with limitCapacity
	doCheckWithoutExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {MaxReplicas: pointer.Int64(10)},
				"D": {MinReplicas: 10},
			},
			replicas:        50,
			clusters:        []string{"A", "B", "C", "D"},
			limitedCapacity: map[string]int64{"B": 10},
		},
		&expectedResult{plan: map[string]int64{"A": 10, "B": 10, "C": 20, "D": 10}},
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
			rsp:               map[string]ClusterPreferences{},
			replicas:          50,
			clusters:          []string{"A", "B", "C"},
			existing:          map[string]int64{"C": 30},
			scheduledReplicas: map[string]int64{"C": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 50, "B": 0, "C": 0}},
			{plan: map[string]int64{"A": 20, "B": 0, "C": 30}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          50,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 30},
			limitedCapacity:   map[string]int64{"A": 0},
			scheduledReplicas: map[string]int64{"A": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 0, "B": 50}},
			{plan: map[string]int64{"A": 0, "B": 50}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          50,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 30},
			limitedCapacity:   map[string]int64{"A": 10},
			scheduledReplicas: map[string]int64{"A": 30},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 10, "B": 40}},
			{plan: map[string]int64{"A": 10, "B": 40}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          15,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 0, "B": 8},
			scheduledReplicas: map[string]int64{"B": 8},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 15, "B": 0}},
			{plan: map[string]int64{"A": 7, "B": 8}},
		},
	)

	doCheckWithExisting(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          15,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 15, "B": 8},
			scheduledReplicas: map[string]int64{"A": 15, "B": 8},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 15, "B": 0}},
			{plan: map[string]int64{"A": 15, "B": 0}},
		},
	)

	// add maxReplicas for existing replicas
	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {
					MaxReplicas: pointer.Int64(10),
				},
			},
			replicas:          15,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 15, "B": 0},
			scheduledReplicas: map[string]int64{"A": 15, "B": 0},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 10, "B": 5}},
			{plan: map[string]int64{"A": 15, "B": 0}},
		},
	)

	// add minReplicas for existing replicas
	doCheckWithExisting(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"B": {
					MinReplicas: 10,
				},
			},
			replicas:          15,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 15, "B": 0},
			scheduledReplicas: map[string]int64{"A": 15, "B": 0},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 5, "B": 10}},
			{plan: map[string]int64{"A": 15, "B": 0}},
		},
	)

	// add limitCapacity for existing replicas
	doCheckWithExisting(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          15,
			clusters:          []string{"A", "B"},
			existing:          map[string]int64{"A": 15, "B": 0},
			scheduledReplicas: map[string]int64{"A": 15, "B": 0},
			limitedCapacity:   map[string]int64{"A": 5},
		},
		[2]*expectedResult{
			{plan: map[string]int64{"A": 5, "B": 10}},
			{plan: map[string]int64{"A": 5, "B": 10}},
		},
	)
}

func doCheckWithExistingAndCapacity(
	t *testing.T,
	tc *testCase,
	expected [4]*expectedResult,
) {
	// With existing and capacity, both avoidDisruption and keepUnschedulableReplicas
	// should affect the distribution

	t.Helper()
	doCheck(t, tc, false, false, expected[0])
	doCheck(t, tc, false, true, expected[1])
	doCheck(t, tc, true, false, expected[2])
	doCheck(t, tc, true, true, expected[3])
}

func TestWithExistingAndCapacity(t *testing.T) {
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          50,
			clusters:          []string{"A", "B", "C"},
			existing:          map[string]int64{"A": 50, "B": 20},
			capacity:          map[string]int64{"A": 30, "B": 10},
			scheduledReplicas: map[string]int64{"A": 30, "B": 10},
		},

		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 30, "B": 10, "C": 10},
				overflow: map[string]int64{"A": 20, "B": 10},
			},
			{
				plan:     map[string]int64{"A": 30, "B": 10, "C": 10},
				overflow: map[string]int64{"A": 20, "B": 10},
			},
			{
				plan:     map[string]int64{"A": 30, "B": 10, "C": 10},
				overflow: map[string]int64{"A": 10, "B": 10},
			},
			{
				plan:     map[string]int64{"A": 30, "B": 10, "C": 10},
				overflow: map[string]int64{"A": 20, "B": 10},
			},
		},
	)

	// scale up
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          50,
			clusters:          []string{"A", "B", "C"},
			existing:          map[string]int64{"A": 30, "C": 20},
			capacity:          map[string]int64{"C": 10},
			scheduledReplicas: map[string]int64{"A": 30, "C": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 50, "B": 0, "C": 0},
				overflow: map[string]int64{},
			},
			{
				plan:     map[string]int64{"A": 50, "B": 0, "C": 0},
				overflow: map[string]int64{},
			},
			{
				plan: map[string]int64{"A": 40, "B": 0, "C": 10},
			},
			{
				plan: map[string]int64{"A": 40, "B": 0, "C": 10},
			},
		},
	)

	// scale down
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp:               map[string]ClusterPreferences{},
			replicas:          60,
			clusters:          []string{"A", "B", "C"},
			existing:          map[string]int64{"A": 60, "B": 40},
			capacity:          map[string]int64{"A": 40},
			scheduledReplicas: map[string]int64{"A": 40},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 40, "B": 20, "C": 0},
				overflow: map[string]int64{"A": 20},
			},
			{
				plan:     map[string]int64{"A": 40, "B": 20, "C": 0},
				overflow: map[string]int64{"A": 20},
			},
			{
				plan:     map[string]int64{"A": 40, "B": 20, "C": 0},
				overflow: map[string]int64{"A": 20},
			},
			{
				plan:     map[string]int64{"A": 40, "B": 20, "C": 0},
				overflow: map[string]int64{"A": 20},
			},
		},
	)

	// total capacity < desired replicas
	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp:      map[string]ClusterPreferences{},
			replicas: 60,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"A": 60, "B": 50, "C": 40},
			capacity: map[string]int64{"A": 10, "B": 10, "C": 20},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 10, "B": 10, "C": 20},
				overflow: map[string]int64{"A": 50, "B": 40, "C": 20},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10, "C": 20},
				overflow: map[string]int64{"A": 50, "B": 40, "C": 20},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10, "C": 20},
				overflow: map[string]int64{"A": 50, "B": 40, "C": 20},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10, "C": 20},
				overflow: map[string]int64{"A": 50, "B": 40, "C": 20},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"A": {MaxReplicas: pointer.Int64(10)},
				"C": {MaxReplicas: pointer.Int64(21)},
				"D": {MaxReplicas: pointer.Int64(10)},
			},
			replicas: 60,
			clusters: []string{"A", "B", "C", "D"},
			existing: map[string]int64{"A": 20, "B": 40},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 10, "B": 10, "C": 21, "D": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"A": 10, "B": 10, "C": 21, "D": 10},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"A": 20, "B": 10, "C": 21, "D": 9},
				overflow: map[string]int64{"B": 40},
			},
			{
				plan:     map[string]int64{"A": 20, "B": 10, "C": 21, "D": 9},
				overflow: map[string]int64{"B": 40},
			},
		},
	)

	doCheckWithExistingAndCapacity(t,
		&testCase{
			rsp: map[string]ClusterPreferences{
				"B": {MinReplicas: 20},
			},
			replicas: 50,
			clusters: []string{"A", "B", "C"},
			existing: map[string]int64{"C": 24},
			capacity: map[string]int64{"B": 10},
		},
		[4]*expectedResult{
			{
				plan:     map[string]int64{"A": 40, "B": 10, "C": 0},
				overflow: map[string]int64{"B": 10},
			},
			{
				plan:     map[string]int64{"A": 40, "B": 10, "C": 0},
				overflow: map[string]int64{"B": 10},
			},
			{
				plan:     map[string]int64{"A": 16, "B": 10, "C": 24},
				overflow: map[string]int64{"B": 10},
			},
			{
				plan:     map[string]int64{"A": 16, "B": 10, "C": 24},
				overflow: map[string]int64{"B": 10},
			},
		},
	)
}
