/*
Copyright 2014 The Kubernetes Authors.

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

/*
This file is lifted from https://github.com/kubernetes/kubernetes/blob/release-1.27/pkg/printers/internalversion/printers_test.go
*/

package internalversion

import (
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers"
)

//nolint:lll
func TestPrintPod(t *testing.T) {
	tests := []struct {
		pod    corev1.Pod
		expect []metav1.TableRow
	}{
		{
			// Test name, num of containers, restarts, container ready status
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "podPhase", "6", "<unknown>"}}},
		},
		{
			// Test container error overwrites pod phase
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test2"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerWaitingReason"}}, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test2", "1/2", "ContainerWaitingReason", "6", "<unknown>"}}},
		},
		{
			// Test the same as the above but with Terminated state and the first container overwrites the rest
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test3"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []corev1.ContainerStatus{
						{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerWaitingReason"}}, RestartCount: 3},
						{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "ContainerTerminatedReason"}}, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test3", "0/2", "ContainerWaitingReason", "6", "<unknown>"}}},
		},
		{
			// Test ready is not enough for reporting running
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test4"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{Ready: true, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test4", "1/2", "podPhase", "6", "<unknown>"}}},
		},
		{
			// Test ready is not enough for reporting running
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test5"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Reason: "podReason",
					Phase:  "podPhase",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{Ready: true, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test5", "1/2", "podReason", "6", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one is running and the other is completed, w/o ready condition
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test6"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase:  "Running",
					Reason: "",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed", ExitCode: 0}}},
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test6", "1/2", "NotReady", "6", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one is running and the other is completed, with ready condition
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test6"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase:  "Running",
					Reason: "",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed", ExitCode: 0}}},
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
					},
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastProbeTime: metav1.Time{Time: time.Now()}},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test6", "1/2", "Running", "6", "<unknown>"}}},
		},
		{
			// Test pod has 1 init container restarting and 1 container not running
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test7"},
				Spec:       corev1.PodSpec{InitContainers: make([]corev1.Container, 1), Containers: make([]corev1.Container, 1)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:        false,
							RestartCount: 0,
							State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test7", "0/1", "Init:0/1", "3 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 init containers, one restarting and the other not running, and 1 container not running
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test8"},
				Spec:       corev1.PodSpec{InitContainers: make([]corev1.Container, 2), Containers: make([]corev1.Container, 1)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
						{
							Ready: false,
							State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: false,
							State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test8", "0/1", "Init:0/2", "3 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 init containers, one completed without restarts and the other restarting, and 1 container not running
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test9"},
				Spec:       corev1.PodSpec{InitContainers: make([]corev1.Container, 2), Containers: make([]corev1.Container, 1)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: false,
							State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}},
						},
						{
							Ready:                false,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: false,
							State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test9", "0/1", "Init:1/2", "3 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 init containers, one completed with restarts and the other restarting, and 1 container not running
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test10"},
				Spec:       corev1.PodSpec{InitContainers: make([]corev1.Container, 2), Containers: make([]corev1.Container, 1)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         2,
							State:                corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-2 * time.Minute))}},
						},
						{
							Ready:                false,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: false,
							State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test10", "0/1", "Init:1/2", "5 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 1 init container completed with restarts and one container restarting
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test11"},
				Spec:       corev1.PodSpec{InitContainers: make([]corev1.Container, 1), Containers: make([]corev1.Container, 1)},
				Status: corev1.PodStatus{
					Phase: "Running",
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         2,
							State:                corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-2 * time.Minute))}},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         4,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-20 * time.Second))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test11", "0/1", "Running", "4 (20s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 1 container that restarted 5d ago
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test12"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 1)},
				Status: corev1.PodStatus{
					Phase: "Running",
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                true,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-5 * 24 * time.Hour))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test12", "1/1", "Running", "3 (5d ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one has never restarted and the other has restarted 10d ago
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test13"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "Running",
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:        true,
							RestartCount: 0,
							State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
						},
						{
							Ready:                true,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * 24 * time.Hour))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test13", "2/2", "Running", "3 (10d ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one restarted 5d ago and the other restarted 20d ago
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test14"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "Running",
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready:                true,
							RestartCount:         6,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-5 * 24 * time.Hour))}},
						},
						{
							Ready:                true,
							RestartCount:         3,
							State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-20 * 24 * time.Hour))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test14", "2/2", "Running", "9 (5d ago)", "<unknown>"}}},
		},
		{
			// Test PodScheduled condition with reason WaitingForGates
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test15"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodScheduled,
							Status: corev1.ConditionFalse,
							Reason: corev1.PodReasonSchedulingGated,
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test15", "0/2", corev1.PodReasonSchedulingGated, "0", "<unknown>"}}},
		},
	}

	for i, test := range tests {
		rows, err := printPod(&test.pod, printers.GenerateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].Object.Object = nil
		}
		if !reflect.DeepEqual(test.expect, rows) {
			t.Errorf("%d mismatch: %s", i, diff.ObjectReflectDiff(test.expect, rows))
		}
	}
}

