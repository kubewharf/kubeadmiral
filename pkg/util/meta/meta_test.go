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

package meta

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestObjectMeta(t *testing.T) {
	o1 := metav1.ObjectMeta{
		Namespace:       "ns1",
		Name:            "s1",
		UID:             "1231231412",
		ResourceVersion: "999",
	}
	o2 := copyObjectMeta(o1)
	o3 := metav1.ObjectMeta{
		Namespace:   "ns1",
		Name:        "s1",
		UID:         "1231231412",
		Annotations: map[string]string{"A": "B"},
	}
	o4 := metav1.ObjectMeta{
		Namespace:   "ns1",
		Name:        "s1",
		UID:         "1231255531412",
		Annotations: map[string]string{"A": "B"},
	}
	o5 := metav1.ObjectMeta{
		Namespace:       "ns1",
		Name:            "s1",
		ResourceVersion: "1231231412",
		Annotations:     map[string]string{"A": "B"},
	}
	o6 := metav1.ObjectMeta{
		Namespace:       "ns1",
		Name:            "s1",
		ResourceVersion: "1231255531412",
		Annotations:     map[string]string{"A": "B"},
	}
	o7 := metav1.ObjectMeta{
		Namespace:       "ns1",
		Name:            "s1",
		ResourceVersion: "1231255531412",
		Annotations:     map[string]string{},
		Labels:          map[string]string{},
	}
	o8 := metav1.ObjectMeta{
		Namespace:       "ns1",
		Name:            "s1",
		ResourceVersion: "1231255531412",
	}
	assert.Equal(t, 0, len(o2.UID))
	assert.Equal(t, 3, len(o2.ResourceVersion))
	assert.Equal(t, o1.Name, o2.Name)
	assert.True(t, ObjectMetaEquivalent(o1, o2))
	assert.False(t, ObjectMetaEquivalent(o1, o3))
	assert.True(t, ObjectMetaEquivalent(o3, o4))
	assert.True(t, ObjectMetaEquivalent(o5, o6))
	assert.True(t, ObjectMetaEquivalent(o3, o5))
	assert.True(t, ObjectMetaEquivalent(o7, o8))
	assert.True(t, ObjectMetaEquivalent(o8, o7))
}

func TestObjectMetaAndSpec(t *testing.T) {
	s1 := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "s1",
		},
		Spec: corev1.ServiceSpec{
			ExternalName: "Service1",
		},
	}
	s1b := s1
	s2 := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "s2",
		},
		Spec: corev1.ServiceSpec{
			ExternalName: "Service1",
		},
	}
	s3 := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "s1",
		},
		Spec: corev1.ServiceSpec{
			ExternalName: "Service2",
		},
	}
	assert.True(t, ObjectMetaAndSpecEquivalent(&s1, &s1b))
	assert.False(t, ObjectMetaAndSpecEquivalent(&s1, &s2))
	assert.False(t, ObjectMetaAndSpecEquivalent(&s1, &s3))
	assert.False(t, ObjectMetaAndSpecEquivalent(&s2, &s3))
}

func TestConvertViaJson(t *testing.T) {
	var replicas int32 = 10
	typedObj := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "foo",
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "appsv1",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			Replicas:        10,
			UpdatedReplicas: 5,
			ReadyReplicas:   5,
		},
	}

	unstrunctruedObj := &unstructured.Unstructured{}
	assert.NoError(t, ConvertViaJson(typedObj, unstrunctruedObj))

	convertedTyped := &appsv1.Deployment{}
	assert.NoError(t, ConvertViaJson(unstrunctruedObj, convertedTyped))
	assert.Equal(t, typedObj, convertedTyped)

	status := appsv1.DeploymentStatus{
		Replicas:            5,
		UpdatedReplicas:     6,
		UnavailableReplicas: 7,
	}
	unstrucedStatus, err := GetUnstructuredStatus(status)
	assert.NoError(t, err)
	unstrucedStatus["foo"] = "bar"
}

func TestGetUnstructuredStatus(t *testing.T) {
	status := appsv1.DeploymentStatus{
		Replicas:            15,
		UpdatedReplicas:     6,
		UnavailableReplicas: 7,
	}
	unstrucedStatus, err := GetUnstructuredStatus(status)
	assert.NoError(t, err)
	assert.Equal(t, map[string]interface{}{
		"replicas":            interface{}(float64(15)),
		"updatedReplicas":     interface{}(float64(6)),
		"unavailableReplicas": interface{}(float64(7)),
	}, unstrucedStatus)
}
