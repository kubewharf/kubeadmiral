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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
		rsList := &appsv1.ReplicaSetList{}
		listOpts, err := convertListOptions(ns, &opts)
		if err != nil {
			return nil, err
		}
		if err := handle.Client.ListWithOptions(ctx, rsList, listOpts); err != nil {
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
	podList, err := deploymentutil.ListPods(
		deployment,
		[]*appsv1.ReplicaSet{newRS},
		func(ns string, opts metav1.ListOptions) (*corev1.PodList, error) {
			podList := &corev1.PodList{}
			listOpts, err := convertListOptions(ns, &opts)
			if err != nil {
				return nil, err
			}
			if err := handle.Client.ListWithOptions(ctx, podList, listOpts); err != nil {
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

var _ Plugin = &deploymentPlugin{}

func convertListOptions(ns string, opts *metav1.ListOptions) (*client.ListOptions, error) {
	labelSelector, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		return nil, err
	}
	fieldSelector, err := fields.ParseSelector(opts.FieldSelector)
	if err != nil {
		return nil, err
	}

	return &client.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: fieldSelector,
		Namespace:     ns,
		Limit:         opts.Limit,
		Continue:      opts.Continue,
	}, nil
}
