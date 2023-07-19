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
	"reflect"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func TestJobPlugin(t *testing.T) {
	testTime := time.Now()

	completedJobStartTime := metav1.NewTime(testTime.Add(-10 * time.Second))
	completedJobCompletionTime := metav1.NewTime(testTime)
	completedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "completed",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions:     []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
			StartTime:      completedJobStartTime.DeepCopy(),
			CompletionTime: completedJobCompletionTime.DeepCopy(),
			Active:         1,
			Succeeded:      1,
			Failed:         1,
		},
	}
	u1, err := runtime.DefaultUnstructuredConverter.ToUnstructured(completedJob)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uCompletedJob := &unstructured.Unstructured{Object: u1}

	failedJobStartTime := metav1.NewTime(testTime.Add(-15 * time.Second))
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "failed",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}},
			StartTime:  failedJobStartTime.DeepCopy(),
			Active:     0,
			Succeeded:  0,
			Failed:     1,
		},
	}
	u2, err := runtime.DefaultUnstructuredConverter.ToUnstructured(failedJob)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uFailedJob := &unstructured.Unstructured{Object: u2}

	suspendedJobStartTime := metav1.NewTime(testTime.Add(-15 * time.Second))
	suspendedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "suspended",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobSuspended, Status: corev1.ConditionTrue}},
			StartTime:  suspendedJobStartTime.DeepCopy(),
		},
	}
	u3, err := runtime.DefaultUnstructuredConverter.ToUnstructured(suspendedJob)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uSuspendedJob := &unstructured.Unstructured{Object: u3}

	tests := []struct {
		name string

		sourceObject *unstructured.Unstructured
		fedObject    fedcorev1a1.GenericFederatedObject
		clusterObjs  map[string]interface{}

		expectedErr        error
		expectedNeedUpdate bool
		expectedJobStatus  *batchv1.JobStatus
	}{
		{
			name:               "2 completed jobs, need update",
			sourceObject:       uCompletedJob.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uCompletedJob.DeepCopy(), "c2": uCompletedJob.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedJobStatus: &batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{
					Type:    batchv1.JobComplete,
					Status:  corev1.ConditionTrue,
					Reason:  "Completed",
					Message: "Job completed in clusters [c1,c2]",
				}},
				StartTime:      completedJobStartTime.DeepCopy(),
				CompletionTime: completedJobCompletionTime.DeepCopy(),
				Active:         2,
				Succeeded:      2,
				Failed:         2,
			},
		},
		{
			name:               "1 completed job, 1 failed job, need update",
			sourceObject:       uCompletedJob.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uCompletedJob.DeepCopy(), "c2": uFailedJob.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedJobStatus: &batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Reason:  "Mixed",
					Message: "Job completed in clusters [c1] and failed in member clusters [c2]",
				}},
				StartTime: failedJobStartTime.DeepCopy(),
				Active:    1,
				Succeeded: 1,
				Failed:    2,
			},
		},
		{
			name:               "2 failed jobs, need update",
			sourceObject:       uCompletedJob.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uFailedJob.DeepCopy(), "c2": uFailedJob.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedJobStatus: &batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Reason:  "Failed",
					Message: "Job failed in clusters [c1,c2]",
				}},
				StartTime: failedJobStartTime.DeepCopy(),
				Active:    0,
				Succeeded: 0,
				Failed:    2,
			},
		},
		{
			name:               "1 completed job, 1 suspended job, need update",
			sourceObject:       uCompletedJob.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uCompletedJob.DeepCopy(), "c2": uSuspendedJob.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedJobStatus: &batchv1.JobStatus{
				StartTime: suspendedJobStartTime.DeepCopy(),
				Active:    1,
				Succeeded: 1,
				Failed:    1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := klog.NewContext(context.Background(), klog.Background())
			receiver := NewJobPlugin()

			got, needUpdate, err := receiver.AggregateStatuses(ctx, tt.sourceObject, tt.fedObject, tt.clusterObjs, false)
			if !errors.Is(err, tt.expectedErr) {
				t.Fatalf("got error: %v, expectedErr: %v", err, tt.expectedErr)
			}
			// if err was expected, just pass it
			if err != nil {
				return
			}
			if needUpdate != tt.expectedNeedUpdate {
				t.Fatalf("got needUpdate: %v, expectedNeedUpdate: %v", needUpdate, tt.expectedNeedUpdate)
			}

			if conds, found, err := unstructured.NestedSlice(got.Object, "status", "conditions"); err != nil {
				t.Fatalf("unexpected err: %v", err)
			} else if found {
				// ignore lastProbeTime and lastTransitionTime of conditions
				// to avoid time conflict due to processor processing
				for i := range conds {
					err = unstructured.SetNestedField(conds[i].(map[string]interface{}), nil, "lastProbeTime")
					if err != nil {
						t.Fatalf("unexpected err: %v", err)
					}
					err = unstructured.SetNestedField(conds[i].(map[string]interface{}), nil, "lastTransitionTime")
					if err != nil {
						t.Fatalf("unexpected err: %v", err)
					}
				}
				err = unstructured.SetNestedField(got.Object, conds, "status", "conditions")
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			}
			uExpectedJobStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tt.expectedJobStatus)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}

			uGotStatus, _, _ := unstructured.NestedMap(got.Object, "status")
			if !reflect.DeepEqual(uExpectedJobStatus, uGotStatus) {
				t.Fatalf("got jobStatus: %v, expected jobStatus: %v", uGotStatus, uExpectedJobStatus)
			}
		})
	}
}

func TestIsJobFinishedWithFailed(t *testing.T) {
	testCases := map[string]struct {
		conditionType               batchv1.JobConditionType
		conditionStatus             corev1.ConditionStatus
		expectJobFinishedWithFailed bool
	}{
		"Job is completed and condition is true": {
			batchv1.JobComplete,
			corev1.ConditionTrue,
			false,
		},
		"Job is completed and condition is false": {
			batchv1.JobComplete,
			corev1.ConditionFalse,
			false,
		},
		"Job is completed and condition is unknown": {
			batchv1.JobComplete,
			corev1.ConditionUnknown,
			false,
		},

		"Job is failed and condition is true": {
			batchv1.JobFailed,
			corev1.ConditionTrue,
			true,
		},
		"Job is failed and condition is false": {
			batchv1.JobFailed,
			corev1.ConditionFalse,
			false,
		},
		"Job is failed and condition is unknown": {
			batchv1.JobFailed,
			corev1.ConditionUnknown,
			false,
		},

		"Job is suspended and condition is true": {
			batchv1.JobSuspended,
			corev1.ConditionTrue,
			false,
		},
		"Job is suspended and condition is false": {
			batchv1.JobSuspended,
			corev1.ConditionFalse,
			false,
		},
		"Job is suspended and condition is unknown": {
			batchv1.JobSuspended,
			corev1.ConditionUnknown,
			false,
		},

		"Job is about to fail its execution and condition is true": {
			batchv1.JobFailureTarget,
			corev1.ConditionTrue,
			false,
		},
		"Job is about to fail its execution and condition is false": {
			batchv1.JobFailureTarget,
			corev1.ConditionFalse,
			false,
		},
		"Job is about to fail its execution and condition is unknown": {
			batchv1.JobFailureTarget,
			corev1.ConditionUnknown,
			false,
		},
	}

	for name, tc := range testCases {
		status := &batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:   tc.conditionType,
				Status: tc.conditionStatus,
			}},
		}

		if tc.expectJobFinishedWithFailed != IsJobFinishedWithFailed(status) {
			t.Errorf("test name: %s, job was not expected", name)
		}
	}
}
