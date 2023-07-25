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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	podutil "github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/api/v1/pod"
	fedpodutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
)

type objectGroupKindKey struct {
	namespace  string
	fedName    string
	sourceName string
	sourceGK   schema.GroupKind
}

func (k objectGroupKindKey) ObjectSourceKey() string {
	return fmt.Sprintf("%s/%s", k.namespace, k.sourceName)
}

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
	fedObject fedcorev1a1.GenericFederatedObject,
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

		followers.Insert(FollowerReference{
			GroupKind: sourceGK,
			// Only allow followers from the same namespace
			Namespace: fedObject.GetNamespace(),
			Name:      followerFromAnnotation.Name,
		})
	}
	return followers, nil
}

func getFollowersFromPodSpec(
	fedObject fedcorev1a1.GenericFederatedObject,
	podSpecPath string,
) (sets.Set[FollowerReference], error) {
	podSpec, err := fedpodutil.GetPodSpec(fedObject, podSpecPath)
	if err != nil {
		return nil, err
	}

	pod := &corev1.Pod{
		Spec: *podSpec,
	}
	return getFollowersFromPod(fedObject.GetNamespace(), pod), nil
}

func getFollowersFromPod(
	namespace string,
	pod *corev1.Pod,
) sets.Set[FollowerReference] {
	followers := sets.New[FollowerReference]()

	podutil.VisitPodSecretNames(pod, func(name string) bool {
		followers.Insert(FollowerReference{
			GroupKind: schema.GroupKind{Kind: "Secret"},
			Namespace: namespace,
			Name:      name,
		})
		return true
	})

	podutil.VisitPodConfigmapNames(pod, func(name string) bool {
		followers.Insert(FollowerReference{
			GroupKind: schema.GroupKind{Kind: "ConfigMap"},
			Namespace: namespace,
			Name:      name,
		})
		return true
	})

	for _, vol := range pod.Spec.Volumes {
		// TODO: do we need to support PVCs created from ephemeral volumes?
		if vol.PersistentVolumeClaim != nil {
			followers.Insert(FollowerReference{
				GroupKind: schema.GroupKind{Kind: "PersistentVolumeClaim"},
				Namespace: namespace,
				Name:      vol.PersistentVolumeClaim.ClaimName,
			})
		}
	}

	if saName := pod.Spec.ServiceAccountName; saName != "" {
		followers.Insert(FollowerReference{
			GroupKind: schema.GroupKind{Kind: "ServiceAccount"},
			Namespace: namespace,
			Name:      saName,
		})
	}

	return followers
}
