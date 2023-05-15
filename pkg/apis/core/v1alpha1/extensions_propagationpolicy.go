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
	_ GenericPropagationPolicy = &PropagationPolicy{}
	_ GenericPropagationPolicy = &ClusterPropagationPolicy{}
)

func (pp *PropagationPolicy) GetSpec() *PropagationPolicySpec {
	return &pp.Spec
}

func (pp *PropagationPolicy) GetRefCountedStatus() *GenericRefCountedStatus {
	return &pp.Status.GenericRefCountedStatus
}

func (pp *PropagationPolicy) GetStatus() *PropagationPolicyStatus {
	return &pp.Status
}

func (pp *ClusterPropagationPolicy) GetSpec() *PropagationPolicySpec {
	return &pp.Spec
}

func (cpp *ClusterPropagationPolicy) GetRefCountedStatus() *GenericRefCountedStatus {
	return &cpp.Status.GenericRefCountedStatus
}

func (pp *ClusterPropagationPolicy) GetStatus() *PropagationPolicyStatus {
	return &pp.Status
}
