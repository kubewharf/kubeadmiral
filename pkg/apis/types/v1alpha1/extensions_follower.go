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

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func SetFollows(uns *unstructured.Unstructured, leaders []LeaderReference) error {
	leaderMaps := make([]interface{}, 0, len(leaders))
	for _, leader := range leaders {
		leaderMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&leader)
		if err != nil {
			return err
		}
		leaderMaps = append(leaderMaps, leaderMap)
	}

	return unstructured.SetNestedSlice(uns.Object, leaderMaps, common.FollowsPath...)
}

func (l *LeaderReference) GroupKind() schema.GroupKind {
	return schema.GroupKind{
		Group: l.Group,
		Kind:  l.Kind,
	}
}
