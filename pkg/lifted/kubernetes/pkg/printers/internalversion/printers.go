/*
Copyright 2017 The Kubernetes Authors.

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
This file is lifted from https://github.com/kubernetes/kubernetes/blob/release-1.27/pkg/printers/internalversion/printers.go
*/

package internalversion

import (
	"fmt"
	"strconv"
	"time"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers"
)

// NodeUnreachablePodReason is the reason on a pod when its state cannot be confirmed as kubelet is unresponsive
// on the node it is (was) running.
//
// taken from https://github.com/kubernetes/kubernetes/blob/release-1.27/pkg/util/node/node.go#L37
const NodeUnreachablePodReason = "NodeLost"

// AddHandlers adds print handlers for default Kubernetes types dealing with internal versions.
// TODO: handle errors from Handler
func AddHandlers(h printers.PrintHandler) {
	podColumnDefinitions := []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: metav1.ObjectMeta{}.SwaggerDoc()["name"]},
		{Name: "Ready", Type: "string", Description: "The aggregate readiness state of this pod for accepting traffic."},
		{Name: "Status", Type: "string", Description: "The aggregate status of the containers in this pod."},
		{Name: "Restarts", Type: "string", Description: "The number of times the containers in this pod have been restarted and when the last container in this pod has restarted."},
		{Name: "Age", Type: "string", Description: metav1.ObjectMeta{}.SwaggerDoc()["creationTimestamp"]},
		{Name: "IP", Type: "string", Priority: 1, Description: apiv1.PodStatus{}.SwaggerDoc()["podIP"]},
		{Name: "Node", Type: "string", Priority: 1, Description: apiv1.PodSpec{}.SwaggerDoc()["nodeName"]},
		{Name: "Nominated Node", Type: "string", Priority: 1, Description: apiv1.PodStatus{}.SwaggerDoc()["nominatedNodeName"]},
		{Name: "Readiness Gates", Type: "string", Priority: 1, Description: apiv1.PodSpec{}.SwaggerDoc()["readinessGates"]},
	}
	h.TableHandler(podColumnDefinitions, printPodList)
	h.TableHandler(podColumnDefinitions, printPod)
}

// translateTimestampSince returns the elapsed time since timestamp in
// human-readable approximation.
func translateTimestampSince(timestamp metav1.Time) string {
	if timestamp.IsZero() {
		return "<unknown>"
	}

	return duration.HumanDuration(time.Since(timestamp.Time))
}

var (
	podSuccessConditions = []metav1.TableRowCondition{{Type: metav1.RowCompleted, Status: metav1.ConditionTrue, Reason: string(apiv1.PodSucceeded), Message: "The pod has completed successfully."}}
	podFailedConditions  = []metav1.TableRowCondition{{Type: metav1.RowCompleted, Status: metav1.ConditionTrue, Reason: string(apiv1.PodFailed), Message: "The pod failed."}}
)

