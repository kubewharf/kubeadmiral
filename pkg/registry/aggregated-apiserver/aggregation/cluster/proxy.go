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

package cluster

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"

	aggregatedv1alpha "github.com/kubewharf/kubeadmiral/pkg/apis/aggregatedapiserver/v1alpha1"
	corev1alpha "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/proxy"
)

const matchAllClusters = "*"

// ProxyREST implements the proxy subresource for a Cluster.
type ProxyREST struct {
	redirector               rest.Redirector
	federatedInformerManager informermanager.FederatedInformerManager
	resolver                 genericapirequest.RequestInfoResolver
	clusterGetter            func(name string) (*corev1alpha.FederatedCluster, error)
	clusterLister            func() (*corev1alpha.FederatedClusterList, error)

	admiralLocation  *url.URL
	admiralTransPort http.RoundTripper

	logger klog.Logger
}

// NewProxyREST returns a new ProxyREST.
func NewProxyREST(
	federatedInformerManager informermanager.FederatedInformerManager,
	resolver genericapirequest.RequestInfoResolver,
	clusterGetter func(name string) (*corev1alpha.FederatedCluster, error),
	clusterLister func() (*corev1alpha.FederatedClusterList, error),
	admiralLocation *url.URL,
	admiralTransPort http.RoundTripper,
	logger klog.Logger) *ProxyREST {
	return &ProxyREST{
		federatedInformerManager: federatedInformerManager,
		resolver:                 resolver,
		clusterGetter:            clusterGetter,
		clusterLister:            clusterLister,
		admiralLocation:          admiralLocation,
		admiralTransPort:         admiralTransPort,
		logger:                   logger,
	}
}

// Implement Connecter
var _ = rest.Connecter(&ProxyREST{})

var proxyMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// New returns an empty cluster proxy subresource.
func (r *ProxyREST) New() runtime.Object {
	return &aggregatedv1alpha.ClusterProxyOptions{}
}

// ConnectMethods returns the list of HTTP methods handled by Connect.
func (r *ProxyREST) ConnectMethods() []string {
	return proxyMethods
}

// NewConnectOptions returns versioned resource that represents proxy parameters.
func (r *ProxyREST) NewConnectOptions() (runtime.Object, bool, string) {
	return &aggregatedv1alpha.ClusterProxyOptions{}, true, "path"
}

// Connect returns a handler for the cluster proxy.
func (r *ProxyREST) Connect(ctx context.Context, id string, options runtime.Object, responder rest.Responder) (http.Handler, error) {
	proxyOpts, ok := options.(*aggregatedv1alpha.ClusterProxyOptions)
	if !ok {
		return nil, fmt.Errorf("invalid options object: %#v", options)
	}

	if id == matchAllClusters {
		return r.connectAllClusters(proxyOpts.Path, responder)
	}

	cluster, err := r.clusterGetter(id)
	if err != nil {
		return nil, err
	}

	return proxy.ConnectCluster(cluster, proxyOpts.Path, responder, r.federatedInformerManager)
}

// Destroy cleans up its resources on shutdown.
func (r *ProxyREST) Destroy() {
	// Given no underlying store, so we don't
	// need to destroy anything.
}

// NamespaceScoped returns true if the storage is namespaced
func (r *ProxyREST) NamespaceScoped() bool {
	return false
}
