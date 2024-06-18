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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"

	aggregatedv1alpha "github.com/kubewharf/kubeadmiral/pkg/apis/aggregatedapiserver/v1alpha1"
	corev1alpha "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	corelister "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/proxy"
)

// ClusterREST implements a RESTStorage for Cluster.
type ClusterREST struct {
	federatedInformerManager informermanager.FederatedInformerManager
	federatedClusterLister   corelister.FederatedClusterLister

	logger klog.Logger
}

// NewClusterREST returns a new ClusterREST.
func NewClusterREST(federatedInformerManager informermanager.FederatedInformerManager, logger klog.Logger) *ClusterREST {
	return &ClusterREST{
		federatedInformerManager: federatedInformerManager,
		federatedClusterLister:   federatedInformerManager.GetFederatedClusterLister(),
		logger:                   logger,
	}
}

func (r *ClusterREST) New() runtime.Object {
	return &aggregatedv1alpha.Aggregations{}
}

// NamespaceScoped returns true if the storage is namespaced
func (r *ClusterREST) NamespaceScoped() bool {
	return false
}

func (r *ClusterREST) Destroy() {
}

// Implement Redirector.
var _ = rest.Redirector(&ClusterREST{})

// ResourceLocation returns a URL to which one can send traffic for the specified cluster.
func (r *ClusterREST) ResourceLocation(ctx context.Context, name string) (*url.URL, http.RoundTripper, error) {
	cluster, err := r.GetCluster(name)
	if err != nil {
		return nil, nil, err
	}

	tlsConfig, err := proxy.GetTlsConfigForCluster(r.federatedInformerManager, name)
	if err != nil {
		return nil, nil, err
	}

	return proxy.Location(cluster, tlsConfig)
}

func (r *ClusterREST) GetCluster(name string) (*corev1alpha.FederatedCluster, error) {
	cluster, err := r.federatedClusterLister.Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// return not-found errors directly
			return nil, err
		}
		klog.ErrorS(err, "Failed getting federated cluster", "cluster", klog.KRef("", name))
		return nil, fmt.Errorf("failed getting federated cluster: %w", err)
	}

	return cluster, nil
}

func (r *ClusterREST) ListClusters() (*corev1alpha.FederatedClusterList, error) {
	clusters, err := r.federatedClusterLister.List(labels.Everything())
	if err != nil {
		klog.ErrorS(err, "Failed listing federated clusters", "clusters")
		return nil, fmt.Errorf("failed listing nodes: %w", err)
	}
	clusterlist := &corev1alpha.FederatedClusterList{}
	for _, cluster := range clusters {
		clusterlist.Items = append(clusterlist.Items, *cluster)
	}
	return clusterlist, nil
}
