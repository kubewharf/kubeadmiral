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

package sourcefeedback

import (
	"reflect"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
)

const testValue = "{\"unavailableClusters\":[{\"cluster\":\"kubeadmiral-member-1\",\"validUntil\":\"2023-11-24T07:12:52Z\"}]}"

func generateFedObj(workload *unstructured.Unstructured) *fedcorev1a1.FederatedObject {
	rawTargetTemplate, _ := workload.MarshalJSON()
	return &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Generation: 1,
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: rawTargetTemplate},
		},
	}
}

func Test_PopulateAnnotations(t *testing.T) {
	testJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
		Status: batchv1.JobStatus{
			Active:    1,
			Succeeded: 1,
			Failed:    1,
		},
	}
	u1, err := runtime.DefaultUnstructuredConverter.ToUnstructured(testJob)
	if err != nil {
		t.Fatalf(err.Error())
	}
	unstructuredJob := &unstructured.Unstructured{Object: u1}
	fedObj := generateFedObj(unstructuredJob)
	annotations := map[string]string{
		common.AppliedMigrationConfigurationAnnotation: testValue,
		scheduler.SchedulingAnnotation:                 "{}",
	}
	fedObj.SetAnnotations(annotations)

	tests := []struct {
		name            string
		sourceObj       *unstructured.Unstructured
		fedObj          fedcorev1a1.GenericFederatedObject
		wantNeedUpdate  bool
		wantAnnotations map[string]string
	}{
		{
			name:            "feedback annotations",
			sourceObj:       unstructuredJob,
			fedObj:          fedObj,
			wantNeedUpdate:  true,
			wantAnnotations: annotations,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needUpdate := PopulateAnnotations(tt.sourceObj, tt.fedObj)
			if needUpdate != tt.wantNeedUpdate ||
				!reflect.DeepEqual(unstructuredJob.GetAnnotations(), tt.wantAnnotations) {
				t.Errorf("PopulateAnnotations() want (%v, %v), but got (%v, %v)",
					tt.wantNeedUpdate, tt.wantAnnotations, needUpdate, unstructuredJob.GetAnnotations())
			}
		})
	}
}

func Test_populateAnnotation(t *testing.T) {
	tests := []struct {
		name              string
		sourceAnnotations map[string]string
		fedAnnotations    map[string]string
		key               string
		wantNeedUpdate    bool
		wantAnnotations   map[string]string
	}{
		{
			name:              "sourceAnnotations is nil, fedAnnotations with anno",
			sourceAnnotations: nil,
			fedAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: "{}",
			},
			key:            common.AppliedMigrationConfigurationAnnotation,
			wantNeedUpdate: true,
			wantAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: "{}",
			},
		},
		{
			name:              "sourceAnnotations without anno, fedAnnotations with anno",
			sourceAnnotations: map[string]string{},
			fedAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: "{}",
			},
			key:            common.AppliedMigrationConfigurationAnnotation,
			wantNeedUpdate: true,
			wantAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: "{}",
			},
		},
		{
			name: "sourceAnnotations with anno, fedAnnotations with the same value",
			sourceAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: testValue,
			},
			fedAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: testValue,
			},
			key:            common.AppliedMigrationConfigurationAnnotation,
			wantNeedUpdate: false,
			wantAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: testValue,
			},
		},
		{
			name: "sourceAnnotations with anno, fedAnnotations with different value",
			sourceAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: "{}",
			},
			fedAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: testValue,
			},
			key:            common.AppliedMigrationConfigurationAnnotation,
			wantNeedUpdate: true,
			wantAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: testValue,
			},
		},
		{
			name: "sourceAnnotations with anno, fedAnnotations without anno",
			sourceAnnotations: map[string]string{
				common.AppliedMigrationConfigurationAnnotation: "{}",
			},
			fedAnnotations:  map[string]string{},
			key:             common.AppliedMigrationConfigurationAnnotation,
			wantNeedUpdate:  true,
			wantAnnotations: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAnnotations, needUpdate := populateAnnotation(tt.sourceAnnotations, tt.fedAnnotations, tt.key)
			if needUpdate != tt.wantNeedUpdate || !reflect.DeepEqual(gotAnnotations, tt.wantAnnotations) {
				t.Errorf("populateAnnotation() want (%v, %v), but got (%v, %v)", tt.wantNeedUpdate,
					tt.wantAnnotations, needUpdate, gotAnnotations)
			}
		})
	}
}
