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

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers"
)

func TestPrintPod(t *testing.T) {
	tests := []struct {
		pod    apiv1.Pod
		expect []metav1.TableRow
	}{
		{
			// Test name, num of containers, restarts, container ready status
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "podPhase", "6", "<unknown>"}}},
		},
		{
			// Test container error overwrites pod phase
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test2"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{Reason: "ContainerWaitingReason"}}, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test2", "1/2", "ContainerWaitingReason", "6", "<unknown>"}}},
		},
		{
			// Test the same as the above but with Terminated state and the first container overwrites the rest
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test3"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []apiv1.ContainerStatus{
						{State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{Reason: "ContainerWaitingReason"}}, RestartCount: 3},
						{State: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{Reason: "ContainerTerminatedReason"}}, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test3", "0/2", "ContainerWaitingReason", "6", "<unknown>"}}},
		},
		{
			// Test ready is not enough for reporting running
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test4"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{Ready: true, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test4", "1/2", "podPhase", "6", "<unknown>"}}},
		},
		{
			// Test ready is not enough for reporting running
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test5"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Reason: "podReason",
					Phase:  "podPhase",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{Ready: true, RestartCount: 3},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test5", "1/2", "podReason", "6", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one is running and the other is completed, w/o ready condition
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test6"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase:  "Running",
					Reason: "",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{Reason: "Completed", ExitCode: 0}}},
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test6", "1/2", "NotReady", "6", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one is running and the other is completed, with ready condition
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test6"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase:  "Running",
					Reason: "",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{Reason: "Completed", ExitCode: 0}}},
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
					},
					Conditions: []apiv1.PodCondition{
						{Type: apiv1.PodReady, Status: apiv1.ConditionTrue, LastProbeTime: metav1.Time{Time: time.Now()}},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test6", "1/2", "Running", "6", "<unknown>"}}},
		},
		{
			// Test pod has 1 init container restarting and 1 container not running
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test7"},
				Spec:       apiv1.PodSpec{InitContainers: make([]apiv1.Container, 1), Containers: make([]apiv1.Container, 1)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
					},
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:        false,
							RestartCount: 0,
							State:        apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test7", "0/1", "Init:0/1", "3 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 init containers, one restarting and the other not running, and 1 container not running
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test8"},
				Spec:       apiv1.PodSpec{InitContainers: make([]apiv1.Container, 2), Containers: make([]apiv1.Container, 1)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
						{
							Ready: false,
							State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{}},
						},
					},
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready: false,
							State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test8", "0/1", "Init:0/2", "3 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 init containers, one completed without restarts and the other restarting, and 1 container not running
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test9"},
				Spec:       apiv1.PodSpec{InitContainers: make([]apiv1.Container, 2), Containers: make([]apiv1.Container, 1)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready: false,
							State: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{}},
						},
						{
							Ready:                false,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
					},
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready: false,
							State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test9", "0/1", "Init:1/2", "3 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 init containers, one completed with restarts and the other restarting, and 1 container not running
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test10"},
				Spec:       apiv1.PodSpec{InitContainers: make([]apiv1.Container, 2), Containers: make([]apiv1.Container, 1)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					InitContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         2,
							State:                apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-2 * time.Minute))}},
						},
						{
							Ready:                false,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second))}},
						},
					},
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready: false,
							State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test10", "0/1", "Init:1/2", "5 (10s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 1 init container completed with restarts and one container restarting
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test11"},
				Spec:       apiv1.PodSpec{InitContainers: make([]apiv1.Container, 1), Containers: make([]apiv1.Container, 1)},
				Status: apiv1.PodStatus{
					Phase: "Running",
					InitContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         2,
							State:                apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-2 * time.Minute))}},
						},
					},
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                false,
							RestartCount:         4,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-20 * time.Second))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test11", "0/1", "Running", "4 (20s ago)", "<unknown>"}}},
		},
		{
			// Test pod has 1 container that restarted 5d ago
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test12"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 1)},
				Status: apiv1.PodStatus{
					Phase: "Running",
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                true,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-5 * 24 * time.Hour))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test12", "1/1", "Running", "3 (5d ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one has never restarted and the other has restarted 10d ago
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test13"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "Running",
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:        true,
							RestartCount: 0,
							State:        apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
						},
						{
							Ready:                true,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-10 * 24 * time.Hour))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test13", "2/2", "Running", "3 (10d ago)", "<unknown>"}}},
		},
		{
			// Test pod has 2 containers, one restarted 5d ago and the other restarted 20d ago
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test14"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "Running",
					ContainerStatuses: []apiv1.ContainerStatus{
						{
							Ready:                true,
							RestartCount:         6,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-5 * 24 * time.Hour))}},
						},
						{
							Ready:                true,
							RestartCount:         3,
							State:                apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}},
							LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Now().Add(-20 * 24 * time.Hour))}},
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test14", "2/2", "Running", "9 (5d ago)", "<unknown>"}}},
		},
		{
			// Test PodScheduled condition with reason WaitingForGates
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test15"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					Conditions: []apiv1.PodCondition{
						{
							Type:   apiv1.PodScheduled,
							Status: apiv1.ConditionFalse,
							Reason: apiv1.PodReasonSchedulingGated,
						},
					},
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test15", "0/2", apiv1.PodReasonSchedulingGated, "0", "<unknown>"}}},
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

