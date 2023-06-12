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
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type JobPlugin struct{}

func NewJobPlugin() *JobPlugin {
	return &JobPlugin{}
}

func (receiver *JobPlugin) AggregateStatuses(
	ctx context.Context,
	sourceObject, fedObject *unstructured.Unstructured,
	clusterObjs map[string]interface{},
	clusterObjsUpToDate bool,
) (*unstructured.Unstructured, bool, error) {
	logger := klog.FromContext(ctx).WithValues("status-aggregator-plugin", "jobs")

	aggregatedStatus := &batchv1.JobStatus{}
	var completionTime *metav1.Time
	var finishedCount int
	var completedJobs, failedJobs []string

	for clusterName, clusterObj := range clusterObjs {
		logger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)

		utd := clusterObj.(*unstructured.Unstructured)
		// For status of job
		var found bool
		status, found, err := unstructured.NestedMap(utd.Object, common.StatusField)
		if err != nil || !found {
			logger.Error(err, "Failed to get status of cluster object")
			return nil, false, err
		}

		if status == nil {
			continue
		}

		jobStatus := &batchv1.JobStatus{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(status, jobStatus); err != nil {
			logger.Error(err, "Failed to convert the status of cluster object")
			return nil, false, err
		}

		if aggregatedStatus.StartTime.IsZero() || jobStatus.StartTime.Before(aggregatedStatus.StartTime) {
			aggregatedStatus.StartTime = jobStatus.StartTime
		}
		if !jobStatus.CompletionTime.IsZero() {
			finishedCount++
			completedJobs = append(completedJobs, clusterName)
			if completionTime.IsZero() || completionTime.Before(jobStatus.CompletionTime) {
				completionTime = jobStatus.CompletionTime
			}
		} else if IsJobFinishedWithFailed(jobStatus) {
			finishedCount++
			failedJobs = append(failedJobs, clusterName)
		}

		aggregatedStatus.Active += jobStatus.Active
		aggregatedStatus.Succeeded += jobStatus.Succeeded
		aggregatedStatus.Failed += jobStatus.Failed
	}
	if finishedCount > 0 && finishedCount == len(clusterObjs) {
		now := time.Now()
		var conditionType batchv1.JobConditionType
		var reason, message string
		sort.Strings(completedJobs)
		sort.Strings(failedJobs)

		switch {
		case len(completedJobs) > 0 && len(failedJobs) > 0:
			conditionType = batchv1.JobFailed
			reason = "Mixed"
			message = fmt.Sprintf("Job completed in clusters [%s] and failed in member clusters [%s]",
				strings.Join(completedJobs, ","), strings.Join(failedJobs, ","))
		case len(completedJobs) > 0:
			conditionType = batchv1.JobComplete
			reason = "Completed"
			message = fmt.Sprintf("Job completed in clusters [%s]", strings.Join(completedJobs, ","))
			aggregatedStatus.CompletionTime = completionTime
		default:
			conditionType = batchv1.JobFailed
			reason = "Failed"
			message = fmt.Sprintf("Job failed in clusters [%s]", strings.Join(failedJobs, ","))
		}

		aggregatedStatus.Conditions = append(aggregatedStatus.Conditions, batchv1.JobCondition{
			Type:               conditionType,
			Status:             corev1.ConditionTrue,
			LastProbeTime:      metav1.NewTime(now),
			LastTransitionTime: metav1.NewTime(now),
			Reason:             reason,
			Message:            message,
		})
	}

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
	needUpdate := false
	if !reflect.DeepEqual(newStatus, oldStatus) {
		if err := unstructured.SetNestedMap(sourceObject.Object, newStatus, common.StatusField); err != nil {
			logger.Error(err, "Failed to set the new status on source object")
			return nil, false, err
		}
		needUpdate = true
	}

	return sourceObject, needUpdate, nil
}

// IsJobFinishedWithFailed checks whether the given Job has finished execution with failed condition.
func IsJobFinishedWithFailed(jobStatus *batchv1.JobStatus) bool {
	for _, c := range jobStatus.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
