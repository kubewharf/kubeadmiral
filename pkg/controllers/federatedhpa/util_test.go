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

package federatedhpa

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
)

func TestFedObjectToSourceObjectResource(t *testing.T) {
	hpaObject := &autoscalingv1.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}
	hpaJSON, err := json.Marshal(hpaObject)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Create a fedcorev1a1.GenericFederatedObject
	fedObj := &fedcorev1a1.FederatedObject{
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{
				Raw: hpaJSON,
			},
		},
	}

	// Call the fedObjectToSourceObjectResource function
	resource, err := fedObjectToSourceObjectResource(fedObj)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Assert the expected values of the Resource fields
	expectedResource := Resource{
		name:      "test",
		namespace: "default",
		gvk:       schema.GroupVersionKind{Group: "autoscaling", Version: "v1", Kind: "HorizontalPodAutoscaler"},
	}
	if resource.name != expectedResource.name {
		t.Errorf("Expected name %q, but got %q", expectedResource.name, resource.name)
	}
	if resource.namespace != expectedResource.namespace {
		t.Errorf("Expected namespace %q, but got %q", expectedResource.namespace, resource.namespace)
	}
}

func TestPolicyObjectToResource(t *testing.T) {
	// Create a dummy fedcorev1a1.PropagationPolicy object
	propagationPolicy := &fedcorev1a1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ppObject",
			Namespace: "default",
		},
	}

	// Call the policyObjectToResource function
	resource := policyObjectToResource(propagationPolicy)

	// Assert the expected values of the Resource fields
	expectedResource := Resource{
		name:      "ppObject",
		namespace: "default",
		gvk: schema.GroupVersionKind{
			Group:   fedcorev1a1.SchemeGroupVersion.Group,
			Version: fedcorev1a1.SchemeGroupVersion.Version,
			Kind:    PropagationPolicyKind,
		},
	}
	if resource.name != expectedResource.name {
		t.Errorf("Expected name %q, but got %q", expectedResource.name, resource.name)
	}
	if resource.namespace != expectedResource.namespace {
		t.Errorf("Expected namespace %q, but got %q", expectedResource.namespace, resource.namespace)
	}
}

func TestPolicyObjectToResource_ClusterPropagationPolicy(t *testing.T) {
	// Create a dummy fedcorev1a1.ClusterPropagationPolicy object
	clusterPropagationPolicy := &fedcorev1a1.ClusterPropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cpp",
		},
	}

	// Call the policyObjectToResource function
	resource := policyObjectToResource(clusterPropagationPolicy)

	// Assert the expected values of the Resource fields
	expectedResource := Resource{
		name:      "cpp",
		namespace: "",
		gvk: schema.GroupVersionKind{
			Group:   fedcorev1a1.SchemeGroupVersion.Group,
			Version: fedcorev1a1.SchemeGroupVersion.Version,
			Kind:    PropagationPolicyKind,
		},
	}
	if resource.name != expectedResource.name {
		t.Errorf("Expected name %q, but got %q", expectedResource.name, resource.name)
	}
	if resource.namespace != expectedResource.namespace {
		t.Errorf("Expected namespace %q, but got %q", expectedResource.namespace, resource.namespace)
	}
}

