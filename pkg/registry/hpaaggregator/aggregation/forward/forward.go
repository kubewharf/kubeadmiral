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

package forward

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/apiserver/pkg/audit"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	restclient "k8s.io/client-go/rest"
)

const (
	labelSelectorQueryKey   = "labelSelector"
	labelSelectorQueryValue = "kubeadmiral.io/centralized-hpa-enabled=true"

	// taken from https://github.com/kubernetes/kubernetes/blob/release-1.27/staging/src/k8s.io/kube-aggregator/pkg/apiserver/handler_proxy.go#L47
	aggregatedDiscoveryTimeout = 5 * time.Second

	// Header to hold the audit ID as the request is propagated through the serving hierarchy. The
	// Audit-ID header should be set by the first server to receive the request (e.g. the federation
	// server or kube-aggregator).
	//
	// Audit ID is also returned to client by http response header.
	// It's not guaranteed Audit-Id http header is sent for all requests. When kube-apiserver didn't
	// audit the events according to the audit policy, no Audit-ID is returned. Also, for request to
	// pods/exec, pods/attach, pods/proxy, kube-apiserver works like a proxy and redirect the request
	// to kubelet node, users will only get http headers sent from kubelet node, so no Audit-ID is
	// sent when users run command like "kubectl exec" or "kubectl attach".
	//
	// taken from https://github.com/kubernetes/kubernetes/blob/release-1.27/staging/src/k8s.io/apiserver/pkg/apis/audit/types.go#L38
	headerAuditID = "Audit-ID"
)

func NewForwardHandler(
	info *request.RequestInfo,
	r rest.Responder,
	adminConfig *restclient.Config,
	isHPA bool,
) (http.Handler, error) {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		adminConfig, err := NewConfigWithImpersonate(req.Context(), adminConfig)
		if err != nil {
			responsewriters.InternalError(rw, req, err)
			return
		}

		// we use admin config and ImpersonateUser to forward
		proxyRoundTripper, err := restclient.TransportFor(adminConfig)
		if err != nil {
			responsewriters.InternalError(rw, req, errors.New("failed to new transport"))
			return
		}

		if isHPA {
			switch info.Verb {
			case "get", "list", "watch", "deletecollection":
				q := req.URL.Query()
				if selector := q.Get(labelSelectorQueryKey); selector == "" {
					q.Set(labelSelectorQueryKey, labelSelectorQueryValue)
				} else {
					selector = fmt.Sprintf("%s,%s", selector, labelSelectorQueryValue)
					q.Set(labelSelectorQueryKey, selector)
				}
				req.URL.RawQuery = q.Encode()
				// TODO: do we need to filter out the create/update/patch/delete requests?
			}
		}

		location, err := url.Parse(adminConfig.Host)
		if err != nil {
			responsewriters.InternalError(rw, req, errors.New("failed to get location"))
			return
		}
		location.Path = req.URL.Path
		location.RawQuery = req.URL.RawQuery

		newReq, cancelFn := NewRequestForProxy(location, req, info)
		defer cancelFn()

		handler := proxy.NewUpgradeAwareHandler(
			location, proxyRoundTripper, true, false, proxy.NewErrorResponder(r))
		handler.UseLocationHost = true

		handler.ServeHTTP(rw, newReq)
	}), nil
}

// NewRequestForProxy returns a shallow copy of the original request with a context
// that may include a timeout for discovery requests
func NewRequestForProxy(
	location *url.URL,
	req *http.Request,
	info *request.RequestInfo,
) (*http.Request, context.CancelFunc) {
	newCtx := req.Context()
	cancelFn := func() {}

	// trim leading and trailing slashes. Then "/apis/group/version" and "/api/version" requests are for discovery,
	// so if we have three or fewer segments that we are going to proxy, we have a discovery request.
	if !info.IsResourceRequest && len(strings.Split(strings.Trim(info.Path, "/"), "/")) <= 3 {
		// discovery requests are used by kubectl and others to determine which resources a server has.
		// This is a cheap call that should be fast for every aggregated apiserver.  Latency for aggregation
		// is expected to be low (as for all extensions) so forcing a short timeout here helps
		// responsiveness of all clients.
		newCtx, cancelFn = context.WithTimeout(newCtx, aggregatedDiscoveryTimeout)
	}

	// WithContext creates a shallow clone of the request with the same context.
	newReq := req.WithContext(newCtx)
	newReq.Header = utilnet.CloneHeader(req.Header)
	newReq.URL = location
	newReq.Host = location.Host

	// If the original request has an audit ID, let's make sure we propagate this
	// to the aggregated server.
	if auditID, found := audit.AuditIDFrom(req.Context()); found {
		newReq.Header.Set(headerAuditID, string(auditID))
	}

	return newReq, cancelFn
}