func printPodList(podList *apiv1.PodList, options printers.GenerateOptions) ([]metav1.TableRow, error) {
	rows := make([]metav1.TableRow, 0, len(podList.Items))
	for i := range podList.Items {
		r, err := printPod(&podList.Items[i], options)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	return rows, nil
}

func printPod(pod *apiv1.Pod, options printers.GenerateOptions) ([]metav1.TableRow, error) {
	restarts := 0
	totalContainers := len(pod.Spec.Containers)
	readyContainers := 0
	lastRestartDate := metav1.NewTime(time.Time{})

	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	// If the Pod carries {type:PodScheduled, reason:WaitingForGates}, set reason to 'SchedulingGated'.
	for _, condition := range pod.Status.Conditions {
		if condition.Type == apiv1.PodScheduled && condition.Reason == apiv1.PodReasonSchedulingGated {
			reason = apiv1.PodReasonSchedulingGated
		}
	}

	row := metav1.TableRow{
		Object: runtime.RawExtension{Object: pod},
	}

	switch pod.Status.Phase {
	case apiv1.PodSucceeded:
		row.Conditions = podSuccessConditions
	case apiv1.PodFailed:
		row.Conditions = podFailedConditions
	}

	initializing := false
	for i := range pod.Status.InitContainerStatuses {
		container := pod.Status.InitContainerStatuses[i]
		restarts += int(container.RestartCount)
		if container.LastTerminationState.Terminated != nil {
			terminatedDate := container.LastTerminationState.Terminated.FinishedAt
			if lastRestartDate.Before(&terminatedDate) {
				lastRestartDate = terminatedDate
			}
		}
		switch {
		case container.State.Terminated != nil && container.State.Terminated.ExitCode == 0:
			continue
		case container.State.Terminated != nil:
			// initialization is failed
			if len(container.State.Terminated.Reason) == 0 {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Init:Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("Init:ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else {
				reason = "Init:" + container.State.Terminated.Reason
			}
			initializing = true
		case container.State.Waiting != nil && len(container.State.Waiting.Reason) > 0 && container.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + container.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}
	if !initializing {
		restarts = 0
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			container := pod.Status.ContainerStatuses[i]

			restarts += int(container.RestartCount)
			if container.LastTerminationState.Terminated != nil {
				terminatedDate := container.LastTerminationState.Terminated.FinishedAt
				if lastRestartDate.Before(&terminatedDate) {
					lastRestartDate = terminatedDate
				}
			}
			if container.State.Waiting != nil && container.State.Waiting.Reason != "" {
				reason = container.State.Waiting.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason != "" {
				reason = container.State.Terminated.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason == "" {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else if container.Ready && container.State.Running != nil {
				hasRunning = true
				readyContainers++
			}
		}

		// change pod status back to "Running" if there is at least one container still reporting as "Running" status
		if reason == "Completed" && hasRunning {
			if hasPodReadyCondition(pod.Status.Conditions) {
				reason = "Running"
			} else {
				reason = "NotReady"
			}
		}
	}

	if pod.DeletionTimestamp != nil && pod.Status.Reason == NodeUnreachablePodReason {
		reason = "Unknown"
	} else if pod.DeletionTimestamp != nil {
		reason = "Terminating"
	}

	restartsStr := strconv.Itoa(restarts)
	if !lastRestartDate.IsZero() {
		restartsStr = fmt.Sprintf("%d (%s ago)", restarts, translateTimestampSince(lastRestartDate))
	}

	row.Cells = append(row.Cells, pod.Name, fmt.Sprintf("%d/%d", readyContainers, totalContainers), reason, restartsStr, translateTimestampSince(pod.CreationTimestamp))
	if options.Wide {
		nodeName := pod.Spec.NodeName
		nominatedNodeName := pod.Status.NominatedNodeName
		podIP := ""
		if len(pod.Status.PodIPs) > 0 {
			podIP = pod.Status.PodIPs[0].IP
		}

		if podIP == "" {
			podIP = "<none>"
		}
		if nodeName == "" {
			nodeName = "<none>"
		}
		if nominatedNodeName == "" {
			nominatedNodeName = "<none>"
		}

		readinessGates := "<none>"
		if len(pod.Spec.ReadinessGates) > 0 {
			trueConditions := 0
			for _, readinessGate := range pod.Spec.ReadinessGates {
				conditionType := readinessGate.ConditionType
				for _, condition := range pod.Status.Conditions {
					if condition.Type == conditionType {
						if condition.Status == apiv1.ConditionTrue {
							trueConditions++
						}
						break
					}
				}
			}
			readinessGates = fmt.Sprintf("%d/%d", trueConditions, len(pod.Spec.ReadinessGates))
		}
		row.Cells = append(row.Cells, podIP, nodeName, nominatedNodeName, readinessGates)
	}

	return []metav1.TableRow{row}, nil
}

func hasPodReadyCondition(conditions []apiv1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == apiv1.PodReady && condition.Status == apiv1.ConditionTrue {
			return true
		}
	}
	return false
}
