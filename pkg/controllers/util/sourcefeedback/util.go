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

package sourcefeedback

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
)

func setAnnotation(object metav1.Object, key string, value interface{}, hasChanged *bool) {
	jsonBuf, err := json.Marshal(value)
	if err != nil {
		klog.Errorf("Cannot marshal JSON: %v", err)
	}

	localHasChanged, err := annotation.AddAnnotation(object, key, string(jsonBuf))
	if err != nil {
		klog.Errorf("Cannot add %q annotation: %v", key, err)
	}

	*hasChanged = *hasChanged || localHasChanged
}
