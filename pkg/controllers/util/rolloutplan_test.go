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

package util

import (
	"reflect"
	"testing"
)

type PlanTestSuit struct {
	Name           string
	Targets        Targets
	Plans          RolloutPlans
	MaxSurge       int32
	MaxUnavailable int32
	Replicas       int32
}

func ptr(i int32) *int32 {
	return &i
}

func newTarget(
	name string,
	updated bool,
	replicas,
	desiredReplicas,
	updatedReplicas,
	updatedAvailableReplicas,
	ms,
	mu int32,
) *TargetInfo {
	currentNewReplicas := updatedReplicas
	if !updated {
		currentNewReplicas = replicas
	}
	currentNewAvailableReplicas := currentNewReplicas
	return &TargetInfo{
		name,
		TargetStatus{
			replicas,
			replicas,
			replicas,
			updatedReplicas,
			updatedAvailableReplicas,
			currentNewReplicas,
			currentNewAvailableReplicas,
			updated,
			ms,
			mu,
		},
		desiredReplicas,
	}
}

func newTargetWithActualInfo(
	name string,
	updated bool,
	replicas,
	desiredReplicas,
	updatedReplicas,
	updatedAvailableReplicas,
	actualReplicas,
	availableReplicas,
	ms,
	mu int32,
) *TargetInfo {
	currentNewReplicas := updatedReplicas
	if !updated {
		currentNewReplicas = replicas
	}
	currentNewAvailableReplicas := currentNewReplicas
	return &TargetInfo{
		name,
		TargetStatus{
			replicas,
			actualReplicas,
			availableReplicas,
			updatedReplicas,
			updatedAvailableReplicas,
			currentNewReplicas,
			currentNewAvailableReplicas,
			updated,
			ms,
			mu,
		},
		desiredReplicas,
	}
}

func newTargetWithAllInfo(
	name string,
	updated bool,
	replicas,
	desiredReplicas,
	updatedReplicas,
	updatedAvailableReplicas,
	currentNewReplicas,
	currentNewAvailableReplicas,
	actualReplicas,
	availableReplicas,
	ms,
	mu int32,
) *TargetInfo {
	return &TargetInfo{
		name,
		TargetStatus{
			replicas,
			actualReplicas,
			availableReplicas,
			updatedReplicas,
			updatedAvailableReplicas,
			currentNewReplicas,
			currentNewAvailableReplicas,
			updated,
			ms,
			mu,
		},
		desiredReplicas,
	}
}