func TestGenerateCentralizedHPANotWorkReason(t *testing.T) {
	// Test case 1: newPPResource is nil
	reasons1 := generateCentralizedHPANotWorkReason(nil, &fedcorev1a1.PropagationPolicy{})
	expectedResult1 := "[The workload is not bound to any propagationPolicy.]"
	if reasons1 != expectedResult1 {
		t.Errorf("Expected %q, but got %q", expectedResult1, reasons1)
	}

	// Test case 2: PropagationPolicy does not exist
	reasons2 := generateCentralizedHPANotWorkReason(&Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp1"}, nil)
	expectedResult2 := "[The Policy pp1 bound to the workload does not exist.]"
	if reasons2 != expectedResult2 {
		t.Errorf("Expected %q, but got %q", expectedResult2, reasons2)
	}

	// Test case 3: PropagationPolicy exists but not in Divided mode
	reasons3 := generateCentralizedHPANotWorkReason(&Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp2"},
		&fedcorev1a1.PropagationPolicy{
			Spec: fedcorev1a1.PropagationPolicySpec{SchedulingMode: fedcorev1a1.SchedulingModeDuplicate},
		})
	expectedResult3 := "[The Policy pp2 bound to the workload is not Divided mode.]"
	if reasons3 != expectedResult3 {
		t.Errorf("Expected %q, but got %q", expectedResult3, reasons3)
	}
}

func TestGenerateDistributedHPANotWorkReason(t *testing.T) {
	ctx := context.TODO()

	retainObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.RetainReplicasAnnotation: common.AnnotationValueTrue,
			},
			Name:      "retain",
			Namespace: "default",
		},
	}
	followObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.FollowersAnnotation: `[{"group": "autoscaling", "kind": "HorizontalPodAutoscaler", "name": "test"}]`,
			},
			Name:      "follow",
			Namespace: "default",
		},
	}
	retainAndFollowObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.FollowersAnnotation:      `[{"group": "autoscaling", "kind": "HorizontalPodAutoscaler", "name": "test"}]`,
				common.RetainReplicasAnnotation: common.AnnotationValueTrue,
			},
			Name:      "retainAndFollow",
			Namespace: "default",
		},
	}

	hpaObj := &autoscalingv1.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}
	hpaUnsMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(hpaObj)
	hpaUns := &unstructured.Unstructured{Object: hpaUnsMap}

	// Test case 1: Workload is not enabled for retain replicas
	reasons5 := generateDistributedHPANotWorkReason(ctx, &Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp4"},
		&fedcorev1a1.PropagationPolicy{
			Spec: fedcorev1a1.PropagationPolicySpec{SchedulingMode: fedcorev1a1.SchedulingModeDuplicate},
		}, followObj, hpaUns)
	expectedResult5 := "[The workload is not enabled for retain replicas.]"
	if reasons5 != expectedResult5 {
		t.Errorf("Expected %q, but got %q", expectedResult5, reasons5)
	}

	// Test case 2: HPA does not follow the workload
	reasons6 := generateDistributedHPANotWorkReason(ctx, &Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp5"},
		&fedcorev1a1.PropagationPolicy{
			Spec: fedcorev1a1.PropagationPolicySpec{SchedulingMode: fedcorev1a1.SchedulingModeDuplicate},
		}, retainObj, hpaUns)
	expectedResult6 := "[The hpa is not follow the workload.]"
	if reasons6 != expectedResult6 {
		t.Errorf("Expected %q, but got %q", expectedResult6, reasons6)
	}

	// Test case 3: newPPResource is nil
	reasons1 := generateDistributedHPANotWorkReason(ctx, nil, nil, retainAndFollowObj, hpaUns)
	expectedResult1 := "[The workload is not bound to any propagationPolicy.]"
	if reasons1 != expectedResult1 {
		t.Errorf("Expected %q, but got %q", expectedResult1, reasons1)
	}

	// Test case 4: PropagationPolicy does not exist
	reasons2 := generateDistributedHPANotWorkReason(ctx, &Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp1"},
		nil, retainAndFollowObj, hpaUns)
	expectedResult2 := "[The Policy pp1 bound to the workload does not exist.]"
	if reasons2 != expectedResult2 {
		t.Errorf("Expected %q, but got %q", expectedResult2, reasons2)
	}

	// Test case 5: PropagationPolicy is not in Duplicate mode
	reasons3 := generateDistributedHPANotWorkReason(ctx, &Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp2"},
		&fedcorev1a1.PropagationPolicy{
			Spec: fedcorev1a1.PropagationPolicySpec{SchedulingMode: fedcorev1a1.SchedulingModeDivide},
		}, retainAndFollowObj, hpaUns)
	expectedResult3 := "[The Policy pp2 bound to the workload is not Duplicate mode.]"
	if reasons3 != expectedResult3 {
		t.Errorf("Expected %q, but got %q", expectedResult3, reasons3)
	}

	// Test case 6: PropagationPolicy is not enabled for follower scheduling
	reasons4 := generateDistributedHPANotWorkReason(ctx, &Resource{gvk: schema.GroupVersionKind{Kind: "Policy"}, name: "pp3"},
		&fedcorev1a1.PropagationPolicy{
			Spec: fedcorev1a1.PropagationPolicySpec{
				SchedulingMode:            fedcorev1a1.SchedulingModeDuplicate,
				DisableFollowerScheduling: true,
			},
		}, retainAndFollowObj, hpaUns)
	expectedResult4 := "[The Policy pp3 bound to the workload is not enabled for follower scheduling.]"
	if reasons4 != expectedResult4 {
		t.Errorf("Expected %q, but got %q", expectedResult4, reasons4)
	}
}

