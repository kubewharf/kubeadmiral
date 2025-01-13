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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers"
)

// NodeUnreachablePodReason is the reason on a pod when its state cannot be confirmed as kubelet is unresponsive
// on the node it is (was) running.
//
// taken from https://github.com/kubernetes/kubernetes/blob/release-1.27/pkg/util/node/node.go#L37
const (
	NodeUnreachablePodReason = "NodeLost"
	loadBalancerWidth        = 16
)

// AddHandlers adds print handlers for default Kubernetes types dealing with internal versions.
// TODO: handle errors from Handler
//
//nolint:lll
func AddHandlers(h printers.PrintHandler) {
	podColumnDefinitions := []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: metav1.ObjectMeta{}.SwaggerDoc()["name"]},
		{Name: "Ready", Type: "string", Description: "The aggregate readiness state of this pod for accepting traffic."},
		{Name: "Status", Type: "string", Description: "The aggregate status of the containers in this pod."},
		{Name: "Restarts", Type: "string", Description: "The number of times the containers in this pod have been restarted and when the last container in this pod has restarted."},
		{Name: "Age", Type: "string", Description: metav1.ObjectMeta{}.SwaggerDoc()["creationTimestamp"]},
		{Name: "IP", Type: "string", Priority: 1, Description: corev1.PodStatus{}.SwaggerDoc()["podIP"]},
		{Name: "Node", Type: "string", Priority: 1, Description: corev1.PodSpec{}.SwaggerDoc()["nodeName"]},
		{Name: "Nominated Node", Type: "string", Priority: 1, Description: corev1.PodStatus{}.SwaggerDoc()["nominatedNodeName"]},
		{Name: "Readiness Gates", Type: "string", Priority: 1, Description: corev1.PodSpec{}.SwaggerDoc()["readinessGates"]},
	}
	_ = h.TableHandler(podColumnDefinitions, printPodList)
	_ = h.TableHandler(podColumnDefinitions, printPod)

	serviceColumnDefinitions := []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: metav1.ObjectMeta{}.SwaggerDoc()["name"]},
		{Name: "Type", Type: "string", Description: corev1.ServiceSpec{}.SwaggerDoc()["type"]},
		{Name: "Cluster-IP", Type: "string", Description: corev1.ServiceSpec{}.SwaggerDoc()["clusterIP"]},
		{Name: "External-IP", Type: "string", Description: corev1.ServiceSpec{}.SwaggerDoc()["externalIPs"]},
		{Name: "Port(s)", Type: "string", Description: corev1.ServiceSpec{}.SwaggerDoc()["ports"]},
		{Name: "Age", Type: "string", Description: metav1.ObjectMeta{}.SwaggerDoc()["creationTimestamp"]},
		{Name: "Selector", Type: "string", Priority: 1, Description: corev1.ServiceSpec{}.SwaggerDoc()["selector"]},
	}
	_ = h.TableHandler(serviceColumnDefinitions, printService)
	_ = h.TableHandler(serviceColumnDefinitions, printServiceList)

	endpointSliceColumnDefinitions := []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: metav1.ObjectMeta{}.SwaggerDoc()["name"]},
		{Name: "AddressType", Type: "string", Description: discoveryv1.EndpointSlice{}.SwaggerDoc()["addressType"]},
		{Name: "Ports", Type: "string", Description: discoveryv1.EndpointSlice{}.SwaggerDoc()["ports"]},
		{Name: "Endpoints", Type: "string", Description: discoveryv1.EndpointSlice{}.SwaggerDoc()["endpoints"]},
		{Name: "Age", Type: "string", Description: metav1.ObjectMeta{}.SwaggerDoc()["creationTimestamp"]},
	}
	_ = h.TableHandler(endpointSliceColumnDefinitions, printEndpointSlice)
	_ = h.TableHandler(endpointSliceColumnDefinitions, printEndpointSliceList)
}

// translateTimestampSince returns the elapsed time since timestamp in
// human-readable approximation.
func translateTimestampSince(timestamp metav1.Time) string {
	if timestamp.IsZero() {
		return "<unknown>"
	}

	return duration.HumanDuration(time.Since(timestamp.Time))
}

//nolint:lll
var (
	podSuccessConditions = []metav1.TableRowCondition{{Type: metav1.RowCompleted, Status: metav1.ConditionTrue, Reason: string(corev1.PodSucceeded), Message: "The pod has completed successfully."}}
	podFailedConditions  = []metav1.TableRowCondition{{Type: metav1.RowCompleted, Status: metav1.ConditionTrue, Reason: string(corev1.PodFailed), Message: "The pod failed."}}
)

