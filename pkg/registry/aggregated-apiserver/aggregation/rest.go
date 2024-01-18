/*
Copyright 2024 The KubeAdmiral Authors.

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

package aggregation

import (
	"k8s.io/apimachinery/pkg/util/sets"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/registry/aggregated-apiserver/aggregation/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

// REST includes operators for FederatedCluster and for proxy subresource.
type REST struct {
	Cluster *cluster.ClusterREST
	Proxy   *cluster.ProxyREST
}

// NewREST returns new instance of REST.
func NewREST(
	federatedInformerManager informermanager.FederatedInformerManager,
	restConfig *restclient.Config,
	logger klog.Logger) (*REST, error) {
	admiralLocation, admiralTransport, err := cluster.ResourceLocation(restConfig)
	resolver := &genericapirequest.RequestInfoFactory{
		APIPrefixes:          sets.NewString("apis", "api"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}
	if err != nil {
		return &REST{}, err
	}
	clusterREST := cluster.NewClusterREST(federatedInformerManager, logger)
	proxyREST := cluster.NewProxyREST(
		federatedInformerManager,
		resolver,
		clusterREST.GetCluster,
		clusterREST.ListClusters,
		admiralLocation,
		admiralTransport,
		logger)

	return &REST{
		Cluster: clusterREST,
		Proxy:   proxyREST,
	}, nil
}
