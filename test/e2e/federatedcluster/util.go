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

package federatedcluster

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	clusterfwk "github.com/kubewharf/kubeadmiral/test/e2e/framework/cluster"
)

var invalidClusterEndpointPatchData = []byte("[{\"op\": \"replace\", \"path\": \"/spec/apiEndpoint\", \"value\": \"invalid-endpoint\"}]")

func getServiceAccountInfo(secret *corev1.Secret) (token, ca []byte) {
	token = secret.Data[common.ClusterServiceAccountTokenKey]
	ca = secret.Data[common.ClusterServiceAccountCAKey]
	return
}

func hasAllResourcePermissions(rule rbacv1.PolicyRule) bool {
	verbs := sets.New(rule.Verbs...)
	groups := sets.New(rule.APIGroups...)
	resources := sets.New(rule.Resources...)

	return verbs.Has(rbacv1.VerbAll) && groups.Has(rbacv1.APIGroupAll) && resources.Has(rbacv1.ResourceAll)
}

func hasNonResourceURLReadPermission(rule rbacv1.PolicyRule) bool {
	urls := sets.New(rule.NonResourceURLs...)
	verbs := sets.New(rule.Verbs...)

	return urls.Has(rbacv1.NonResourceAll) && (verbs.Has(rbacv1.VerbAll) || verbs.Has("get"))
}

func statusCollected(cluster *fedcorev1a1.FederatedCluster) bool {
	conditions := cluster.Status.Conditions

	joinCond := clusterfwk.GetClusterJoinCondition(conditions)
	readyCond := clusterfwk.GetClusterReadyCondition(conditions)
	offlineCond := clusterfwk.GetClusterOfflineCondition(conditions)

	return joinCond != nil && joinCond.Status == corev1.ConditionTrue && readyCond != nil && offlineCond != nil
}