//nolint:lll
func TestPrintPodwide(t *testing.T) {
	condition1 := "condition1"
	condition2 := "condition2"
	condition3 := "condition3"
	tests := []struct {
		pod    corev1.Pod
		expect []metav1.TableRow
	}{
		{
			// Test when the NodeName and PodIP are not none
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec: corev1.PodSpec{
					Containers: make([]corev1.Container, 2),
					NodeName:   "test1",
					ReadinessGates: []corev1.PodReadinessGate{
						{
							ConditionType: corev1.PodConditionType(condition1),
						},
						{
							ConditionType: corev1.PodConditionType(condition2),
						},
						{
							ConditionType: corev1.PodConditionType(condition3),
						},
					},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodConditionType(condition1),
							Status: corev1.ConditionFalse,
						},
						{
							Type:   corev1.PodConditionType(condition2),
							Status: corev1.ConditionTrue,
						},
					},
					Phase:  "podPhase",
					PodIPs: []corev1.PodIP{{IP: "1.1.1.1"}},
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
					NominatedNodeName: "node1",
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "podPhase", "6", "<unknown>", "1.1.1.1", "test1", "node1", "1/3"}}},
		},
		{
			// Test when the NodeName and PodIP are not none
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec: corev1.PodSpec{
					Containers: make([]corev1.Container, 2),
					NodeName:   "test1",
					ReadinessGates: []corev1.PodReadinessGate{
						{
							ConditionType: corev1.PodConditionType(condition1),
						},
						{
							ConditionType: corev1.PodConditionType(condition2),
						},
						{
							ConditionType: corev1.PodConditionType(condition3),
						},
					},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodConditionType(condition1),
							Status: corev1.ConditionFalse,
						},
						{
							Type:   corev1.PodConditionType(condition2),
							Status: corev1.ConditionTrue,
						},
					},
					Phase:  "podPhase",
					PodIPs: []corev1.PodIP{{IP: "1.1.1.1"}, {IP: "2001:db8::"}},
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
					NominatedNodeName: "node1",
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "podPhase", "6", "<unknown>", "1.1.1.1", "test1", "node1", "1/3"}}},
		},
		{
			// Test when the NodeName and PodIP are none
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test2"},
				Spec: corev1.PodSpec{
					Containers: make([]corev1.Container, 2),
					NodeName:   "",
				},
				Status: corev1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerWaitingReason"}}, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test2", "1/2", "ContainerWaitingReason", "6", "<unknown>", "<none>", "<none>", "<none>", "<none>"}}},
		},
	}

	for i, test := range tests {
		rows, err := printPod(&test.pod, printers.GenerateOptions{Wide: true})
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].Object.Object = nil
		}
		if !reflect.DeepEqual(test.expect, rows) {
			t.Errorf("%d mismatch: %s", i, diff.ObjectReflectDiff(test.expect, rows))
		}
	}
}

