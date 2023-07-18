//go:build exclude
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

package federatedclient

import (
	"context"

	dynamicclient "k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformer "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
)

type ClientUpdateHandler func(cluster string, factory FederatedClientFactory)

// FederatedClientFactory allows users to get shared clientsets and informer factories for joined member clusters.
// Note that in the context of a FederatedClientFactory, a cluster will only "exist" when it becomes "Joined".
// If a cluster is not "Joined", we may not have the necessary information to connect to the member cluster.
type FederatedClientFactory interface {
	Start(ctx context.Context)
	AddClientUpdateHandler(handler ClientUpdateHandler)

	KubeClientsetForCluster(cluster string) (client kubeclient.Interface, exists bool, err error)
	DynamicClientsetForCluster(cluster string) (client dynamicclient.Interface, exists bool, err error)

	KubeSharedInformerFactoryForCluster(cluster string) (factory kubeinformer.SharedInformerFactory, exist bool, err error)
	DynamicSharedInformerFactoryForCluster(cluster string) (factory dynamicinformer.DynamicSharedInformerFactory, exists bool, err error)
}
