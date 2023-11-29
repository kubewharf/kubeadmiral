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

package scheduler

import (
	"encoding/json"

	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/core"
)

var SchedulingAnnotation = common.DefaultPrefix + "scheduling"

type Scheduling struct {
	// SourceGeneration is the generation of the source object
	// observed in the federated object when this placement is sampled.
	SourceGeneration int64 `json:"sourceGeneration"`

	// FederatedGeneration is the generation of the federated object
	// observed when this placement is sampled.
	FederatedGeneration int64 `json:"fedGeneration"`

	// Placement contains a list of FederatedCluster object names and replicas on them.
	Placement map[string]*int64 `json:"placement,omitempty"`
}

func getSchedulingAnnotationValue(
	fedObject fedcorev1a1.GenericFederatedObject,
	result core.ScheduleResult,
) (value string, err error) {
	scheduling := Scheduling{}

	srcMeta, err := fedObject.GetSpec().GetTemplateMetadata()
	if err != nil {
		return "", err
	}

	scheduling.SourceGeneration = srcMeta.GetGeneration()
	scheduling.FederatedGeneration = fedObject.GetGeneration()
	scheduling.Placement = result.SuggestedClusters

	jsonBuf, err := json.Marshal(scheduling)
	if err != nil {
		klog.Errorf("Cannot marshal JSON: %v", err)
		return "", err
	}

	return string(jsonBuf), nil
}
