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
	"context"

	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func isCentralizedHPAObject(ctx context.Context, fedObject fedcorev1a1.GenericFederatedObject) (bool, error) {
	logger := klog.FromContext(ctx)

	templateMetadata, err := fedObject.GetSpec().GetTemplateMetadata()
	if err != nil {
		logger.Error(err, "Failed to get TemplateMetadata")
		return false, err
	}

	if value, ok := templateMetadata.GetLabels()[common.CentralizedHPAEnableKey]; ok && value == common.AnnotationValueTrue {
		return true, nil
	}

	return false, nil
}
