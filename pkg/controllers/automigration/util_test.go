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

package automigration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func doCheck(
	t *testing.T,
	now time.Time,
	threshold time.Duration,
	pods []corev1.Pod,
	expectedUnschedulable int,
	expectedNextCrossIn *time.Duration,
) {
	t.Helper()
	assert := assert.New(t)

	unschedulableCount, nextCrossIn := countUnschedulablePods(
		&corev1.PodList{Items: pods}, now, threshold,
	)
	assert.Equal(expectedUnschedulable, unschedulableCount)
	assert.Equal(expectedNextCrossIn, nextCrossIn)
}

func TestCountUnschedulablePods(t *testing.T) {
	now := time.Now()
	threshold := time.Minute

	okPod := newPod(false, true, now)
	unschedulablePod := newPod(false, false, now.Add(-2*threshold))
	unschedulableTerminatingPod := newPod(true, false, now.Add(-2*threshold))
	crossingIn10s := newPod(false, false, now.Add(10*time.Second-threshold))
	crossingIn20s := newPod(false, false, now.Add(20*time.Second-threshold))

	doCheck(t, now, time.Minute, []corev1.Pod{
		*okPod,
		*okPod,
		*okPod,
	}, 0, nil)

	doCheck(t, now, time.Minute, []corev1.Pod{
		*okPod,
		*okPod,
		*unschedulablePod,
	}, 1, nil)

	doCheck(t, now, time.Minute, []corev1.Pod{
		*okPod,
		*okPod,
		*crossingIn10s,
	}, 0, pointer.Duration(10*time.Second))

	doCheck(t, now, time.Minute, []corev1.Pod{
		*okPod,
		*okPod,
		*unschedulablePod,
		*crossingIn20s,
	}, 1, pointer.Duration(20*time.Second))

	doCheck(t, now, time.Minute, []corev1.Pod{
		*okPod,
		*okPod,
		*unschedulablePod,
		*unschedulablePod,
		*crossingIn10s,
		*crossingIn20s,
	}, 2, pointer.Duration(10*time.Second))

	doCheck(t, now, time.Minute, []corev1.Pod{
		*okPod,
		*okPod,
		*unschedulablePod,
		*unschedulableTerminatingPod,
		*crossingIn10s,
		*crossingIn20s,
	}, 1, pointer.Duration(10*time.Second))
}

func newPod(terminating bool, schedulable bool, lastTransitionTimestamp time.Time) *corev1.Pod {
	condition := corev1.PodCondition{
		Type:               corev1.PodScheduled,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: metav1.Time{Time: lastTransitionTimestamp},
	}
	if !schedulable {
		condition.Status = corev1.ConditionFalse
		condition.Reason = corev1.PodReasonUnschedulable
	}
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "pod",
			APIVersion: "v1",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{condition},
		},
	}
	if terminating {
		pod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	}
	return pod
}