func TestPrintPodwide(t *testing.T) {
	condition1 := "condition1"
	condition2 := "condition2"
	condition3 := "condition3"
	tests := []struct {
		pod    apiv1.Pod
		expect []metav1.TableRow
	}{
		{
			// Test when the NodeName and PodIP are not none
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec: apiv1.PodSpec{
					Containers: make([]apiv1.Container, 2),
					NodeName:   "test1",
					ReadinessGates: []apiv1.PodReadinessGate{
						{
							ConditionType: apiv1.PodConditionType(condition1),
						},
						{
							ConditionType: apiv1.PodConditionType(condition2),
						},
						{
							ConditionType: apiv1.PodConditionType(condition3),
						},
					},
				},
				Status: apiv1.PodStatus{
					Conditions: []apiv1.PodCondition{
						{
							Type:   apiv1.PodConditionType(condition1),
							Status: apiv1.ConditionFalse,
						},
						{
							Type:   apiv1.PodConditionType(condition2),
							Status: apiv1.ConditionTrue,
						},
					},
					Phase:  "podPhase",
					PodIPs: []apiv1.PodIP{{IP: "1.1.1.1"}},
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
					NominatedNodeName: "node1",
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "podPhase", "6", "<unknown>", "1.1.1.1", "test1", "node1", "1/3"}}},
		},
		{
			// Test when the NodeName and PodIP are not none
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec: apiv1.PodSpec{
					Containers: make([]apiv1.Container, 2),
					NodeName:   "test1",
					ReadinessGates: []apiv1.PodReadinessGate{
						{
							ConditionType: apiv1.PodConditionType(condition1),
						},
						{
							ConditionType: apiv1.PodConditionType(condition2),
						},
						{
							ConditionType: apiv1.PodConditionType(condition3),
						},
					},
				},
				Status: apiv1.PodStatus{
					Conditions: []apiv1.PodCondition{
						{
							Type:   apiv1.PodConditionType(condition1),
							Status: apiv1.ConditionFalse,
						},
						{
							Type:   apiv1.PodConditionType(condition2),
							Status: apiv1.ConditionTrue,
						},
					},
					Phase:  "podPhase",
					PodIPs: []apiv1.PodIP{{IP: "1.1.1.1"}, {IP: "2001:db8::"}},
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
					NominatedNodeName: "node1",
				},
			},
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "podPhase", "6", "<unknown>", "1.1.1.1", "test1", "node1", "1/3"}}},
		},
		{
			// Test when the NodeName and PodIP are none
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test2"},
				Spec: apiv1.PodSpec{
					Containers: make([]apiv1.Container, 2),
					NodeName:   "",
				},
				Status: apiv1.PodStatus{
					Phase: "podPhase",
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{Reason: "ContainerWaitingReason"}}, RestartCount: 3},
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
	runningPod := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test1", Labels: map[string]string{"a": "1", "b": "2"}},
		Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
		Status: apiv1.PodStatus{
			Phase: "Running",
			ContainerStatuses: []apiv1.ContainerStatus{
				{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
				{RestartCount: 3},
			},
		},
	}
	succeededPod := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test1", Labels: map[string]string{"a": "1", "b": "2"}},
		Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
		Status: apiv1.PodStatus{
			Phase: "Succeeded",
			ContainerStatuses: []apiv1.ContainerStatus{
				{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
				{RestartCount: 3},
			},
		},
	}
	failedPod := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test2", Labels: map[string]string{"b": "2"}},
		Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
		Status: apiv1.PodStatus{
			Phase: "Failed",
			ContainerStatuses: []apiv1.ContainerStatus{
				{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
				{RestartCount: 3},
			},
		},
	}
	tests := []struct {
		pod    *apiv1.Pod
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

func TestPrintPodList(t *testing.T) {
	tests := []struct {
		pods   apiv1.PodList
		expect []metav1.TableRow
	}{
		// Test podList's pod: name, num of containers, restarts, container ready status
		{
			apiv1.PodList{
				Items: []apiv1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "test1"},
						Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
						Status: apiv1.PodStatus{
							Phase: "podPhase",
							ContainerStatuses: []apiv1.ContainerStatus{
								{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
								{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "test2"},
						Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 1)},
						Status: apiv1.PodStatus{
							Phase: "podPhase",
							ContainerStatuses: []apiv1.ContainerStatus{
								{Ready: true, RestartCount: 1, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
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
		pod    apiv1.Pod
		expect []metav1.TableRow
	}{
		{
			// Test pod phase Running should be printed
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test1"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: apiv1.PodRunning,
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{{Cells: []interface{}{"test1", "1/2", "Running", "6", "<unknown>"}}},
		},
		{
			// Test pod phase Pending should be printed
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test2"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: apiv1.PodPending,
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{{Cells: []interface{}{"test2", "1/2", "Pending", "6", "<unknown>"}}},
		},
		{
			// Test pod phase Unknown should be printed
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test3"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: apiv1.PodUnknown,
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
						{RestartCount: 3},
					},
				},
			},
			// Columns: Name, Ready, Reason, Restarts, Age
			[]metav1.TableRow{{Cells: []interface{}{"test3", "1/2", "Unknown", "6", "<unknown>"}}},
		},
		{
			// Test pod phase Succeeded should be printed
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test4"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: apiv1.PodSucceeded,
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
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
			apiv1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test5"},
				Spec:       apiv1.PodSpec{Containers: make([]apiv1.Container, 2)},
				Status: apiv1.PodStatus{
					Phase: apiv1.PodFailed,
					ContainerStatuses: []apiv1.ContainerStatus{
						{Ready: true, RestartCount: 3, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
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