func TestFederatedHPAController_scaleTargetRefToResource(t *testing.T) {
	hpaObj := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				Kind:       "Deployment",
				Name:       "nginx",
				APIVersion: "apps/v1",
			},
		},
	}
	hpaUnsMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(hpaObj)
	hpaUns := &unstructured.Unstructured{Object: hpaUnsMap}

	type args struct {
		gvk schema.GroupVersionKind
		uns *unstructured.Unstructured
	}
	tests := []struct {
		name    string
		args    args
		want    Resource
		wantErr bool
	}{
		{
			"test get success",
			args{
				schema.GroupVersionKind{
					Group:   "autoscaling",
					Version: "v1",
					Kind:    "HorizontalPodAutoscaler",
				},
				hpaUns,
			},
			Resource{
				gvk: schema.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				name:      "nginx",
				namespace: "default",
			},
			false,
		},
		{
			"test not cache type",
			args{
				schema.GroupVersionKind{
					Group:   "autoscaling",
					Version: "v2beta2",
					Kind:    "HorizontalPodAutoscaler",
				},
				hpaUns,
			},
			Resource{},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FederatedHPAController{
				gvkToScaleTargetRef: map[schema.GroupVersionKind]string{
					{
						Group:   "autoscaling",
						Version: "v1",
						Kind:    "HorizontalPodAutoscaler",
					}: "spec.scaleTargetRef",
				},
			}
			got, err := f.scaleTargetRefToResource(tt.args.gvk, tt.args.uns)
			if (err != nil) != tt.wantErr {
				t.Errorf("scaleTargetRefToResource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("scaleTargetRefToResource() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetPropagationPolicyResourceFromFedWorkload(t *testing.T) {
	workload := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				scheduler.PropagationPolicyNameLabel:        "policy-1",
				scheduler.ClusterPropagationPolicyNameLabel: "policy-2",
			},
			Namespace: "default",
		},
	}

	resource := getPropagationPolicyResourceFromFedWorkload(workload)

	// validate PropagationPolicy case
	expectedResource1 := &Resource{
		gvk: schema.GroupVersionKind{
			Group:   fedcorev1a1.SchemeGroupVersion.Group,
			Version: fedcorev1a1.SchemeGroupVersion.Version,
			Kind:    PropagationPolicyKind,
		},
		name:      "policy-1",
		namespace: "default",
	}
	if resource1 := resource; resource1 == nil || *resource1 != *expectedResource1 {
		t.Errorf("Resource does not match the expected value. Got: %v, Expected: %v", resource, expectedResource1)
	}

	// validate ClusterPropagationPolicy case
	workload.Labels = map[string]string{
		scheduler.ClusterPropagationPolicyNameLabel: "policy-2",
	}
	expectedResource2 := &Resource{
		gvk: schema.GroupVersionKind{
			Group:   fedcorev1a1.SchemeGroupVersion.Group,
			Version: fedcorev1a1.SchemeGroupVersion.Version,
			Kind:    ClusterPropagationPolicyKind,
		},
		name: "policy-2",
	}
	if resource2 := getPropagationPolicyResourceFromFedWorkload(workload); resource2 == nil || *resource2 != *expectedResource2 {
		t.Errorf("Resource does not match the expected value. Got: %v, Expected: %v", resource2, expectedResource2)
	}

	// validate no ClusterPropagationPolicy case
	workload.Labels = nil
	if resource := getPropagationPolicyResourceFromFedWorkload(workload); resource != nil {
		t.Errorf("Expected nil resource, but got: %v", resource)
	}
}

func TestAddFedHPAEnableLabel(t *testing.T) {
	// Create a test Unstructured object with an existing label
	uns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					common.CentralizedHPAEnableKey: common.AnnotationValueTrue,
				},
			},
		},
	}
	// Create a test Unstructured object with empty labels
	unsEmptyLabels := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": nil,
			},
		},
	}
	// Create a test Unstructured object with other labels
	unsOtherLabels := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"other-label": "value",
				},
			},
		},
	}

	// Execute the function under test
	result1 := addFedHPAEnableLabel(context.Background(), uns)
	result2 := addFedHPAEnableLabel(context.Background(), unsEmptyLabels)
	result3 := addFedHPAEnableLabel(context.Background(), unsOtherLabels)

	// Verify the results
	// Verify the case with an existing label
	if result1 != false {
		t.Errorf("Expected false, but got true")
	}
	if labels := uns.GetLabels(); len(labels) != 1 || labels[common.CentralizedHPAEnableKey] != common.AnnotationValueTrue {
		t.Errorf("Unexpected labels: %v", labels)
	}

	// Verify the case with empty labels
	if result2 != true {
		t.Errorf("Expected true, but got false")
	}
	if labels := unsEmptyLabels.GetLabels(); len(labels) != 1 || labels[common.CentralizedHPAEnableKey] != common.AnnotationValueTrue {
		t.Errorf("Unexpected labels: %v", labels)
	}

	// Verify the case with other labels
	if result3 != true {
		t.Errorf("Expected true, but got false")
	}
	if labels := unsOtherLabels.GetLabels(); len(labels) != 2 ||
		labels[common.CentralizedHPAEnableKey] != common.AnnotationValueTrue || labels["other-label"] != "value" {
		t.Errorf("Unexpected labels: %v", labels)
	}
}

