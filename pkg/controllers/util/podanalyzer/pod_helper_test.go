/*
Copyright 2016 The Kubernetes Authors.

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

package podanalyzer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAnalyze(t *testing.T) {
	now := time.Now()
	podRunning := newPod(t, "p1",
		corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		})
	podUnschedulable := newPod(t, "pU",
		corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionFalse,
					Reason:             corev1.PodReasonUnschedulable,
					LastTransitionTime: metav1.Time{Time: now.Add(-10 * time.Minute)},
				},
			},
		})
	podOther := newPod(t, "pO",
		corev1.PodStatus{
			Phase:      corev1.PodPending,
			Conditions: []corev1.PodCondition{},
		})

	result := AnalyzePods(
		&corev1.PodList{
			Items: []corev1.Pod{*podRunning, *podRunning, *podRunning, *podUnschedulable, *podUnschedulable},
		},
		now,
	)
	assert.Equal(t, PodAnalysisResult{
		Total:           5,
		RunningAndReady: 3,
		Unschedulable:   2,
	}, result)

	result = AnalyzePods(&corev1.PodList{Items: []corev1.Pod{*podOther}}, now)
	assert.Equal(t, PodAnalysisResult{
		Total:           1,
		RunningAndReady: 0,
		Unschedulable:   0,
	}, result)
}

func newPod(t *testing.T, name string, status corev1.PodStatus) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
		Status: status,
	}
}