func TestPlanWholeProcessWithMaxUnavailable(t *testing.T) {
	var replicas, maxSurge, maxUnavailable int32 = 45, 0, 10
	var tests []PlanTestSuit
	s := PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 1"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", false, 10, 10, 0, 0, 0, 0),
		newTarget("c", false, 0, 5, 0, 0, 0, 0),
		newTarget("a", false, 5, 15, 0, 0, 0, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), ptr(0), ptr(5), false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(15), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 2"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", false, 10, 10, 0, 0, 0, 0),
		newTarget("c", true, 0, 5, 0, 0, 0, 10),
		newTarget("a", true, 5, 15, 5, 5, 0, 5),
		newTarget("b", false, 15, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(10), nil, nil, true},
		"d": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 3"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", true, 10, 10, 5, 5, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 0, 10),
		newTarget("a", true, 5, 15, 5, 5, 0, 10),
		newTarget("b", false, 10, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"d": {nil, ptr(5), ptr(0), false},
		"b": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 4"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", true, 10, 10, 10, 9, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 0, 10),
		newTarget("a", true, 5, 15, 5, 5, 0, 10),
		newTarget("b", true, 10, 10, 5, 5, 5, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {nil, ptr(5), ptr(0), false},
		"d": {nil, ptr(1), ptr(0), false},
		"f": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 5"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 3, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 0, 10),
		newTarget("c", true, 0, 5, 0, 0, 0, 10),
		newTarget("a", true, 5, 15, 5, 5, 0, 10),
		newTarget("b", true, 10, 10, 10, 8, 5, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(15), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {nil, ptr(1), ptr(0), false},
		"d": {nil, nil, nil, false},
		"f": {nil, ptr(1), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 6"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 0, 10),
		newTarget("d", true, 10, 10, 10, 10, 0, 10),
		newTarget("c", true, 0, 5, 0, 0, 0, 10),
		newTarget("a", true, 15, 15, 15, 12, 0, 10),
		newTarget("b", true, 10, 10, 10, 10, 0, 10),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(0), nil, nil, false},
		"e": {ptr(0), ptr(0), ptr(2), false},
		"a": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 7"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 0, 10),
		newTarget("d", true, 10, 10, 10, 10, 0, 10),
		newTarget("c", true, 0, 5, 0, 0, 0, 10),
		newTarget("a", true, 15, 15, 15, 14, 0, 1),
		newTarget("b", true, 10, 10, 10, 10, 0, 10),
	}
	s.Plans = RolloutPlans{
		"c": {nil, nil, nil, false},
		"a": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 8"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 0, 10),
		newTarget("d", true, 10, 10, 10, 10, 0, 10),
		newTarget("c", true, 5, 5, 5, 5, 0, 10),
		newTarget("a", true, 15, 15, 15, 15, 0, 1),
		newTarget("b", true, 10, 10, 10, 10, 0, 10),
	}
	s.Plans = RolloutPlans{
		"a": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: test.MaxSurge, MaxUnavailable: test.MaxUnavailable, Replicas: test.Replicas}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanWholeProcessWithBoth(t *testing.T) {
	var replicas, maxSurge, maxUnavailable int32 = 45, 5, 10
	var tests []PlanTestSuit

	s := PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 1"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", false, 10, 10, 0, 0, 0, 0),
		newTarget("c", false, 0, 5, 0, 0, 0, 0),
		newTarget("a", false, 5, 15, 0, 0, 0, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), ptr(5), ptr(5), false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(15), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 2"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", false, 10, 10, 0, 0, 0, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 10),
		newTarget("a", true, 5, 15, 5, 5, 5, 5),
		newTarget("b", false, 15, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(10), nil, nil, true},
		"d": {nil, ptr(10), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 3"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", true, 10, 10, 10, 8, 10, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 10),
		newTarget("a", true, 5, 15, 5, 5, 5, 10),
		newTarget("b", false, 10, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"d": {nil, ptr(1), ptr(0), false},
		"b": {nil, ptr(10), ptr(0), false},
		"f": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 4"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 4, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 10),
		newTarget("c", true, 0, 5, 0, 0, 5, 10),
		newTarget("a", true, 5, 15, 5, 5, 5, 10),
		newTarget("b", true, 10, 10, 10, 8, 10, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(15), nil, nil, false},
		"c": {ptr(5), nil, nil, false},
		"b": {nil, ptr(1), ptr(0), false},
		"d": {nil, nil, nil, false},
		"f": {nil, ptr(1), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 5"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 10),
		newTarget("d", true, 10, 10, 10, 10, 5, 10),
		newTarget("c", true, 5, 5, 5, 2, 5, 10),
		newTarget("a", true, 15, 15, 15, 15, 5, 10),
		newTarget("b", true, 10, 10, 10, 10, 5, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"e": {ptr(0), ptr(0), ptr(5), false},
		"a": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 6"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 10),
		newTarget("d", true, 10, 10, 10, 10, 5, 10),
		newTarget("c", true, 5, 5, 5, 4, 5, 10),
		newTarget("a", true, 15, 15, 15, 15, 5, 10),
		newTarget("b", true, 10, 10, 10, 10, 5, 10),
	}
	s.Plans = RolloutPlans{
		"a": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable, Replicas: 45}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanWholeProcessWithSurge(t *testing.T) {
	var replicas, maxSurge, maxUnavailable int32 = 45, 5, 0
	var tests []PlanTestSuit

	s := PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 1"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", false, 10, 10, 0, 0, 0, 0),
		newTarget("c", false, 0, 5, 0, 0, 0, 0),
		newTarget("a", false, 5, 15, 0, 0, 0, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 2"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", false, 10, 10, 0, 0, 0, 0),
		newTarget("c", false, 0, 5, 0, 0, 0, 0),
		newTarget("a", true, 5, 15, 5, 4, 5, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"d": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 3"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", true, 10, 10, 5, 4, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 5, 15, 5, 5, 5, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"d": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 4"
	s.Targets = Targets{
		newTarget("f", false, 5, 5, 0, 0, 0, 0),
		newTarget("d", true, 10, 10, 10, 7, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 5, 15, 5, 5, 5, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(5), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, ptr(5), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 5"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 2, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 5, 15, 5, 5, 5, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(10), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 6"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 10, 15, 10, 8, 5, 0),
		newTarget("b", false, 20, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(10), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(17), nil, nil, true},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 7"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 10, 15, 10, 10, 0, 1),
		newTarget("b", false, 17, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(13), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(15), nil, nil, true},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 8"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 13, 15, 13, 13, 5, 0),
		newTarget("b", false, 15, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(15), nil, nil, false},
		"c": {ptr(0), nil, nil, false},
		"b": {ptr(12), nil, nil, true},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 9"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 0, 5, 0, 0, 5, 0),
		newTarget("a", true, 15, 15, 15, 15, 5, 0),
		newTarget("b", false, 12, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(3), nil, nil, false},
		"b": {ptr(10), nil, nil, true},
		"a": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 10"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 3, 5, 3, 3, 5, 0),
		newTarget("a", true, 15, 15, 15, 15, 5, 0),
		newTarget("b", false, 10, 10, 0, 0, 0, 0),
		newTarget("e", false, 5, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(3), nil, nil, false},
		"e": {ptr(2), nil, nil, true},
		"a": {nil, nil, nil, false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
		"b": {nil, ptr(2), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 11"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 3, 5, 3, 3, 5, 0),
		newTarget("a", true, 15, 15, 15, 15, 5, 0),
		newTarget("b", true, 10, 10, 2, 0, 2, 0),
		newTarget("e", false, 2, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(3), nil, nil, false},
		"a": {nil, nil, nil, false},
		"b": {nil, ptr(5), ptr(0), false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 12"
	s.Targets = Targets{
		newTarget("f", true, 5, 5, 5, 5, 5, 0),
		newTarget("d", true, 10, 10, 10, 10, 5, 0),
		newTarget("c", true, 3, 5, 3, 3, 5, 0),
		newTarget("a", true, 15, 15, 15, 15, 5, 0),
		newTarget("b", true, 10, 10, 7, 7, 5, 0),
		newTarget("e", false, 2, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(5), nil, nil, false},
		"a": {nil, nil, nil, false},
		"b": {nil, ptr(3), ptr(0), false},
		"d": {nil, nil, nil, false},
		"f": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable, Replicas: 45}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanCreation(t *testing.T) {
	var replicas, maxSurge, maxUnavailable int32 = 15, 10, 20
	var tests []PlanTestSuit

	s := PlanTestSuit{Replicas: replicas, MaxSurge: maxSurge, MaxUnavailable: maxUnavailable}
	s.Name = "round 1"
	s.Targets = Targets{
		newTarget("b", false, 0, 5, 0, 0, 0, 0),
		newTarget("a", false, 0, 10, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {Replicas: nil},
		"b": {Replicas: nil},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: test.MaxSurge, MaxUnavailable: test.MaxUnavailable}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanScale(t *testing.T) {
	var tests []PlanTestSuit

	s := PlanTestSuit{Replicas: 4, MaxSurge: 0, MaxUnavailable: 1}
	s.Name = "scale out with creation"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 4, 4, 4, 4, 4, 4, 0, 1),
		newTargetWithActualInfo("c", true, 0, 2, 0, 0, 0, 0, 0, 1),
		newTargetWithActualInfo("k", true, 0, 1, 0, 0, 0, 0, 0, 0),
	}
	s.Plans = RolloutPlans{
		"c": {nil, nil, nil, false},
		"k": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: test.MaxSurge, MaxUnavailable: test.MaxUnavailable, Replicas: test.Replicas}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanEmptyTargets(t *testing.T) {
	var tests []PlanTestSuit

	s := PlanTestSuit{}
	s.Name = "empty targets"
	s.Plans = RolloutPlans{}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{MaxSurge: 0, MaxUnavailable: 25, Replicas: 100}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}

	s = PlanTestSuit{}
	s.Name = "empty maxSurge & maxUnavailable"
	s.Plans = RolloutPlans{}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{MaxSurge: 0, MaxUnavailable: 0}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanUnexceptedCases(t *testing.T) {
	var tests []PlanTestSuit

	s := PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "too many unavailable"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 200, 200, 0, 0, 200, 200, 0, 1),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 300, 100, 0, 1),
	}
	s.Plans = RolloutPlans{
		"b": {nil, ptr(0), ptr(1), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 700, MaxSurge: 0, MaxUnavailable: 35}
	s.Name = "too many unavailable during updating"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 400, 100, 4, 4, 400, 360, 0, 35),
		newTargetWithActualInfo("c", false, 300, 600, 0, 0, 300, 300, 0, 15),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(400), ptr(0), ptr(35), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 600, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "too many unavailable due to cluster not ready"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 200, 200, 0, 0, 200, 200, 0, 1),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 300, 300, 0, 1),
	}
	s.Plans = RolloutPlans{}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "a few unavailable due to cluster not ready"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 200, 200, 0, 0, 200, 200, 0, 1),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 300, 300, 0, 1),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(10), ptr(15), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "missing too many replicas"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 200, 200, 0, 0, 200, 200, 0, 1),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 200, 200, 0, 1),
	}
	s.Plans = RolloutPlans{
		"b": {nil, ptr(0), ptr(1), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "missing a few replicas"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 200, 200, 0, 0, 199, 195, 0, 1),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 298, 295, 0, 1),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(0), ptr(20), false},
		"b": {nil, ptr(0), ptr(5), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "too many replicas actually"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 200, 200, 0, 0, 300, 250, 0, 1),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 300, 300, 0, 1),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 700, MaxSurge: 0, MaxUnavailable: 35}
	s.Name = "too many replicas actually while available still less than spec replicas"
	s.Targets = Targets{
		newTargetWithActualInfo("a", false, 594, 300, 0, 0, 598, 598, 0, 1),
		newTargetWithActualInfo("b", true, 102, 400, 100, 71, 100, 71, 0, 5),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(102), ptr(0), ptr(31), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "unavailable in status lags during updating"
	s.Targets = Targets{
		newTargetWithActualInfo("a", true, 200, 200, 0, 0, 200, 200, 0, 25),
		newTargetWithActualInfo("b", false, 300, 300, 0, 0, 300, 300, 0, 10),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	// todo: it would be better if we can scale in "a" during it's updating
	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "unavailable in status lags during updating 1"
	s.Targets = Targets{
		newTargetWithActualInfo("a", true, 200, 100, 0, 0, 200, 200, 0, 25),
		newTargetWithActualInfo("b", false, 300, 400, 0, 0, 300, 300, 0, 10),
	}
	s.Plans = RolloutPlans{
		"a": {ptr(200), ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 50}
	s.Name = "unavailable in status lags during updating 2"
	s.Targets = Targets{
		newTargetWithAllInfo("a", true, 200, 100, 0, 0, 0, 0, 200, 200, 0, 25),
		newTargetWithActualInfo("b", false, 300, 400, 0, 0, 300, 300, 0, 10),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(300), ptr(0), ptr(25), false},
		"a": {ptr(200), ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 50}
	s.Name = "unavailable in status lags during updating 3"
	s.Targets = Targets{
		newTargetWithActualInfo("a", true, 175, 100, 25, 25, 175, 174, 0, 1),
		newTargetWithActualInfo("b", true, 300, 400, 25, 25, 300, 300, 0, 25),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(300), ptr(25), ptr(24), false},
		"a": {ptr(175), ptr(0), ptr(1), false},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: test.MaxSurge, MaxUnavailable: test.MaxUnavailable, Replicas: test.Replicas}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}

func TestPlanActualCases(t *testing.T) {
	var tests []PlanTestSuit

	s := PlanTestSuit{Replicas: 420, MaxSurge: 0, MaxUnavailable: 21}
	s.Name = "I0721 13:28:24.347552"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 120, 120, 120, 120, 120, 120, 0, 6),
		newTargetWithActualInfo("c", true, 380, 300, 380, 292, 380, 292, 0, 19),
	}
	s.Plans = RolloutPlans{
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 13:33:21.582337"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 120, 220, 120, 120, 120, 120, 0, 6),
		newTargetWithActualInfo("c", true, 290, 290, 290, 290, 290, 290, 0, 14),
	}
	s.Plans = RolloutPlans{
		"c": {nil, nil, nil, false},
		"b": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 13:33:26.799923"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 220, 220, 120, 220, 120, 0, 11),
		newTargetWithActualInfo("c", true, 290, 290, 290, 290, 290, 290, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:08:04.842031"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 220, 220, 0, 0, 220, 220, 0, 11),
		newTargetWithActualInfo("c", false, 290, 290, 0, 0, 290, 290, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {nil, ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:08:20.459339"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 220, 43, 22, 216, 195, 0, 25),
		newTargetWithActualInfo("c", false, 290, 290, 0, 0, 290, 290, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {nil, ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:09:42.009571"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 220, 202, 182, 216, 196, 0, 25),
		newTargetWithActualInfo("c", false, 290, 290, 0, 0, 290, 290, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {nil, ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:09:52.491311"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 220, 220, 203, 220, 202, 0, 25),
		newTargetWithActualInfo("c", false, 290, 290, 0, 0, 290, 290, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {nil, nil, nil, false},
		"c": {nil, ptr(0), ptr(7), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:10:02.860697"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 220, 220, 220, 220, 220, 0, 25),
		newTargetWithActualInfo("c", true, 290, 290, 16, 0, 290, 274, 0, 25),
	}
	s.Plans = RolloutPlans{
		"b": {nil, nil, nil, false},
		"c": {nil, ptr(0), ptr(25), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:27:19.667982"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 220, 320, 0, 0, 220, 220, 0, 11),
		newTargetWithActualInfo("c", false, 290, 190, 0, 0, 290, 281, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), ptr(0), ptr(16), false},
		"c": {ptr(281), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:27:25.862175"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 320, 16, 5, 220, 205, 0, 16),
		newTargetWithActualInfo("c", false, 290, 190, 0, 0, 290, 281, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), ptr(0), ptr(16), false},
		"c": {ptr(281), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:28:26.178728"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 320, 121, 110, 218, 204, 0, 16),
		newTargetWithAllInfo("c", false, 290, 190, 0, 0, 290, 282, 290, 282, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), ptr(0), ptr(17), false},
		"c": {ptr(282), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:29:32.612121"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 320, 220, 217, 220, 217, 0, 18),
		newTargetWithActualInfo("c", false, 290, 190, 0, 0, 290, 283, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), nil, nil, false},
		"c": {ptr(268), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:29:43.095244"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 320, 220, 220, 220, 220, 0, 11),
		newTargetWithActualInfo("c", false, 268, 190, 0, 0, 268, 268, 0, 13),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(242), nil, nil, false},
		"c": {ptr(265), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:29:53.385275"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 242, 320, 242, 242, 242, 242, 0, 12),
		newTargetWithActualInfo("c", false, 263, 190, 0, 0, 263, 263, 0, 13),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(247), nil, nil, false},
		"c": {ptr(243), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:29:53.799261"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 247, 320, 242, 242, 242, 242, 0, 12),
		newTargetWithActualInfo("c", false, 243, 190, 0, 0, 243, 243, 0, 12),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(262), ptr(5), ptr(5), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:30:35.912725"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 297, 320, 297, 297, 297, 297, 0, 14),
		newTargetWithActualInfo("c", false, 203, 190, 0, 0, 203, 203, 0, 10),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(307), nil, nil, false},
		"c": {ptr(190), ptr(0), ptr(2), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:30:36.162431"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 307, 320, 297, 297, 297, 297, 0, 15),
		newTargetWithAllInfo("c", true, 190, 190, 0, 0, 203, 203, 203, 203, 0, 2),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(307), ptr(0), ptr(10), false},
		"c": {nil, ptr(13), ptr(2), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:30:46.793575"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 307, 320, 307, 307, 307, 307, 0, 15),
		newTargetWithActualInfo("c", true, 190, 190, 19, 4, 203, 188, 13, 12),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(307), nil, nil, false},
		"c": {nil, ptr(13), ptr(12), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 14:33:31.262478"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 307, 320, 307, 307, 307, 307, 0, 15),
		newTargetWithActualInfo("c", true, 190, 190, 190, 173, 195, 178, 13, 12),
	}
	s.Plans = RolloutPlans{
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	// the result plan is judged as invalid for now so an empty plan will be returned.
	// remove this test case if the validation rule changed. Refer to the comments in validatePlans for more information
	s = PlanTestSuit{Replicas: 610, MaxSurge: 0, MaxUnavailable: 30}
	s.Name = "I0721 15:36:50.064432"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 320, 320, 0, 0, 320, 320, 0, 16),
		newTargetWithActualInfo("c", false, 190, 290, 0, 0, 190, 190, 0, 9),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(190), ptr(100), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 16:01:56.842312"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 310, 320, 0, 0, 310, 310, 0, 15),
		newTargetWithActualInfo("c", true, 190, 190, 190, 182, 190, 182, 0, 9),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(310), ptr(10), ptr(7), false},
		"c": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 255, MaxSurge: 0, MaxUnavailable: 2}
	s.Name = "I0721 11:10:26.009785"
	s.Targets = Targets{
		newTargetWithActualInfo("a", true, 99, 99, 4, 2, 101, 99, 0, 2),
		newTargetWithActualInfo("b", true, 16, 16, 16, 16, 16, 16, 0, 1),
		newTargetWithActualInfo("c", false, 40, 40, 0, 0, 40, 40, 0, 0),
		newTargetWithActualInfo("d", false, 29, 29, 0, 0, 29, 29, 0, 0),
		newTargetWithActualInfo("e", false, 30, 30, 0, 0, 30, 30, 0, 0),
		newTargetWithActualInfo("f", false, 16, 16, 0, 0, 16, 16, 0, 0),
		newTargetWithActualInfo("g", false, 10, 10, 0, 0, 10, 10, 0, 0),
		newTargetWithActualInfo("h", false, 15, 15, 0, 0, 15, 15, 0, 0),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(0), ptr(2), false},
		"b": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 16:45:31.681757"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 320, 220, 320, 320, 320, 320, 0, 16),
		newTargetWithActualInfo("c", true, 190, 290, 190, 190, 190, 190, 0, 9),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(190), nil, nil, false},
		"b": {ptr(295), nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 16:45:36.682072"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 295, 220, 295, 295, 295, 295, 0, 14),
		newTargetWithActualInfo("c", true, 190, 290, 190, 190, 190, 190, 0, 9),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(215), nil, nil, false},
		"b": {ptr(295), nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 16:46:50.975936"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 220, 243, 243, 243, 243, 0, 11),
		newTargetWithActualInfo("c", true, 267, 290, 267, 267, 267, 267, 0, 13),
	}
	s.Plans = RolloutPlans{
		"b": {nil, nil, nil, false},
		"c": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 17:02:19.762340"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 220, 320, 0, 0, 220, 220, 0, 11),
		newTargetWithActualInfo("c", false, 290, 190, 0, 0, 290, 275, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), ptr(0), ptr(10), false},
		"c": {ptr(275), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 17:04:36.844995"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 320, 220, 214, 220, 214, 0, 10),
		newTargetWithActualInfo("c", false, 290, 190, 0, 0, 290, 275, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), nil, nil, false},
		"c": {ptr(271), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 410, MaxSurge: 0, MaxUnavailable: 20}
	s.Name = "I0721 20:03:08.508247"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 193, 120, 0, 0, 193, 193, 0, 9),
		newTargetWithActualInfo("c", false, 317, 290, 0, 0, 317, 292, 0, 15),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(120), ptr(0), ptr(20), false},
		"c": {ptr(290), nil, nil, true},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 20:04:47.826333"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 120, 220, 120, 120, 120, 120, 0, 6),
		newTargetWithActualInfo("c", true, 290, 290, 23, 4, 290, 271, 0, 20),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(120), nil, nil, false},
		"c": {nil, ptr(100), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 20:08:48.439058"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 120, 220, 120, 120, 120, 120, 0, 6),
		newTargetWithActualInfo("c", true, 290, 290, 265, 245, 290, 270, 0, 20),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(195), nil, nil, false},
		"c": {nil, ptr(25), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0721 22:00:34.777064"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 220, 320, 220, 217, 220, 215, 0, 11),
		newTargetWithActualInfo("c", true, 290, 190, 290, 279, 290, 279, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), nil, nil, false},
		"c": {ptr(270), nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0722 10:58:38.428687"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 120, 220, 0, 0, 120, 120, 0, 6),
		newTargetWithActualInfo("c", false, 190, 290, 0, 0, 190, 190, 0, 9),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(120), ptr(120), ptr(0), false},
		"c": {ptr(190), ptr(80), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0722 10:59:21.853509"
	s.Targets = Targets{
		newTargetWithActualInfo("b", true, 120, 220, 120, 120, 120, 120, 120, 0),
		newTargetWithActualInfo("c", true, 190, 290, 96, 0, 286, 190, 94, 0),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), nil, nil, false},
		"c": {ptr(194), ptr(94), ptr(0), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 510, MaxSurge: 0, MaxUnavailable: 25}
	s.Name = "I0722 11:43:31.110287"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 220, 420, 0, 0, 220, 220, 0, 11),
		newTargetWithActualInfo("c", false, 290, 90, 0, 0, 290, 281, 0, 14),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(220), ptr(0), ptr(16), false},
		"c": {ptr(281), nil, nil, true},
	}
	tests = append(tests, s)

	// c is actually during updating, it's paused during updating from a->b. While b is paused
	// during updating from b->c
	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 50}
	s.Name = "I0726 13:08:28.299562"
	s.Targets = Targets{
		newTargetWithAllInfo("b", false, 350, 350, 0, 0, 320, 320, 350, 350, 0, 30),
		newTargetWithAllInfo("c", false, 150, 150, 0, 0, 0, 0, 150, 150, 0, 20),
	}
	s.Plans = RolloutPlans{
		"b": {nil, ptr(0), ptr(30), false},
		"c": {nil, ptr(0), ptr(20), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 50}
	s.Name = "I0726 17:34:07.505088"
	s.Targets = Targets{
		newTargetWithActualInfo("b", false, 261, 320, 0, 0, 261, 261, 0, 26),
		newTargetWithActualInfo("c", true, 239, 180, 239, 189, 239, 189, 0, 23),
	}
	s.Plans = RolloutPlans{
		"c": {ptr(189), nil, nil, false},
	}
	tests = append(tests, s)

	// it would be better if we scale in c during it's updating
	s = PlanTestSuit{Replicas: 500, MaxSurge: 0, MaxUnavailable: 50}
	s.Name = "I0726 19:32:32.905839"
	s.Targets = Targets{
		newTargetWithAllInfo("b", false, 320, 220, 0, 0, 235, 235, 320, 282, 0, 43),
		newTargetWithActualInfo("c", false, 180, 280, 0, 0, 180, 173, 0, 18),
	}
	s.Plans = RolloutPlans{
		"b": {ptr(320), ptr(0), ptr(43), false},
		"c": {ptr(180), ptr(0), ptr(7), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 25, MaxSurge: 0, MaxUnavailable: 6}
	s.Name = "I0729 15:55:04.138529"
	s.Targets = Targets{
		newTargetWithAllInfo("a", false, 8, 8, 0, 0, 8, 1, 8, 1, 0, 2),
		newTargetWithAllInfo("b", false, 1, 1, 0, 0, 1, 0, 1, 0, 0, 1),
		newTargetWithAllInfo("c", false, 4, 4, 0, 0, 4, 0, 4, 0, 0, 1),
		newTargetWithAllInfo("e", false, 4, 4, 0, 0, 4, 0, 4, 0, 0, 1),
		newTargetWithAllInfo("f", false, 4, 4, 0, 0, 4, 2, 4, 2, 0, 1),
		newTargetWithAllInfo("h", false, 4, 4, 0, 0, 4, 1, 4, 1, 0, 1),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(0), ptr(1), false},
		"b": {nil, ptr(0), ptr(1), false},
		"c": {nil, ptr(0), ptr(1), false},
		"e": {nil, ptr(0), ptr(1), false},
		"f": {nil, ptr(0), ptr(1), false},
		"h": {nil, ptr(0), ptr(1), false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 10, MaxSurge: 0, MaxUnavailable: 2}
	s.Name = "I0801 23:42:36.557596"
	s.Targets = Targets{
		newTargetWithAllInfo("a", false, 3, 0, 0, 0, 5, 5, 5, 5, 0, 1),
		newTargetWithAllInfo("c", false, 2, 0, 0, 0, 2, 2, 2, 2, 0, 1),
		newTargetWithAllInfo("d", true, 0, 10, 0, 0, 0, 0, 0, 0, 0, 1),
		newTargetWithAllInfo("e", false, 1, 0, 0, 0, 1, 1, 1, 1, 0, 1),
		newTargetWithAllInfo("f", false, 2, 0, 0, 0, 2, 2, 2, 2, 0, 1),
	}
	s.Plans = RolloutPlans{
		"d": {ptr(0), nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 8, MaxSurge: 0, MaxUnavailable: 2}
	s.Name = "I0801 18:08:25.282496"
	s.Targets = Targets{
		newTargetWithAllInfo("a", false, 6, 1, 0, 0, 8, 8, 8, 8, 0, 1),
		newTargetWithAllInfo("d", true, 0, 7, 0, 0, 0, 0, 0, 0, 0, 1),
	}
	s.Plans = RolloutPlans{
		"d": {ptr(0), nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 50, MaxSurge: 5, MaxUnavailable: 0}
	s.Name = "I0823 11:46:19.897608"
	s.Targets = Targets{
		newTargetWithAllInfo("a", true, 19, 19, 19, 19, 19, 19, 19, 19, 2, 0),
		newTargetWithAllInfo("b", true, 8, 8, 6, 2, 6, 2, 10, 8, 4, 0),
		newTargetWithAllInfo("d", true, 5, 5, 5, 5, 5, 5, 5, 5, 1, 0),
		newTargetWithAllInfo("e", true, 7, 7, 7, 6, 7, 6, 8, 7, 1, 0),
		newTargetWithAllInfo("f", false, 7, 7, 0, 0, 7, 7, 7, 7, 1, 0),
		newTargetWithAllInfo("h", true, 4, 4, 4, 4, 4, 4, 4, 4, 1, 0),
	}
	s.Plans = RolloutPlans{
		"a": {nil, nil, nil, false},
		"b": {nil, ptr(2), ptr(0), false},
		"d": {nil, nil, nil, false},
		"e": {nil, nil, nil, false},
		"h": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 50, MaxSurge: 5, MaxUnavailable: 0}
	s.Name = "I0823 11:46:24.931388"
	s.Targets = Targets{
		newTargetWithAllInfo("a", true, 19, 19, 19, 19, 19, 19, 19, 19, 2, 0),
		newTargetWithAllInfo("b", true, 8, 8, 6, 2, 6, 2, 12, 8, 4, 0),
		newTargetWithAllInfo("d", true, 5, 5, 5, 5, 5, 5, 5, 5, 1, 0),
		newTargetWithAllInfo("e", true, 7, 7, 7, 6, 7, 6, 8, 7, 1, 0),
		newTargetWithAllInfo("f", true, 7, 7, 2, 0, 2, 0, 9, 7, 2, 0),
		newTargetWithAllInfo("h", true, 4, 4, 4, 4, 4, 4, 4, 4, 1, 0),
	}
	s.Plans = RolloutPlans{
		"a": {nil, nil, nil, false},
		"b": {nil, ptr(2), ptr(0), false},
		"d": {nil, nil, nil, false},
		"e": {nil, nil, nil, false},
		"f": {nil, ptr(1), ptr(0), false},
		"h": {nil, nil, nil, false},
	}
	tests = append(tests, s)

	s = PlanTestSuit{Replicas: 2, MaxSurge: 0, MaxUnavailable: 1}
	s.Name = "I0819 14:27:50.120900"
	s.Targets = Targets{
		newTargetWithAllInfo("a", true, 1, 1, 1, 0, 1, 0, 2, 1, 1, 0),
		newTargetWithAllInfo("e", true, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1),
	}
	s.Plans = RolloutPlans{
		"a": {nil, ptr(1), ptr(0), false},
		"e": {ptr(0), nil, nil, false},
	}
	tests = append(tests, s)

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			planner := &RolloutPlanner{Targets: test.Targets, MaxSurge: test.MaxSurge, MaxUnavailable: test.MaxUnavailable, Replicas: test.Replicas}
			got := planner.Plan()
			if !reflect.DeepEqual(got, test.Plans) {
				t.Errorf("%s: got: %v, expected: %v", test.Name, got, test.Plans)
			}
		})
	}
}