func TestRemoveFedHPAEnableLabel(t *testing.T) {
	// Create a test Unstructured object with the label
	uns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					common.CentralizedHPAEnableKey: common.AnnotationValueTrue,
				},
			},
		},
	}
	// Create a test Unstructured object without the label
	unsNoLabel := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": nil,
		},
	}
	// Create a test Unstructured object with other labels
	unsOtherLabels := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"other-label": "value",
				},
			},
		},
	}
	// Execute the function under test
	result1 := removeFedHPAEnableLabel(context.Background(), uns)
	result2 := removeFedHPAEnableLabel(context.Background(), unsNoLabel)
	result3 := removeFedHPAEnableLabel(context.Background(), unsOtherLabels)

	// Verify the results
	// Verify the case with the label
	if result1 != true {
		t.Errorf("Expected true, but got false")
	}
	if labels := uns.GetLabels(); len(labels) != 0 {
		t.Errorf("Expected empty labels, but got: %v", labels)
	}

	// Verify the case without the label
	if result2 != false {
		t.Errorf("Expected false, but got true")
	}
	if labels := unsNoLabel.GetLabels(); len(labels) != 0 {
		t.Errorf("Expected empty labels, but got: %v", labels)
	}

	// Verify the case with other labels
	if result3 != false {
		t.Errorf("Expected false, but got true")
	}
	if labels := unsOtherLabels.GetLabels(); len(labels) != 1 || labels["other-label"] != "value" {
		t.Errorf("Unexpected labels: %v", labels)
	}
}

