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
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Returns the number of unschedulable pods that remain
// unschedulable for more than unschedulableThreshold,
// and a time.Duration representing the time from now
// when the new unschedulable pod will cross the threshold, if any.
func countUnschedulablePods(
	podList *corev1.PodList,
	currentTime time.Time,
	unschedulableThreshold time.Duration,
) (unschedulableCount int, nextCrossIn *time.Duration) {
	for i := range podList.Items {
		pod := &podList.Items[i]

		if pod.GetDeletionTimestamp() != nil {
			continue
		}

		var scheduledCondition *corev1.PodCondition
		for i := range pod.Status.Conditions {
			condition := &pod.Status.Conditions[i]
			if condition.Type == corev1.PodScheduled {
				scheduledCondition = condition
				break
			}
		}
		if scheduledCondition == nil ||
			scheduledCondition.Status != corev1.ConditionFalse ||
			scheduledCondition.Reason != corev1.PodReasonUnschedulable {
			continue
		}

		timeBecameUnschedulable := scheduledCondition.LastTransitionTime
		timeCrossingThreshold := timeBecameUnschedulable.Add(unschedulableThreshold)
		crossingThresholdIn := timeCrossingThreshold.Sub(currentTime)
		if crossingThresholdIn <= 0 {
			unschedulableCount++
		} else if nextCrossIn == nil || *nextCrossIn > crossingThresholdIn {
			nextCrossIn = &crossingThresholdIn
		}
	}

	return unschedulableCount, nextCrossIn
}