func TestPrintPodConditions(t *testing.T) {
	runningPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test1", Labels: map[string]string{"a": "1", "b": "2"}},
		Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
		Status: corev1.PodStatus{
			Phase: "Running",
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{RestartCount: 3},
			},
		},
	}
	succeededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test1", Labels: map[string]string{"a": "1", "b": "2"}},
		Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
		Status: corev1.PodStatus{
			Phase: "Succeeded",
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{RestartCount: 3},
			},
		},
	}
	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test2", Labels: map[string]string{"b": "2"}},
		Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
		Status: corev1.PodStatus{
			Phase: "Failed",
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{RestartCount: 3},
			},
		},
	}
	tests := []struct {
		pod    *corev1.Pod
		expect []metav1.TableRow
	}{
		// Should not have TableRowCondition
		{
			pod: runningPod,
			// Columns: Name, Ready, Reason, Restarts, Age
			expect: []metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "Running", "6", "<unknown>"}}},
		},
		// Should have TableRowCondition: podSuccessConditions
		{
			pod: succeededPod,
			expect: []metav1.TableRow{
				{
					// Columns: Name, Ready, Reason, Restarts, Age
					Cells:      []interface{}{"test1", "1/2", "Succeeded", "6", "<unknown>"},
					Conditions: podSuccessConditions,
				},
			},
		},
		// Should have TableRowCondition: podFailedCondition
		{
			pod: failedPod,
			expect: []metav1.TableRow{
				{
					// Columns: Name, Ready, Reason, Restarts, Age
					Cells:      []interface{}{"test2", "1/2", "Failed", "6", "<unknown>"},
					Conditions: podFailedConditions,
				},
			},
		},
	}

	for i, test := range tests {
		rows, err := printPod(test.pod, printers.GenerateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].Object.Object = nil
		}
		if !reflect.DeepEqual(test.expect, rows) {
			t.Errorf("%d mismatch: %s", i, diff.ObjectReflectDiff(test.expect, rows))
		}
	}
}

//nolint:lll
func TestPrintPodList(t *testing.T) {
	tests := []struct {
		pods   corev1.PodList
		expect []metav1.TableRow
	}{
		// Test podList's pod: name, num of containers, restarts, container ready status
		{
			corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "test1"},
						Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
						Status: corev1.PodStatus{
							Phase: "podPhase",
							ContainerStatuses: []corev1.ContainerStatus{
								{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
								{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "test2"},
						Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 1)},
						Status: corev1.PodStatus{
							Phase: "podPhase",
							ContainerStatuses: []corev1.ContainerStatus{
								{Ready: true, RestartCount: 1, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
							},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "2/2", "podPhase", "6", "<unknown>"}}, {Cells: []interface{}{"test2", "1/1", "podPhase", "1", "<unknown>"}}},
		},
	}

	for _, test := range tests {
		rows, err := printPodList(&test.pods, printers.GenerateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].Object.Object = nil
		}
		if !reflect.DeepEqual(test.expect, rows) {
			t.Errorf("mismatch: %s", diff.ObjectReflectDiff(test.expect, rows))
		}
	}
}

func TestPrintNonTerminatedPod(t *testing.T) {
	tests := []struct {
		pod    corev1.Pod
		expect []metav1.TableRow
	}{
		{
			// Test pod phase Running should be printed
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "Running", "6", "<unknown>"}}},
		},
		{
			// Test pod phase Pending should be printed
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test2"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{{Cells: []interface{}{"test2", "1/2", "Pending", "6", "<unknown>"}}},
		},
		{
			// Test pod phase Unknown should be printed
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test3"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: corev1.PodUnknown,
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{{Cells: []interface{}{"test3", "1/2", "Unknown", "6", "<unknown>"}}},
		},
		{
			// Test pod phase Succeeded should be printed
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test4"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{
				{
					Cells:      []interface{}{"test4", "1/2", "Succeeded", "6", "<unknown>"},
					Conditions: podSuccessConditions,
				},
			},
		},
		{
			// Test pod phase Failed shouldn't be printed
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test5"},
				Spec:       corev1.PodSpec{Containers: make([]corev1.Container, 2)},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
					ContainerStatuses: []corev1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						{Ready: true, RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{
				{
					Cells:      []interface{}{"test5", "1/2", "Failed", "6", "<unknown>"},
					Conditions: podFailedConditions,
				},
			},
		},
	}

	for i, test := range tests {
		rows, err := printPod(&test.pod, printers.GenerateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].Object.Object = nil
		}
		if !reflect.DeepEqual(test.expect, rows) {
			t.Errorf("%d mismatch: %s", i, diff.ObjectReflectDiff(test.expect, rows))
		}
	}
}
