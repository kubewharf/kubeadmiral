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

package plugins

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type PodPlugin struct{}

func NewPodPlugin() *PodPlugin {
	return &PodPlugin{}
}

func (receiver *PodPlugin) AggregateStatuses(
	ctx context.Context,
	sourceObject *unstructured.Unstructured,
	fedObject fedcorev1a1.GenericFederatedObject,
	clusterObjs map[string]interface{},
	clusterObjsUpToDate bool,
) (*unstructured.Unstructured, bool, error) {
	logger := klog.FromContext(ctx).WithValues("status-aggregator-plugin", "pods")

	needUpdate := false
	aggregatedStatus := &corev1.PodStatus{}
	phases := map[corev1.PodPhase][]string{
		corev1.PodPending:   make([]string, 0, len(clusterObjs)),
		corev1.PodRunning:   make([]string, 0, len(clusterObjs)),
		corev1.PodSucceeded: make([]string, 0, len(clusterObjs)),
		corev1.PodFailed:    make([]string, 0, len(clusterObjs)),
		// Ignore phase PodUnknown as it has been deprecated since year 2015.
	}

	for clusterName, clusterObj := range clusterObjs {
		logger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)

		utd := clusterObj.(*unstructured.Unstructured)
		// For status of pod
		var found bool
		status, found, err := unstructured.NestedMap(utd.Object, common.StatusField)
		if err != nil || !found {
			logger.Error(err, "Failed to get status of cluster object")
			return nil, false, err
		}

		if status == nil {
			continue
		}

		podStatus := &corev1.PodStatus{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(status, podStatus); err != nil {
			logger.Error(err, "Failed to convert the status of cluster object")
			return nil, false, err
		}

		if podStatus.Phase == "" {
			// If the status has not yet been generated, set it to pending as default.
			phases[corev1.PodPending] = append(phases[corev1.PodPending], clusterName)
			continue
		}

		phases[podStatus.Phase] = append(phases[podStatus.Phase], clusterName)
		if aggregatedStatus.StartTime.IsZero() || podStatus.StartTime.Before(aggregatedStatus.StartTime) {
			aggregatedStatus.StartTime = podStatus.StartTime
		}
		for _, initContainerStatus := range podStatus.InitContainerStatuses {
			initContainerStatus.Name = fmt.Sprintf("%s (%s)", initContainerStatus.Name, clusterName)
			aggregatedStatus.InitContainerStatuses = append(aggregatedStatus.InitContainerStatuses, initContainerStatus)
		}
		for _, containerStatus := range podStatus.ContainerStatuses {
			containerStatus.Name = fmt.Sprintf("%s (%s)", containerStatus.Name, clusterName)
			aggregatedStatus.ContainerStatuses = append(aggregatedStatus.ContainerStatuses, containerStatus)
		}
	}

	// Check phase in order: PodFailed-->PodPending-->PodRunning-->PodSucceeded.
	for _, phase := range []corev1.PodPhase{corev1.PodFailed, corev1.PodPending, corev1.PodRunning, corev1.PodSucceeded} {
		if len(phases[phase]) == 0 {
			continue
		}
		if aggregatedStatus.Phase == "" {
			aggregatedStatus.Phase = phase
		}
		sort.Strings(phases[phase])
		if aggregatedStatus.Message == "" {
			aggregatedStatus.Message = fmt.Sprintf("Pod %s in clusters [%s]",
				strings.ToLower(string(phase)), strings.Join(phases[phase], ","))
			continue
		}
		aggregatedStatus.Message = fmt.Sprintf("%s, and %s in clusters [%s]",
			aggregatedStatus.Message, strings.ToLower(string(phase)), strings.Join(phases[phase], ","))
	}
	if len(phases) > 4 {
		logger.Error(errors.New("unknown pod phases"),
			fmt.Sprintf("Should not happen: %v. Maybe Pod added a new state that KubeAdmiral doesn't know about", phases))
	}

	// make results stable
	sort.Slice(aggregatedStatus.InitContainerStatuses, func(i, j int) bool {
		return aggregatedStatus.InitContainerStatuses[i].Name < aggregatedStatus.InitContainerStatuses[j].Name
	})
	sort.Slice(aggregatedStatus.ContainerStatuses, func(i, j int) bool {
		return aggregatedStatus.ContainerStatuses[i].Name < aggregatedStatus.ContainerStatuses[j].Name
	})

	newStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(aggregatedStatus)
	if err != nil {
		logger.Error(err, "Failed to convert aggregated status to unstructured")
		return nil, false, err
	}

	oldStatus, _, err := unstructured.NestedMap(sourceObject.Object, common.StatusField)
	if err != nil {
		logger.Error(err, "Failed to get old status of source object")
		return nil, false, err
	}

	// update status of source object if needed
	if !reflect.DeepEqual(newStatus, oldStatus) {
		if err := unstructured.SetNestedMap(sourceObject.Object, newStatus, common.StatusField); err != nil {
			logger.Error(err, "Failed to set the new status on source object")
			return nil, false, err
		}
		needUpdate = true
	}

	return sourceObject, needUpdate, nil
}
