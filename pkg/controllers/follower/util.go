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

package follower

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	podutil "github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/api/v1/pod"
)

type FollowerReference struct {
	GroupKind schema.GroupKind
	Namespace string
	Name      string
}

type followerAnnotationElement struct {
	Group string `json:"group,omitempty"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

func getFollowersFromAnnotation(
	fedObject *unstructured.Unstructured,
	sourceToFederatedGKMap map[schema.GroupKind]schema.GroupKind,
) (sets.Set[FollowerReference], error) {
	annotation := fedObject.GetAnnotations()[common.FollowersAnnotation]
	if len(annotation) == 0 {
		return nil, nil
	}

	var followersFromAnnotation []followerAnnotationElement
	if err := json.Unmarshal([]byte(annotation), &followersFromAnnotation); err != nil {
		return nil, err
	}

	followers := sets.New[FollowerReference]()
	for _, followerFromAnnotation := range followersFromAnnotation {
		sourceGK := schema.GroupKind{
			Group: followerFromAnnotation.Group,
			Kind:  followerFromAnnotation.Kind,
		}
		federatedGK, exists := sourceToFederatedGKMap[sourceGK]
		if !exists {
			return nil, fmt.Errorf("no federated type config found for source type %v", sourceGK)
		}
		followers.Insert(FollowerReference{
			GroupKind: federatedGK,
			// Only allow followers from the same namespace
			Namespace: fedObject.GetNamespace(),
			Name:      followerFromAnnotation.Name,
		})
	}
	return followers, nil
}

func getFollowersFromPodSpec(
	fedObject *unstructured.Unstructured,
	podSpecPath string,
	sourceToFederatedGKMap map[schema.GroupKind]schema.GroupKind,
) (sets.Set[FollowerReference], error) {
	podSpec, err := getPodSpec(fedObject, podSpecPath)
	if err != nil {
		return nil, err
	}

	pod := &corev1.Pod{
		Spec: *podSpec,
	}
	return getFollowersFromPod(fedObject.GetNamespace(), pod, sourceToFederatedGKMap), nil
}

func getFollowersFromPod(
	namespace string,
	pod *corev1.Pod,
	sourceToFederatedGKMap map[schema.GroupKind]schema.GroupKind,
) sets.Set[FollowerReference] {
	followers := sets.New[FollowerReference]()

	if federatedSecretGK, exists := sourceToFederatedGKMap[schema.GroupKind{Kind: "Secret"}]; exists {
		podutil.VisitPodSecretNames(pod, func(name string) bool {
			followers.Insert(FollowerReference{
				GroupKind: federatedSecretGK,
				Namespace: namespace,
				Name:      name,
			})
			return true
		})
	}

	if federatedConfigMapGK, exists := sourceToFederatedGKMap[schema.GroupKind{Kind: "ConfigMap"}]; exists {
		podutil.VisitPodConfigmapNames(pod, func(name string) bool {
			followers.Insert(FollowerReference{
				GroupKind: federatedConfigMapGK,
				Namespace: namespace,
				Name:      name,
			})
			return true
		})
	}

	if federatedPVCGK, exists := sourceToFederatedGKMap[schema.GroupKind{Kind: "PersistentVolumeClaim"}]; exists {
		for _, vol := range pod.Spec.Volumes {
			// TODO: do we need to support PVCs created from ephemeral volumes?
			if vol.PersistentVolumeClaim != nil {
				followers.Insert(FollowerReference{
					GroupKind: federatedPVCGK,
					Namespace: namespace,
					Name:      vol.PersistentVolumeClaim.ClaimName,
				})
			}
		}
	}

	if federatedSAGK, exists := sourceToFederatedGKMap[schema.GroupKind{Kind: "ServiceAccount"}]; exists {
		if saName := pod.Spec.ServiceAccountName; saName != "" {
			followers.Insert(FollowerReference{
				GroupKind: federatedSAGK,
				Namespace: namespace,
				Name:      saName,
			})
		}
	}

	return followers
}

func getPodSpec(fedObject *unstructured.Unstructured, podSpecPath string) (*corev1.PodSpec, error) {
	if fedObject == nil {
		return nil, fmt.Errorf("fedObject is nil")
	}
	fedObjectPodSpecPath := append(
		[]string{common.SpecField, common.TemplateField},
		strings.Split(podSpecPath, ".")...)
	podSpecMap, found, err := unstructured.NestedMap(fedObject.Object, fedObjectPodSpecPath...)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("pod spec does not exist at path %q", podSpecPath)
	}
	podSpec := &corev1.PodSpec{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(podSpecMap, podSpec); err != nil {
		return nil, err
	}
	return podSpec, nil
}
