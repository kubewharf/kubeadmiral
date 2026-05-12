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
	"context"
    "github.com/kubewharf/kubeadmiral/pkg/util/logging"

	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type ServiceLister struct {
	federatedInformerManager informermanager.FederatedInformerManager
}

type ServiceNamespaceLister struct {
	namespace                string
	federatedInformerManager informermanager.FederatedInformerManager
}

func NewServiceLister(informer informermanager.FederatedInformerManager) *ServiceLister {
	return &ServiceLister{federatedInformerManager: informer}
}

func (s *ServiceLister) ByNamespace(namespace string) AggregatedNamespaceLister {
	return &ServiceNamespaceLister{federatedInformerManager: s.federatedInformerManager, namespace: namespace}
}

func (s *ServiceNamespaceLister) List(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
	grv := NewGlobalResourceVersionFromString(opts.ResourceVersion)
	retGrv := grv.Clone()
	clusters, err := s.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}
	var resultObject runtime.Object
	items := make([]runtime.Object, 0)
	for _, cluster := range clusters {
		client, exists := s.federatedInformerManager.GetClusterKubeClient(cluster.Name)
		if !exists {
			continue
		}

		serviceList, err := client.CoreV1().Services(s.namespace).List(ctx, metav1.ListOptions{
			LabelSelector:   opts.LabelSelector,
			FieldSelector:   opts.FieldSelector,
			ResourceVersion: grv.Get(cluster.Name),
		})
		if err != nil {
			logging.Errorf(ctx, "Failed to list services from cluster %s (namespace %s): %v",
                cluster.Name, s.namespace, err,
            )
			continue
		}
		services := serviceList.Items

		list, err := meta.ListAccessor(serviceList)
		if err != nil {
			continue
		}

		if resultObject == nil {
			resultObject = serviceList
		}

		for i := range services {
			clusterobject.MakeObjectUnique(&services[i], cluster.Name)
			svcObj := services[i].DeepCopyObject()
			items = append(items, svcObj)
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

func (s *ServiceNamespaceLister) Get(ctx context.Context, name string, opts metav1.GetOptions) (runtime.Object, error) {
	clusters, err := s.federatedInformerManager.GetReadyClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusterobject.GetPossibleClusters(clusters, name) {
		client, exists := s.federatedInformerManager.GetClusterKubeClient(cluster)
		if !exists {
			continue
		}

		serviceList, err := client.CoreV1().Services(s.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		services := serviceList.Items

		for i := range services {
			if name == clusterobject.GenUniqueName(cluster, services[i].Name) {
				service := services[i].DeepCopy()
				clusterobject.MakeObjectUnique(service, cluster)
				grv := NewGlobalResourceVersionWithCapacity(1)
				grv.Set(cluster, service.GetResourceVersion())
				service.SetResourceVersion(grv.String())
				return service, nil
			}
		}
	}
	return nil, apierrors.NewNotFound(corev1.Resource("service"), name)
}
