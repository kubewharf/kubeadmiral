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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestObjectMetaObjEquivalent(t *testing.T) {
	o1 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns1",
			Name:            "s1",
			UID:             "1231231412",
			ResourceVersion: "999",
		},
	}
	o2 := o1
	o3 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ns1",
			Name:        "s1",
			UID:         "1231231412",
			Annotations: map[string]string{"A": "B"},
		},
	}
	o4 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ns1",
			Name:        "s1",
			UID:         "1231255531412",
			Annotations: map[string]string{"A": "B"},
		},
	}
	o5 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns1",
			Name:            "s1",
			ResourceVersion: "1231231412",
			Annotations:     map[string]string{"A": "B"},
		},
	}
	o6 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns1",
			Name:            "s1",
			ResourceVersion: "1231255531412",
			Annotations:     map[string]string{"A": "B"},
		},
	}
	o7 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns1",
			Name:            "s1",
			ResourceVersion: "1231255531412",
			Annotations:     map[string]string{},
			Labels:          map[string]string{},
		},
	}
	o8 := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns1",
			Name:            "s1",
			ResourceVersion: "1231255531412",
		},
	}
	assert.True(t, ObjectMetaObjEquivalent(o1, o2))
	assert.False(t, ObjectMetaObjEquivalent(o1, o3))
	assert.True(t, ObjectMetaObjEquivalent(o3, o4))
	assert.True(t, ObjectMetaObjEquivalent(o5, o6))
	assert.True(t, ObjectMetaObjEquivalent(o3, o5))
	assert.True(t, ObjectMetaObjEquivalent(o7, o8))
	assert.True(t, ObjectMetaObjEquivalent(o8, o7))
}
