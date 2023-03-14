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

package v1alpha1

import (
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NamespaceName = "namespaces"
)

func (f *FederatedTypeConfig) GetObjectMeta() metav1.ObjectMeta {
	return f.ObjectMeta
}

func (f *FederatedTypeConfig) GetTargetType() metav1.APIResource {
	return apiResourceToMeta(f.Spec.TargetType)
}

func (f *FederatedTypeConfig) GetNamespaced() bool {
	return f.Spec.TargetType.Namespaced()
}

func (f *FederatedTypeConfig) GetPropagationEnabled() bool {
	return true
}

func (f *FederatedTypeConfig) GetFederatedType() metav1.APIResource {
	return apiResourceToMeta(f.Spec.FederatedType)
}

func (f *FederatedTypeConfig) GetStatusType() *metav1.APIResource {
	if f.Spec.StatusType == nil {
		return nil
	}
	metaAPIResource := apiResourceToMeta(*f.Spec.StatusType)
	return &metaAPIResource
}

func (f *FederatedTypeConfig) GetSourceType() *metav1.APIResource {
	if f.Spec.SourceType == nil {
		return nil
	}
	meta := apiResourceToMeta(*f.Spec.SourceType)
	return &meta
}

func (f *FederatedTypeConfig) GetStatusEnabled() bool {
	return f.Spec.StatusCollection != nil
}

func (f *FederatedTypeConfig) GetStatusAggregationEnabled() bool {
	return f.Spec.StatusAggregation != nil &&
		*f.Spec.StatusAggregation == StatusAggregationEnabled
}

func (f *FederatedTypeConfig) GetPolicyRcEnabled() bool {
	return true // TODO: should this be configurable?
}

func (f *FederatedTypeConfig) GetFederateEnabled() bool {
	return f.Spec.SourceType != nil
}

func (f *FederatedTypeConfig) GetRevisionHistoryEnabled() bool {
	return f.Spec.RevisionHistory != nil &&
		*f.Spec.RevisionHistory == RevisionHistoryEnabled
}

func (f *FederatedTypeConfig) GetRolloutPlanEnabled() bool {
	return f.Spec.RolloutPlan != nil &&
		*f.Spec.RolloutPlan == RolloutPlanEnabled
}

func (f *FederatedTypeConfig) GetControllers() [][]string {
	return f.Spec.Controllers
}

func (f *FederatedTypeConfig) IsNamespace() bool {
	return f.Name == NamespaceName
}

func (a *APIResource) Namespaced() bool {
	return a.Scope == apiextv1beta1.NamespaceScoped
}

func apiResourceToMeta(apiResource APIResource) metav1.APIResource {
	return metav1.APIResource{
		Group:      apiResource.Group,
		Version:    apiResource.Version,
		Kind:       apiResource.Kind,
		Name:       apiResource.PluralName,
		Namespaced: apiResource.Namespaced(),
	}
}
