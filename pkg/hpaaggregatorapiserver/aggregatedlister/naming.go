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

package aggregatedlister

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
)

// MaxHashLength is maxLength of uint32
const MaxHashLength = 10

const ClusterNameAnnotationKey = common.DefaultPrefix + "cluster"
const RawNameAnnotationKey = common.DefaultPrefix + "raw-name"

func GenUniqueName(cluster, rawName string) string {
	return naming.GenerateFederatedObjectName(cluster, rawName)
}

// GetPossibleClusters returns the most matchable clusters from the target name
func GetPossibleClusters(clusters []*fedcorev1a1.FederatedCluster, targetName string) []string {
	possibleClusters := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		if len(cluster.Name) > len(targetName) {
			continue
		}
		if (len(cluster.Name) > common.MaxFederatedObjectNameLength-MaxHashLength-1 &&
			strings.HasPrefix(targetName, cluster.Name[:common.MaxFederatedObjectNameLength-MaxHashLength])) ||
			strings.HasPrefix(targetName, cluster.Name) {
			possibleClusters = append(possibleClusters, cluster.Name)
		}
	}
	return possibleClusters
}

func MakeObjectUnique(obj client.Object, clusterName string) {
	_, _ = annotation.AddAnnotation(obj, ClusterNameAnnotationKey, clusterName)
	_, _ = annotation.AddAnnotation(obj, RawNameAnnotationKey, obj.GetName())
	obj.SetName(GenUniqueName(clusterName, obj.GetName()))
}

func MakePodUnique(pod *corev1.Pod, clusterName string) {
	MakeObjectUnique(pod, clusterName)
	if pod.Spec.NodeName != "" {
		pod.Spec.NodeName = GenUniqueName(clusterName, pod.Spec.NodeName)
	}
}
