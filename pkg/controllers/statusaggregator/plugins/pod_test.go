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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func TestPodPlugin(t *testing.T) {
	testTime := time.Now()

	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "failed-0",
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodFailed,
			StartTime:             &metav1.Time{Time: testTime.Add(-10 * time.Second)},
			InitContainerStatuses: []corev1.ContainerStatus{generateContainerStatus("failed-init", true, testTime)},
			ContainerStatuses:     []corev1.ContainerStatus{generateContainerStatus("failed", true, testTime)},
		},
	}
	u1, err := runtime.DefaultUnstructuredConverter.ToUnstructured(failedPod)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uFailedPod := &unstructured.Unstructured{Object: u1}

	pendingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pending-0",
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodPending,
			StartTime:             &metav1.Time{Time: testTime.Add(-20 * time.Second)},
			InitContainerStatuses: []corev1.ContainerStatus{generateContainerStatus("pending-init", true, testTime)},
		},
	}
	u2, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pendingPod)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uPendingPod := &unstructured.Unstructured{Object: u2}

	runningPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "running-0",
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodRunning,
			StartTime:             &metav1.Time{Time: testTime.Add(-30 * time.Second)},
			InitContainerStatuses: []corev1.ContainerStatus{generateContainerStatus("running-init", true, testTime)},
			ContainerStatuses:     []corev1.ContainerStatus{generateContainerStatus("running", true, testTime)},
		},
	}
	u3, err := runtime.DefaultUnstructuredConverter.ToUnstructured(runningPod)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uRunningPod := &unstructured.Unstructured{Object: u3}

	succeededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "succeeded-0",
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodSucceeded,
			StartTime:             &metav1.Time{Time: testTime.Add(-40 * time.Second)},
			InitContainerStatuses: []corev1.ContainerStatus{generateContainerStatus("succeeded-init", true, testTime)},
			ContainerStatuses:     []corev1.ContainerStatus{generateContainerStatus("succeeded", true, testTime)},
		},
	}
	u4, err := runtime.DefaultUnstructuredConverter.ToUnstructured(succeededPod)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uSucceededPod := &unstructured.Unstructured{Object: u4}

	emptyPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "empty-0"}}
	u5, err := runtime.DefaultUnstructuredConverter.ToUnstructured(emptyPod)
	if err != nil {
		t.Fatalf(err.Error())
	}
	uEmptyPod := &unstructured.Unstructured{Object: u5}

	tests := []struct {
		name string

		sourceObject *unstructured.Unstructured
		fedObject    fedcorev1a1.GenericFederatedObject
		clusterObjs  map[string]interface{}

		expectedErr        error
		expectedNeedUpdate bool
		expectedPodStatus  *corev1.PodStatus
	}{
		{
			name:               "2 failed pods, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uFailedPod.DeepCopy(), "c2": uFailedPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodFailed,
				Message:   "Pod failed in clusters [c1,c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-10 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("failed-init (c1)", true, testTime),
					generateContainerStatus("failed-init (c2)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("failed (c1)", true, testTime),
					generateContainerStatus("failed (c2)", true, testTime),
				},
			},
		},
		{
			name:               "2 pending pods, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uPendingPod.DeepCopy(), "c2": uPendingPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodPending,
				Message:   "Pod pending in clusters [c1,c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-20 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("pending-init (c1)", true, testTime),
					generateContainerStatus("pending-init (c2)", true, testTime),
				},
			},
		},
		{
			name:               "2 running pods, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uRunningPod.DeepCopy(), "c2": uRunningPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodRunning,
				Message:   "Pod running in clusters [c1,c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-30 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("running-init (c1)", true, testTime),
					generateContainerStatus("running-init (c2)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("running (c1)", true, testTime),
					generateContainerStatus("running (c2)", true, testTime),
				},
			},
		},
		{
			name:               "2 succeeded pods, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uSucceededPod.DeepCopy(), "c2": uSucceededPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodSucceeded,
				Message:   "Pod succeeded in clusters [c1,c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-40 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("succeeded-init (c1)", true, testTime),
					generateContainerStatus("succeeded-init (c2)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("succeeded (c1)", true, testTime),
					generateContainerStatus("succeeded (c2)", true, testTime),
				},
			},
		},
		{
			name:               "1 failed pod, 1 succeeded pod, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uFailedPod.DeepCopy(), "c2": uSucceededPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodFailed,
				Message:   "Pod failed in clusters [c1], and succeeded in clusters [c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-40 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("failed-init (c1)", true, testTime),
					generateContainerStatus("succeeded-init (c2)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("failed (c1)", true, testTime),
					generateContainerStatus("succeeded (c2)", true, testTime),
				},
			},
		},
		{
			name:               "1 pending pod, 1 running pod, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uPendingPod.DeepCopy(), "c2": uRunningPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodPending,
				Message:   "Pod pending in clusters [c1], and running in clusters [c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-30 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("pending-init (c1)", true, testTime),
					generateContainerStatus("running-init (c2)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("running (c2)", true, testTime),
				},
			},
		},
		{
			name:               "1 pending pod, 1 empty pod, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uPendingPod.DeepCopy(), "c2": uEmptyPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodPending,
				Message:   "Pod pending in clusters [c1,c2]",
				StartTime: &metav1.Time{Time: testTime.Add(-20 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("pending-init (c1)", true, testTime),
				},
			},
		},
		{
			name:               "1 running pod, 1 empty pod, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uRunningPod.DeepCopy(), "c2": uEmptyPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodPending,
				Message:   "Pod pending in clusters [c2], and running in clusters [c1]",
				StartTime: &metav1.Time{Time: testTime.Add(-30 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("running-init (c1)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("running (c1)", true, testTime),
				},
			},
		},
		{
			name:               "2 empty pods, need update",
			sourceObject:       uEmptyPod.DeepCopy(),
			fedObject:          nil,
			clusterObjs:        map[string]interface{}{"c1": uEmptyPod.DeepCopy(), "c2": uEmptyPod.DeepCopy()},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:   corev1.PodPending,
				Message: "Pod pending in clusters [c1,c2]",
			},
		},
		{
			name:         "1 failed pod, 1 pending pod, 1 running pod, 1 succeeded pod, need update",
			sourceObject: uEmptyPod.DeepCopy(),
			fedObject:    nil,
			clusterObjs: map[string]interface{}{
				"c1": uFailedPod.DeepCopy(),
				"c2": uPendingPod.DeepCopy(),
				"c3": uRunningPod.DeepCopy(),
				"c4": uSucceededPod.DeepCopy(),
			},
			expectedErr:        nil,
			expectedNeedUpdate: true,
			expectedPodStatus: &corev1.PodStatus{
				Phase:     corev1.PodFailed,
				Message:   "Pod failed in clusters [c1], and pending in clusters [c2], and running in clusters [c3], and succeeded in clusters [c4]",
				StartTime: &metav1.Time{Time: testTime.Add(-40 * time.Second)},
				InitContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("failed-init (c1)", true, testTime),
					generateContainerStatus("pending-init (c2)", true, testTime),
					generateContainerStatus("running-init (c3)", true, testTime),
					generateContainerStatus("succeeded-init (c4)", true, testTime),
				},
				ContainerStatuses: []corev1.ContainerStatus{
					generateContainerStatus("failed (c1)", true, testTime),
					generateContainerStatus("running (c3)", true, testTime),
					generateContainerStatus("succeeded (c4)", true, testTime),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := klog.NewContext(context.Background(), klog.Background())
			receiver := NewPodPlugin()

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

			uExpectedPodStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tt.expectedPodStatus)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}

			uGotStatus, _, _ := unstructured.NestedMap(got.Object, "status")
			if !reflect.DeepEqual(uExpectedPodStatus, uGotStatus) {
				t.Fatalf("got podStatus: %v, expected podStatus: %v", uGotStatus, uExpectedPodStatus)
			}
		})
	}
}

func generateContainerStatus(name string, started bool, testTime time.Time) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name: name,
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{
				StartedAt: metav1.NewTime(testTime),
			},
		},
		Ready:        true,
		RestartCount: 0,
		Image:        "docker.io/library/nginx:alpine",
		ImageID:      "docker.io/library/nginx@sha256:2e776a66a3556f001aba13431b26e448fe8acba277bf93d2ab1a785571a46d90",
		ContainerID:  "containerd://664ec405ade16720f5dd08e827791a9bf9cd190b383f645c7d506136b64752ca",
		Started:      &started,
	}
}
