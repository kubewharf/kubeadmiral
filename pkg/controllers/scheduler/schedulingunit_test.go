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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func Test_getCustomMigrationInfo(t *testing.T) {
	tests := []struct {
		name      string
		fedObject fedcorev1a1.GenericFederatedObject
		want      *framework.CustomMigrationInfo
	}{
		{
			name: "get custom migration info",
			fedObject: &fedcorev1a1.FederatedObject{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						common.AppliedMigrationConfigurationAnnotation: "{\"limitedCapacity\":{\"cluster1\":1,\"cluster2\":2}}",
					},
				},
			},
			want: &framework.CustomMigrationInfo{
				LimitedCapacity: map[string]int64{
					"cluster1": 1,
					"cluster2": 2,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, _ := getCustomMigrationInfo(test.fedObject); !reflect.DeepEqual(got, test.want) {
				t.Errorf("getCustomMigrationInfo() = %v, want %v", got, test.want)
			}
		})
	}
}
