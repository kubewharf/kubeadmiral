/*
Copyright 2025 The KubeAdmiral Authors.

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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type EndpointSliceLister struct {
	federatedInformerManager informermanager.FederatedInformerManager
}

type EndpointSliceNamespaceLister struct {
	namespace                string
	federatedInformerManager informermanager.FederatedInformerManager
}

func NewEndpointSliceLister(informer informermanager.FederatedInformerManager) *EndpointSliceLister {
	return &EndpointSliceLister{federatedInformerManager: informer}
}

func (e *EndpointSliceLister) ByNamespace(namespace string) AggregatedNamespaceLister {
	return &EndpointSliceNamespaceLister{federatedInformerManager: e.federatedInformerManager, namespace: namespace}
}

func (e *EndpointSliceNamespaceLister) List(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
	grv := NewGlobalResourceVersionFromString(opts.ResourceVersion)
	retGrv := grv.Clone()
	clusters, err := e.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	var resultObject runtime.Object
	items := make([]runtime.Object, 0)
	for _, cluster := range clusters {
		client, exists := e.federatedInformerManager.GetClusterKubeClient(cluster.Name)
		if !exists {
			continue
		}

		endpointSliceList, err := client.DiscoveryV1().EndpointSlices(e.namespace).List(ctx, metav1.ListOptions{
			LabelSelector:   opts.LabelSelector,
			FieldSelector:   opts.FieldSelector,
			ResourceVersion: grv.Get(cluster.Name),
		})
		if err != nil {
			continue
		}
		endpointSlices := endpointSliceList.Items

		list, err := meta.ListAccessor(endpointSliceList)
		if err != nil {
			continue
		}

		if resultObject == nil {
			resultObject = endpointSliceList
		}

		for i := range endpointSlices {
			clusterobject.MakeObjectUnique(&endpointSlices[i], cluster.Name)
			epsObj := endpointSlices[i].DeepCopyObject()
			items = append(items, epsObj)
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

func (e *EndpointSliceNamespaceLister) Get(ctx context.Context, name string, opts metav1.GetOptions) (runtime.Object, error) {
	clusters, err := e.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusterobject.GetPossibleClusters(clusters, name) {
		client, exists := e.federatedInformerManager.GetClusterKubeClient(cluster)
		if !exists {
			continue
		}

		endpointSliceList, err := client.DiscoveryV1().EndpointSlices(e.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		endpointSlices := endpointSliceList.Items

		for i := range endpointSlices {
			if name == clusterobject.GenUniqueName(cluster, endpointSlices[i].Name) {
				eps := endpointSlices[i].DeepCopy()
				clusterobject.MakeObjectUnique(eps, cluster)
				grv := NewGlobalResourceVersionWithCapacity(1)
				grv.Set(cluster, eps.GetResourceVersion())
				eps.SetResourceVersion(grv.String())
				return eps, nil
			}
		}
	}
	return nil, apierrors.NewNotFound(corev1.Resource("endpointSlice"), name)
}