func printPodList(podList *corev1.PodList, options printers.GenerateOptions) ([]metav1.TableRow, error) {
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

//nolint:all
func printPod(pod *corev1.Pod, options printers.GenerateOptions) ([]metav1.TableRow, error) {
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
		if condition.Type == corev1.PodScheduled && condition.Reason == corev1.PodReasonSchedulingGated {
			reason = corev1.PodReasonSchedulingGated
		}
	}

	row := metav1.TableRow{
		Object: runtime.RawExtension{Object: pod},
	}

	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		row.Conditions = podSuccessConditions
	case corev1.PodFailed:
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
						if condition.Status == corev1.ConditionTrue {
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

func printService(obj *corev1.Service, options printers.GenerateOptions) ([]metav1.TableRow, error) {
	row := metav1.TableRow{
		Object: runtime.RawExtension{Object: obj},
	}
	svcType := obj.Spec.Type
	internalIP := "<none>"
	if len(obj.Spec.ClusterIPs) > 0 {
		internalIP = obj.Spec.ClusterIPs[0]
	}

	externalIP := getServiceExternalIP(obj, options.Wide)
	svcPorts := makePortString(obj.Spec.Ports)
	if len(svcPorts) == 0 {
		svcPorts = "<none>"
	}

	row.Cells = append(row.Cells, obj.Name, string(svcType), internalIP, externalIP, svcPorts, translateTimestampSince(obj.CreationTimestamp))
	if options.Wide {
		row.Cells = append(row.Cells, labels.FormatLabels(obj.Spec.Selector))
	}

	return []metav1.TableRow{row}, nil
}

func getServiceExternalIP(svc *corev1.Service, wide bool) string {
	switch svc.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		if len(svc.Spec.ExternalIPs) > 0 {
			return strings.Join(svc.Spec.ExternalIPs, ",")
		}
		return "<none>"
	case corev1.ServiceTypeNodePort:
		if len(svc.Spec.ExternalIPs) > 0 {
			return strings.Join(svc.Spec.ExternalIPs, ",")
		}
		return "<none>"
	case corev1.ServiceTypeLoadBalancer:
		lbIps := loadBalancerStatusStringer(svc.Status.LoadBalancer, wide)
		if len(svc.Spec.ExternalIPs) > 0 {
			results := []string{}
			if len(lbIps) > 0 {
				results = append(results, strings.Split(lbIps, ",")...)
			}
			results = append(results, svc.Spec.ExternalIPs...)
			return strings.Join(results, ",")
		}
		if len(lbIps) > 0 {
			return lbIps
		}
		return "<pending>"
	case corev1.ServiceTypeExternalName:
		return svc.Spec.ExternalName
	}
	return "<unknown>"
}

// loadBalancerStatusStringer behaves mostly like a string interface and converts the given status to a string.
// `wide` indicates whether the returned value is meant for --o=wide output. If not, it's clipped to 16 bytes.
func loadBalancerStatusStringer(s corev1.LoadBalancerStatus, wide bool) string {
	ingress := s.Ingress
	result := sets.NewString()
	for i := range ingress {
		if ingress[i].IP != "" {
			result.Insert(ingress[i].IP)
		} else if ingress[i].Hostname != "" {
			result.Insert(ingress[i].Hostname)
		}
	}

	r := strings.Join(result.List(), ",")
	if !wide && len(r) > loadBalancerWidth {
		r = r[0:(loadBalancerWidth-3)] + "..."
	}
	return r
}

func makePortString(ports []corev1.ServicePort) string {
	pieces := make([]string, len(ports))
	for ix := range ports {
		port := &ports[ix]
		pieces[ix] = fmt.Sprintf("%d/%s", port.Port, port.Protocol)
		if port.NodePort > 0 {
			pieces[ix] = fmt.Sprintf("%d:%d/%s", port.Port, port.NodePort, port.Protocol)
		}
	}
	return strings.Join(pieces, ",")
}

func printServiceList(list *corev1.ServiceList, options printers.GenerateOptions) ([]metav1.TableRow, error) {
	rows := make([]metav1.TableRow, 0, len(list.Items))
	for i := range list.Items {
		r, err := printService(&list.Items[i], options)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	return rows, nil
}

func printEndpointSlice(obj *discoveryv1.EndpointSlice, options printers.GenerateOptions) ([]metav1.TableRow, error) {
	row := metav1.TableRow{
		Object: runtime.RawExtension{Object: obj},
	}
	row.Cells = append(row.Cells, obj.Name, string(obj.AddressType), formatDiscoveryPorts(obj.Ports),
		formatDiscoveryEndpoints(obj.Endpoints), translateTimestampSince(obj.CreationTimestamp))
	return []metav1.TableRow{row}, nil
}

func printEndpointSliceList(list *discoveryv1.EndpointSliceList, options printers.GenerateOptions) ([]metav1.TableRow, error) {
	rows := make([]metav1.TableRow, 0, len(list.Items))
	for i := range list.Items {
		r, err := printEndpointSlice(&list.Items[i], options)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	return rows, nil
}

func formatDiscoveryPorts(ports []discoveryv1.EndpointPort) string {
	list := []string{}
	max := 3
	more := false
	count := 0
	for _, port := range ports {
		if len(list) < max {
			portNum := "*"
			if port.Port != nil {
				portNum = strconv.Itoa(int(*port.Port))
			} else if port.Name != nil {
				portNum = *port.Name
			}
			list = append(list, portNum)
		} else if len(list) == max {
			more = true
		}
		count++
	}
	return listWithMoreString(list, more, count, max)
}

func listWithMoreString(list []string, more bool, count, max int) string {
	ret := strings.Join(list, ",")
	if more {
		return fmt.Sprintf("%s + %d more...", ret, count-max)
	}
	if ret == "" {
		ret = "<unset>"
	}
	return ret
}

func formatDiscoveryEndpoints(endpoints []discoveryv1.Endpoint) string {
	list := []string{}
	max := 3
	more := false
	count := 0
	for _, endpoint := range endpoints {
		for _, address := range endpoint.Addresses {
			if len(list) < max {
				list = append(list, address)
			} else if len(list) == max {
				more = true
			}
			count++
		}
	}
	return listWithMoreString(list, more, count, max)
}

func hasPodReadyCondition(conditions []corev1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
