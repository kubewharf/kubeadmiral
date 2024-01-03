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
	"encoding/json"
	"testing"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func Test_isCentralizedHPAObject(t *testing.T) {
	type args struct {
		fedObject fedcorev1a1.GenericFederatedObject
	}

	fedHPAObject := autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				common.CentralizedHPAEnableKey: common.AnnotationValueTrue,
			},
		},
	}
	fedHPAObjectJSON, err := json.Marshal(fedHPAObject)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	notFedHPAObject := autoscalingv1.HorizontalPodAutoscaler{}
	notFedHPAObjectJSON, err := json.Marshal(notFedHPAObject)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			"is fed hpa object",
			args{
				&fedcorev1a1.FederatedObject{
					Spec: fedcorev1a1.GenericFederatedObjectSpec{
						Template: apiextensionsv1.JSON{
							Raw: fedHPAObjectJSON,
						},
					},
				},
			},
			true,
			false,
		},
		{
			"is not fed hpa object",
			args{
				&fedcorev1a1.FederatedObject{
					Spec: fedcorev1a1.GenericFederatedObjectSpec{
						Template: apiextensionsv1.JSON{
							Raw: notFedHPAObjectJSON,
						},
					},
				},
			},
			false,
			false,
		},
		{
			"get fed object Spec TemplateMetadata failed",
			args{
				&fedcorev1a1.FederatedObject{
					Spec: fedcorev1a1.GenericFederatedObjectSpec{},
				},
			},
			false,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isCentralizedHPAObject(context.TODO(), tt.args.fedObject)
			if (err != nil) != tt.wantErr {
				t.Errorf("isCentralizedHPAObject() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("isCentralizedHPAObject() got = %v, want %v", got, tt.want)
			}
		})
	}
}
