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

package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func Test_skipSync(t *testing.T) {
	federatedObject := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{common.DryRunAnnotation: common.AnnotationValueTrue},
		},
	}
	assert.True(t, skipSync(federatedObject))
	federatedObject.GetAnnotations()[common.DryRunAnnotation] = common.AnnotationValueFalse
	assert.False(t, skipSync(federatedObject))
	federatedObject.GetAnnotations()[common.DryRunAnnotation] = common.AnnotationValueTrue
	federatedObject.GetStatus().Clusters = []fedcorev1a1.PropagationStatus{
		{
			Cluster: "cluster1",
		},
	}
	assert.False(t, skipSync(federatedObject))
}
