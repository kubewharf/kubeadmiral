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
	"context"

	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type PodLister struct {
	federatedInformerManager informermanager.FederatedInformerManager
}

type PodNamespaceLister struct {
	namespace string

	federatedInformerManager informermanager.FederatedInformerManager
}

func NewPodLister(informer informermanager.FederatedInformerManager) *PodLister {
	return &PodLister{federatedInformerManager: informer}
}

func (p *PodLister) ByNamespace(namespace string) AggregatedNamespaceLister {
	return &PodNamespaceLister{federatedInformerManager: p.federatedInformerManager, namespace: namespace}
}

func (p *PodNamespaceLister) List(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
	grv := NewGlobalResourceVersionFromString(opts.ResourceVersion)
	retGrv := grv.Clone()
	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	var resultObject runtime.Object
	items := make([]runtime.Object, 0)
	for _, cluster := range clusters {
		client, exists := p.federatedInformerManager.GetClusterKubeClient(cluster.Name)
		if !exists {
			continue
		}

		podList, err := client.CoreV1().Pods(p.namespace).List(ctx, metav1.ListOptions{
			LabelSelector:   opts.LabelSelector,
			FieldSelector:   opts.FieldSelector,
			ResourceVersion: grv.Get(cluster.Name),
		})
		if err != nil {
			continue
		}
		pods := podList.Items

		list, err := meta.ListAccessor(podList)
		if err != nil {
			continue
		}

		if resultObject == nil {
			resultObject = podList
		}

		for _, pod := range pods {
			clusterobject.MakePodUnique(&pod, cluster.Name)
			podObj := pod.DeepCopyObject()
			items = append(items, podObj)
		}

		retGrv.Set(cluster.Name, list.GetResourceVersion())
	}

	if resultObject == nil {
		resultObject = &metav1.List{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "List",
			},
			ListMeta: metav1.ListMeta{},
			Items:    []runtime.RawExtension{},
		}
	}

	err = meta.SetList(resultObject, items)
	if err != nil {
		return nil, err
	}
	accessor, err := meta.ListAccessor(resultObject)
	if err != nil {
		return nil, err
	}
	accessor.SetResourceVersion(retGrv.String())
	return resultObject, nil
}

func (p *PodNamespaceLister) Get(ctx context.Context, name string, opts metav1.GetOptions) (runtime.Object, error) {
	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusterobject.GetPossibleClusters(clusters, name) {
		client, exists := p.federatedInformerManager.GetClusterKubeClient(cluster)
		if !exists {
			continue
		}

		podList, err := client.CoreV1().Pods(p.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		pods := podList.Items

		for i := range pods {
			if name == clusterobject.GenUniqueName(cluster, pods[i].Name) {
				pod := pods[i].DeepCopy()
				clusterobject.MakePodUnique(pod, cluster)
				grv := NewGlobalResourceVersionWithCapacity(1)
				grv.Set(cluster, pod.GetResourceVersion())
				pod.SetResourceVersion(grv.String())
				return pod, nil
			}
		}
	}
	return nil, apierrors.NewNotFound(corev1.Resource("pod"), name)
}