func TestAddFedHPANotWorkReasonAnno(t *testing.T) {
	// Create a test Unstructured object without annotations
	uns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": nil,
			},
		},
	}

	// Verify the case without existing annotation
	result1 := addFedHPANotWorkReasonAnno(context.Background(), uns, "reason1")
	if result1 != true {
		t.Errorf("Expected true, but got false")
	}
	if annotations := uns.GetAnnotations(); len(annotations) != 1 || annotations[FedHPANotWorkReason] != "reason1" {
		t.Errorf("Unexpected annotations: %v", annotations)
	}

	// Verify the case with existing annotation and same value
	result2 := addFedHPANotWorkReasonAnno(context.Background(), uns, "reason1")
	if result2 != false {
		t.Errorf("Expected false, but got true")
	}
	if annotations := uns.GetAnnotations(); len(annotations) != 1 || annotations[FedHPANotWorkReason] != "reason1" {
		t.Errorf("Unexpected annotations: %v", annotations)
	}
}

func TestRemoveFedHPANotWorkReasonAnno(t *testing.T) {
	// Create a test Unstructured object with the annotation
	uns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					FedHPANotWorkReason: "reason1",
				},
			},
		},
	}

	// Verify the case with the annotation
	result1 := removeFedHPANotWorkReasonAnno(context.Background(), uns)
	if result1 != true {
		t.Errorf("Expected true, but got false")
	}
	if annotations := uns.GetAnnotations(); len(annotations) != 0 {
		t.Errorf("Expected empty annotations, but got: %v", annotations)
	}

	// Verify the case without the annotation
	result2 := removeFedHPANotWorkReasonAnno(context.Background(), uns)
	if result2 != false {
		t.Errorf("Expected false, but got true")
	}
	if annotations := uns.GetAnnotations(); len(annotations) != 0 {
		t.Errorf("Expected empty annotations, but got: %v", annotations)
	}
}

func Test_addFedHPAPendingController(t *testing.T) {
	type args struct {
		fedObject fedcorev1a1.GenericFederatedObject
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			"test fedObject has pending controller",
			args{
				&fedcorev1a1.FederatedObject{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"kubeadmiral.io/pending-controllers": "[[\"kubeadmiral.io/federatedhpa-controller\"]," +
								"[\"kubeadmiral.io/global-scheduler\"]," +
								"[\"kubeadmiral.io/overridepolicy-controller\"]]",
						},
					},
				},
			},
			false,
			false,
		},
		{
			"test fedObject has not pending controller",
			args{
				&fedcorev1a1.FederatedObject{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"kubeadmiral.io/pending-controllers": "[]",
						},
					},
				},
			},
			true,
			false,
		},
		{
			"test fedObject has not pending controller annotation",
			args{
				&fedcorev1a1.FederatedObject{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: nil,
					},
				},
			},
			false,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := addFedHPAPendingController(context.TODO(), tt.args.fedObject, &fedcorev1a1.FederatedTypeConfig{
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					Controllers: [][]string{
						{PrefixedFederatedHPAControllerName},
						{"schedule"},
						{"override"},
					},
				},
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("addFedHPAPendingController() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("addFedHPAPendingController() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_removePendingController(t *testing.T) {
	type args struct {
		fedObject fedcorev1a1.GenericFederatedObject
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			"test fedObject has pending controller",
			args{
				&fedcorev1a1.FederatedObject{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"kubeadmiral.io/pending-controllers": "[[\"kubeadmiral.io/federatedhpa-controller\"]," +
								"[\"kubeadmiral.io/global-scheduler\"]," +
								"[\"kubeadmiral.io/overridepolicy-controller\"]]",
						},
					},
				},
			},
			true,
			false,
		},
		{
			"test fedObject has not pending controller",
			args{
				&fedcorev1a1.FederatedObject{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"kubeadmiral.io/pending-controllers": "[]",
						},
					},
				},
			},
			false,
			false,
		},
		{
			"test fedObject has not pending controller annotation",
			args{
				&fedcorev1a1.FederatedObject{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: nil,
					},
				},
			},
			false,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := removePendingController(context.TODO(), &fedcorev1a1.FederatedTypeConfig{
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					Controllers: [][]string{
						{PrefixedFederatedHPAControllerName},
						{"schedule"},
						{"override"},
					},
				},
			}, tt.args.fedObject)
			if (err != nil) != tt.wantErr {
				t.Errorf("removePendingController() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("removePendingController() got = %v, want %v", got, tt.want)
			}
		})
	}
}
