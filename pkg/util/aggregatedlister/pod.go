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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type PodLister struct {
	federatedInformerManager informermanager.FederatedInformerManager
}

type PodNamespaceLister struct {
	namespace string

	federatedInformerManager informermanager.FederatedInformerManager
}

var (
	_ cache.GenericLister          = &PodLister{}
	_ cache.GenericNamespaceLister = &PodNamespaceLister{}
)

func NewPodLister(informer informermanager.FederatedInformerManager) *PodLister {
	return &PodLister{federatedInformerManager: informer}
}

func (p *PodLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	for _, cluster := range clusters {
		podLister, podsSynced, exists := p.federatedInformerManager.GetPodLister(cluster.Name)
		if !exists || !podsSynced() {
			continue
		}
		pods, err := podLister.List(selector)
		if err != nil {
			continue
		}
		for i := range pods {
			pod := pods[i].DeepCopy()
			clusterobject.MakePodUnique(pod, cluster.Name)
			ret = append(ret, pod)
		}
	}
	return ret, nil
}

func (p *PodLister) Get(name string) (runtime.Object, error) {
	items := strings.Split(name, "/")
	if len(items) != 2 {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("invalid name %q", name))
	}
	return p.ByNamespace(items[0]).Get(items[1])
}

func (p *PodLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return &PodNamespaceLister{federatedInformerManager: p.federatedInformerManager, namespace: namespace}
}

func (p *PodNamespaceLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	for _, cluster := range clusters {
		podLister, podsSynced, exists := p.federatedInformerManager.GetPodLister(cluster.Name)
		if !exists || !podsSynced() {
			continue
		}
		pods, err := podLister.Pods(p.namespace).List(selector)
		if err != nil {
			continue
		}
		for i := range pods {
			pod := pods[i].DeepCopy()
			clusterobject.MakePodUnique(pod, cluster.Name)
			ret = append(ret, pod)
		}
	}
	return ret, nil
}

func (p *PodNamespaceLister) Get(name string) (runtime.Object, error) {
	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusterobject.GetPossibleClusters(clusters, name) {
		podLister, podsSynced, exists := p.federatedInformerManager.GetPodLister(cluster)
		if !exists || !podsSynced() {
			continue
		}
		pods, err := podLister.Pods(p.namespace).List(labels.Everything())
		if err != nil {
			continue
		}
		for i := range pods {
			if name == clusterobject.GenUniqueName(cluster, pods[i].Name) {
				pod := pods[i].DeepCopy()
				clusterobject.MakePodUnique(pod, cluster)
				return pod, nil
			}
		}
	}
	return nil, apierrors.NewNotFound(corev1.Resource("pod"), name)
}
