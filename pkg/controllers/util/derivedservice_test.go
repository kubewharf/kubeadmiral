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

package util

import (
	"testing"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func TestIsDerivedService(t *testing.T) {
	tests := []struct {
		name           string
		annotations    map[string]string
		expectedResult bool
	}{
		{
			name:           "annotations is nil",
			annotations:    nil,
			expectedResult: false,
		},
		{
			name: "annotations not have derived service anno",
			annotations: map[string]string{
				"test": "test",
			},
			expectedResult: false,
		},
		{
			name: "annotations have derived service anno",
			annotations: map[string]string{
				common.DerivedServiceAnnotation: "serve",
			},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ret := IsDerivedService(tt.annotations); ret != tt.expectedResult {
				t.Errorf("IsDerivedService() want %v, but got %v", tt.expectedResult, ret)
			}
		})
	}
}
