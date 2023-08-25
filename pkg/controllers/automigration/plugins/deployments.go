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

package plugins

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	deploymentutil "github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/controller/deployment/util"
)

type deploymentPlugin struct{}

func (*deploymentPlugin) GetPodsForClusterObject(
	ctx context.Context,
	unsObj *unstructured.Unstructured,
	handle ClusterHandle,
) ([]*corev1.Pod, error) {
	deployment := &appsv1.Deployment{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unsObj.Object, deployment); err != nil {
		return nil, err
	}

	rsList, err := deploymentutil.ListReplicaSets(deployment, func(ns string, opts metav1.ListOptions) ([]*appsv1.ReplicaSet, error) {
		opts = *opts.DeepCopy()
		opts.ResourceVersion = "0" // list from watch cache
		rsList, err := handle.KubeClient.AppsV1().ReplicaSets(ns).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		ret := []*appsv1.ReplicaSet{}
		for i := range rsList.Items {
			ret = append(ret, &rsList.Items[i])
		}
		return ret, nil
	})
	if err != nil {
		return nil, err
	}

	newRS := deploymentutil.FindNewReplicaSet(deployment, rsList)
	if newRS == nil {
		return []*corev1.Pod{}, nil
	}

	podList, err := deploymentutil.ListPods(
		deployment,
		[]*appsv1.ReplicaSet{newRS},
		func(ns string, opts metav1.ListOptions) (*corev1.PodList, error) {
			opts = *opts.DeepCopy()
			opts.ResourceVersion = "0" // list from watch cache
			if err != nil {
				return nil, err
			}
			podList, err := handle.KubeClient.CoreV1().Pods(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			return podList, nil
		},
	)
	if err != nil {
		return nil, err
	}

	ret := []*corev1.Pod{}
	for i := range podList.Items {
		ret = append(ret, &podList.Items[i])
	}

	return ret, nil
}

func (*deploymentPlugin) GetTargetObjectFromPod(
	ctx context.Context,
	pod *corev1.Pod,
	handle ClusterHandle,
) (obj *unstructured.Unstructured, found bool, err error) {
	rs, found, err := GetSpecifiedOwnerFromObj(ctx, handle.DynamicClient, pod, metav1.APIResource{
		Name:    "replicasets",
		Group:   appsv1.GroupName,
		Version: "v1",
		Kind:    common.ReplicaSetKind,
	})
	if err != nil || !found {
		return nil, false, err
	}

	return GetSpecifiedOwnerFromObj(ctx, handle.DynamicClient, rs, metav1.APIResource{
		Name:    "deployments",
		Group:   appsv1.GroupName,
		Version: "v1",
		Kind:    common.DeploymentKind,
	})
}

var _ Plugin = &deploymentPlugin{}
