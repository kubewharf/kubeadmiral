/*
Copyright 2025 The KubeAdmiral Authors.

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

package yaml

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	genericschema "github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
)

type kubernetesResource interface {
	runtime.Object
	schema.ObjectKind
}

// MarshalYAML marshals the given object into bytes
func MarshalYAML(obj interface{}) ([]byte, error) {
	SetObjectGVK(obj)
	bs, err := yaml.Marshal(obj)
	if err != nil {
		if o, ok := obj.(metav1.Object); ok {
			klog.Error(fmt.Sprintf("failed to marshal metav1.Object %s/%s: %v", o.GetNamespace(), o.GetName(), err))
		} else {
			klog.Error(fmt.Sprintf("failed to marshal unknown object: %v", err))
		}
		return nil, err
	}
	return bs, nil
}

// GetObjectGVK get kubernetes object type meta
func GetObjectGVK(obj runtime.Object) schema.GroupVersionKind {
	gvks, _, err := genericschema.Scheme.ObjectKinds(obj)
	if err != nil {
		return schema.GroupVersionKind{}
	}
	if len(gvks) == 0 {
		return schema.GroupVersionKind{}
	}
	return gvks[0]
}

// SetObjectGVK set kubernetes object type meta
func SetObjectGVK(obj interface{}) {
	if o, ok := obj.(kubernetesResource); ok {
		if gvk := GetObjectGVK(o); !gvk.Empty() {
			o.SetGroupVersionKind(gvk)
		}
	}
}
