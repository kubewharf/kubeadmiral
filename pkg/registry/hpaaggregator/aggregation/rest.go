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

package aggregation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/apis/hpaaggregator/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/registry/hpaaggregator/aggregation/forward"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type REST struct {
	restConfig               *restclient.Config
	federatedInformerManager informermanager.FederatedInformerManager

	resolver genericapirequest.RequestInfoResolver

	podLister  cache.GenericLister
	podHandler forward.PodHandler

	forwardHandler forward.ForwardHandler

	logger klog.Logger
}

var _ rest.Storage = &REST{}
var _ rest.Scoper = &REST{}
var _ rest.Connecter = &REST{}

var proxyMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(
	federatedInformerManager informermanager.FederatedInformerManager,
	podLister cache.GenericLister,
	config *restclient.Config,
	minRequestTimeout time.Duration,
	logger klog.Logger,
) (*REST, error) {
	podHandler := forward.NewPodREST(
		federatedInformerManager,
		podLister,
		minRequestTimeout,
	)

	forwardHandler := forward.NewForwardHandler(config)

	resolver := &genericapirequest.RequestInfoFactory{
		APIPrefixes:          sets.NewString("apis", "api"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}

	return &REST{
		restConfig:               config,
		federatedInformerManager: federatedInformerManager,
		resolver:                 resolver,
		podLister:                podLister,
		podHandler:               podHandler,
		forwardHandler:           forwardHandler,
		logger:                   logger,
	}, nil
}

// New returns an empty object that can be used with Create and Update after request data has been put into it.
// This object must be a pointer type for use with Codec.DecodeInto([]byte, runtime.Object)
func (r *REST) New() runtime.Object {
	return &v1alpha1.Aggregation{}
}

// Destroy cleans up its resources on shutdown.
func (r *REST) Destroy() {
}

// NamespaceScoped returns true if the storage is namespaced
func (r *REST) NamespaceScoped() bool {
	return false
}

func (r *REST) Connect(ctx context.Context, _ string, _ runtime.Object, resp rest.Responder) (http.Handler, error) {
	return http.HandlerFunc(
		func(rw http.ResponseWriter, req *http.Request) {
			curInfo, found := genericapirequest.RequestInfoFrom(ctx)
			if !found {
				resp.Error(errors.New("no RequestInfo found in the context"))
				return
			}
			// If the request is "/apis/hpaaggregator.kubeadmiral.io/v1alpha1/aggregations/hpa/proxy/api/v1/pods",
			// the curInfo.Parts are [aggregations, hpa, proxy, api, v1, pods]. This proxy could only work when
			// the curInfo.Parts is not empty, so that we can get the target path from it.
			if len(curInfo.Parts) <= 2 || curInfo.Parts[2] != "proxy" {
				resp.Error(apierrors.NewGenericServerResponse(
					http.StatusNotFound,
					curInfo.Verb,
					schema.GroupResource{},
					"",
					"",
					0,
					false,
				))
				return
			}

			reqCopy := req.Clone(ctx)
			reqCopy.URL.Path = "/" + path.Join(curInfo.Parts[3:]...)
			proxyInfo, err := r.resolver.NewRequestInfo(reqCopy)
			if err != nil {
				resp.Error(errors.New("failed to renew RequestInfo for proxy"))
				return
			}
			ctx = genericapirequest.WithRequestInfo(ctx, proxyInfo)
			req = reqCopy.Clone(ctx)

			var proxyHandler http.Handler
			switch {
			case isSelf(proxyInfo):
				err = errors.New("can't proxy to self")
			case isRequestForPod(proxyInfo):
				proxyHandler, err = r.podHandler.Handler(proxyInfo)
			default:
				// TODO: if we provide an API for ResourceMetrics or CustomMetrics, we can serve it directly without proxy
				proxyHandler, err = r.forwardHandler.Handler(proxyInfo, r.isRequestForHPA(proxyInfo))
			}

			if err != nil {
				resp.Error(err)
				return
			}
			proxyHandler.ServeHTTP(rw, req)
		},
	), nil

}

// NewConnectOptions returns an empty options object that will be used to pass
// options to the Connect method. If nil, then a nil options object is passed to
// Connect.
func (r *REST) NewConnectOptions() (runtime.Object, bool, string) {
	return nil, true, ""
}

// ConnectMethods returns the list of HTTP methods handled by Connect
func (r *REST) ConnectMethods() []string {
	return proxyMethods
}

func isSelf(request *genericapirequest.RequestInfo) bool {
	return request.APIGroup == v1alpha1.SchemeGroupVersion.Group
}

func isRequestForPod(request *genericapirequest.RequestInfo) bool {
	return request.APIGroup == "" && request.APIVersion == "v1" && request.Resource == "pods"
}

func (r *REST) isRequestForHPA(request *genericapirequest.RequestInfo) bool {
	if request.Resource == "" || request.APIGroup == "" {
		return false
	}
	ftc, _ := r.federatedInformerManager.
		GetFederatedTypeConfigLister().
		Get(fmt.Sprintf("%s.%s", request.Resource, request.APIGroup))
	if ftc != nil && ftc.GetAnnotations()[common.HPAScaleTargetRefPath] != "" {
		return true
	}
	return false
}
