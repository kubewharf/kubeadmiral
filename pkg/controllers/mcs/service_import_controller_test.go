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

package mcs

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
)

func TestNewDerivedService(t *testing.T) {
	tests := []struct {
		name        string
		svcImport   *mcsv1alpha1.ServiceImport
		wantService *corev1.Service
	}{
		{
			name: "generate derived service",
			svcImport: &mcsv1alpha1.ServiceImport{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
				Spec: mcsv1alpha1.ServiceImportSpec{
					Ports: []mcsv1alpha1.ServicePort{
						{
							Name:        "test",
							Protocol:    corev1.ProtocolTCP,
							Port:        80,
							AppProtocol: pointer.String("80"),
						},
					},
				},
			},
			wantService: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       common.ServiceKind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
					Annotations: map[string]string{
						common.DerivedServiceAnnotation: "bar",
					},
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{
						{
							Name:        "test",
							Protocol:    corev1.ProtocolTCP,
							Port:        80,
							AppProtocol: pointer.String("80"),
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			derivedSvc := newDerivedService(tt.svcImport)
			if !reflect.DeepEqual(derivedSvc, tt.wantService) {
				t.Errorf("newDerivedService() want %v, but got %v", tt.wantService, derivedSvc)
			}
		})
	}
}

func Test_newFederatedObjectForDerivedSvc(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	siFedObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Placements: []fedcorev1a1.PlacementWithController{
				{
					Controller: PrefixedGlobalSchedulerName,
					Placement: []fedcorev1a1.ClusterReference{
						{
							Cluster: "member1",
						},
					},
				},
			},
		},
	}

	fedObj, err := newFederatedObjectForDerivedSvc(service, siFedObj)
	assert.Equal(t, nil, err)
	assert.Equal(t, "derived-bar-services", fedObj.GetName())
	assert.Equal(t, map[string]string{
		"kubeadmiral.io/derived-service":     "bar",
		"kubeadmiral.io/pending-controllers": "[]",
	}, fedObj.GetAnnotations())
	assert.Equal(t, []metav1.OwnerReference{*metav1.NewControllerRef(siFedObj,
		common.FederatedObjectGVK)}, fedObj.GetOwnerReferences())
	assert.Equal(t, []fedcorev1a1.PlacementWithController{
		{
			Controller: PrefixedServiceImportControllerName,
			Placement: []fedcorev1a1.ClusterReference{
				{
					Cluster: "member1",
				},
			},
		},
	}, fedObj.GetSpec().Placements)
}

func Test_updateFederatedObjectForDerivedSvc(t *testing.T) {
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       common.ServiceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
			Annotations: map[string]string{
				"kubeadmiral.io/derived-service": "bar",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	siFedObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Placements: []fedcorev1a1.PlacementWithController{
				{
					Controller: PrefixedGlobalSchedulerName,
					Placement: []fedcorev1a1.ClusterReference{
						{
							Cluster: "member1",
						},
					},
				},
			},
		},
	}

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
	assert.Equal(t, nil, err)
	targetTemplate := federate.TemplateForSourceObject(&unstructured.Unstructured{Object: unstructuredMap}, service.Annotations, nil)
	rawTargetTemplate, err := targetTemplate.MarshalJSON()
	assert.Equal(t, nil, err)

	fedObj := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "derived-bar-services",
			Annotations: map[string]string{
				"kubeadmiral.io/derived-service":     "bar",
				"kubeadmiral.io/pending-controllers": "[]",
			},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(siFedObj,
				common.FederatedObjectGVK)},
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{
				Raw: rawTargetTemplate,
			},
			Placements: []fedcorev1a1.PlacementWithController{
				{
					Controller: PrefixedServiceImportControllerName,
					Placement: []fedcorev1a1.ClusterReference{
						{
							Cluster: "member1",
						},
					},
				},
			},
		},
	}

	isUpdate, err := updateFederatedObjectForDerivedSvc(service, fedObj, siFedObj)
	assert.Equal(t, nil, err)
	assert.Equal(t, false, isUpdate)
}

func TestServicePorts(t *testing.T) {
	tests := []struct {
		name             string
		svcImport        *mcsv1alpha1.ServiceImport
		wantServicePorts []corev1.ServicePort
	}{
		{
			name: "generate service ports",
			svcImport: &mcsv1alpha1.ServiceImport{
				Spec: mcsv1alpha1.ServiceImportSpec{
					Ports: []mcsv1alpha1.ServicePort{
						{
							Name:        "test",
							Protocol:    corev1.ProtocolTCP,
							Port:        80,
							AppProtocol: pointer.String("80"),
						},
					},
				},
			},
			wantServicePorts: []corev1.ServicePort{
				{
					Name:        "test",
					Protocol:    corev1.ProtocolTCP,
					Port:        80,
					AppProtocol: pointer.String("80"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servicePorts := servicePorts(tt.svcImport)
			if !reflect.DeepEqual(servicePorts, tt.wantServicePorts) {
				t.Errorf("servicePorts() want %v, but got %v", tt.wantServicePorts, servicePorts)
			}
		})
	}
}
