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

package common

import (
	"context"
	"reflect"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const testValue = "{\"unavailableClusters\":[{\"cluster\":\"kubeadmiral-member-1\",\"validUntil\":\"2023-11-24T07:12:52Z\"}]}"

func generateFedObj(workload *unstructured.Unstructured) *fedcorev1a1.FederatedObject {
	rawTargetTemplate, _ := workload.MarshalJSON()
	return &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
			Generation:  1,
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: rawTargetTemplate},
		},
	}
}

func TestMigrationConfigPlugin(t *testing.T) {
	jobWithoutAnno := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "completed",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
		Status: batchv1.JobStatus{
			Active:    1,
			Succeeded: 1,
			Failed:    1,
		},
	}
	jobWithAnno := jobWithoutAnno.DeepCopy()
	jobWithAnno.Annotations[common.AppliedMigrationConfigurationAnnotation] = testValue
	jobEmptyAnnos := jobWithoutAnno.DeepCopy()
	jobEmptyAnnos.Annotations = nil

	u1, err := runtime.DefaultUnstructuredConverter.ToUnstructured(jobWithoutAnno)
	if err != nil {
		t.Fatalf(err.Error())
	}
	unstructuredJobWithoutAnno := &unstructured.Unstructured{Object: u1}
	fedObjWithoutAnno := generateFedObj(unstructuredJobWithoutAnno)

	u2, err := runtime.DefaultUnstructuredConverter.ToUnstructured(jobWithAnno)
	if err != nil {
		t.Fatalf(err.Error())
	}
	unstructuredJobWithAnno := &unstructured.Unstructured{Object: u2}
	fedObjWithAnno := fedObjWithoutAnno.DeepCopy()
	fedObjWithAnno.Annotations[common.AppliedMigrationConfigurationAnnotation] = ""

	u3, err := runtime.DefaultUnstructuredConverter.ToUnstructured(jobEmptyAnnos)
	if err != nil {
		t.Fatalf(err.Error())
	}
	unstructuredJobEmptyAnnos := &unstructured.Unstructured{Object: u3}

	ctx := klog.NewContext(context.Background(), klog.Background())
	receiver := NewMigrationConfigPlugin()

	tests := []struct {
		name           string
		sourceObj      *unstructured.Unstructured
		fedObj         *fedcorev1a1.FederatedObject
		wantNeedUpdate bool
	}{
		{
			name:           "sourceObj without anno, fedObj with anno",
			sourceObj:      unstructuredJobWithoutAnno,
			fedObj:         fedObjWithAnno,
			wantNeedUpdate: true,
		},
		{
			name:           "sourceObj with empty anno, fedObj with anno",
			sourceObj:      unstructuredJobEmptyAnnos,
			fedObj:         fedObjWithAnno,
			wantNeedUpdate: true,
		},
		{
			name:           "sourceObj with anno, fedObj with anno",
			sourceObj:      unstructuredJobWithAnno,
			fedObj:         fedObjWithAnno,
			wantNeedUpdate: true,
		},
		{
			name:           "sourceObj with anno, fedObj without anno",
			sourceObj:      unstructuredJobWithAnno,
			fedObj:         fedObjWithoutAnno,
			wantNeedUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, needUpdate, _ := receiver.AggregateStatuses(ctx, tt.sourceObj, tt.fedObj, nil, false)
			if !reflect.DeepEqual(needUpdate, tt.wantNeedUpdate) {
				t.Errorf("AggregateStatuses = %v, want %v", needUpdate, tt.wantNeedUpdate)
			}
		})
	}
}
