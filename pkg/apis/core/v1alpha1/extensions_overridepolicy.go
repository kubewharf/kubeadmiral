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

package v1alpha1

var (
	_ GenericOverridePolicy   = &OverridePolicy{}
	_ GenericRefCountedPolicy = &OverridePolicy{}
)

func (pp *OverridePolicy) GetSpec() *GenericOverridePolicySpec {
	return &pp.Spec
}

func (pp *OverridePolicy) GetKey() string {
	return pp.Namespace + "/" + pp.Name
}

func (pp *OverridePolicy) GetRefCountedStatus() *GenericRefCountedStatus {
	return &pp.Status.GenericRefCountedStatus
}

var (
	_ GenericOverridePolicy   = &ClusterOverridePolicy{}
	_ GenericRefCountedPolicy = &ClusterOverridePolicy{}
)

func (cpp *ClusterOverridePolicy) GetSpec() *GenericOverridePolicySpec {
	return &cpp.Spec
}

func (cpp *ClusterOverridePolicy) GetKey() string {
	return cpp.Name
}

func (cpp *ClusterOverridePolicy) GetRefCountedStatus() *GenericRefCountedStatus {
	return &cpp.Status.GenericRefCountedStatus
}
