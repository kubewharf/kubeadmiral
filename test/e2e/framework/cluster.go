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

package framework

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/cascadingdeletion"
)

const (
	E2EClusterTaintKey = "kubeadmiral-e2e-test"

	// TODO: make this configurable?
	defaultClusterWaitTimeout    = time.Minute
	defaultClusterCleanupTimeout = 10 * time.Second
)

type ClusterModifier func(*fedcorev1a1.FederatedCluster)

func WithInsecure(c *fedcorev1a1.FederatedCluster) {
	c.Spec.Insecure = true
}

func WithServiceAccount(c *fedcorev1a1.FederatedCluster) {
	c.Spec.UseServiceAccountToken = true
}

func WithInvalidEndpoint(c *fedcorev1a1.FederatedCluster) {
	c.Spec.APIEndpoint = "invalid-endpoint"
}

func WithCascadingDelete(c *fedcorev1a1.FederatedCluster) {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[cascadingdeletion.AnnotationCascadingDelete] = "true"
}

func WithTaints(c *fedcorev1a1.FederatedCluster) {
	if c.Spec.Taints == nil {
		c.Spec.Taints = []corev1.Taint{}
	}
	c.Spec.Taints = append(
		c.Spec.Taints,
		corev1.Taint{
			Effect: corev1.TaintEffectNoSchedule,
			Key:    E2EClusterTaintKey,
		},
		corev1.Taint{
			Effect: corev1.TaintEffectNoExecute,
			Key:    E2EClusterTaintKey,
		},
	)
}
