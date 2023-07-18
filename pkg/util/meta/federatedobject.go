/*
Copyright 2018 The Kubernetes Authors.

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
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func GetSourceObjectMeta(fedObject fedcorev1a1.GenericFederatedObject) (*metav1.PartialObjectMetadata, error) {
	partialObjectMeta := &metav1.PartialObjectMetadata{}
	if err := json.Unmarshal(fedObject.GetSpec().Template.Raw, partialObjectMeta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal FederatedObject's template: %w", err)
	}
	return partialObjectMeta, nil
}
