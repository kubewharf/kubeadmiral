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

package schema

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

func APIResourceToGVR(apiResource *metav1.APIResource) runtimeschema.GroupVersionResource {
	return runtimeschema.GroupVersionResource{
		Group:    apiResource.Group,
		Version:  apiResource.Version,
		Resource: apiResource.Name,
	}
}

func APIResourceToGVK(apiResource *metav1.APIResource) runtimeschema.GroupVersionKind {
	return runtimeschema.GroupVersionKind{
		Group:   apiResource.Group,
		Version: apiResource.Version,
		Kind:    apiResource.Kind,
	}
}
