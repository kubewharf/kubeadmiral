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

package forward

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/handlers"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/kubewharf/kubeadmiral/pkg/util/aggregatedlister"
	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
)

type ServiceHandler interface {
	Handler(requestInfo *genericapirequest.RequestInfo) (http.Handler, error)
}

type ServiceREST struct {
	serviceLister            aggregatedlister.AggregatedLister
	federatedInformerManager informermanager.FederatedInformerManager
	minRequestTimeout        time.Duration
}

var (
	_ rest.Getter    = &ServiceREST{}
	_ rest.Lister    = &ServiceREST{}
	_ rest.Watcher   = &ServiceREST{}
	_ ServiceHandler = &ServiceREST{}
)

func NewServiceREST(
	f informermanager.FederatedInformerManager,
	serviceLister aggregatedlister.AggregatedLister,
	minRequestTimeout time.Duration,
) *ServiceREST {
	return &ServiceREST{
		federatedInformerManager: f,
		serviceLister:            serviceLister,
		minRequestTimeout:        minRequestTimeout,
	}
}

func (s *ServiceREST) Handler(requestInfo *genericapirequest.RequestInfo) (http.Handler, error) {
	switch requestInfo.Verb {
	case getVerb:
		return handlers.GetResource(s, serviceScope), nil
	case listVerb, watchVerb:
		return handlers.ListResource(s, s, serviceScope, false, s.minRequestTimeout), nil
	default:
		return nil, apierrors.NewMethodNotSupported(schema.GroupResource{
			Group:    requestInfo.APIGroup,
			Resource: requestInfo.Resource,
		}, requestInfo.Verb)
	}
}

func (s *ServiceREST) Get(ctx context.Context, name string, opts *metav1.GetOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	getOpts := metav1.GetOptions{}
	if opts != nil {
		getOpts = *opts
	}
	obj, err := s.serviceLister.ByNamespace(namespace).Get(ctx, name, getOpts)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// return not-found errors directly
			return nil, err
		}
		klog.ErrorS(err, "Failed getting service", "service", klog.KRef(namespace, name))
		return nil, fmt.Errorf("failed getting service: %w", err)
	}
	return obj, nil
}

func (s *ServiceREST) NewList() runtime.Object {
	return &corev1.ServiceList{}
}

func (s *ServiceREST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}
	field := fields.Everything()
	if options != nil && options.FieldSelector != nil {
		field = options.FieldSelector
	}
	resourceVersion := ""
	if options != nil {
		resourceVersion = options.ResourceVersion
	}
	objs, err := s.serviceLister.ByNamespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector:   label.String(),
		FieldSelector:   field.String(),
		ResourceVersion: resourceVersion,
	})
	if err != nil {
		klog.ErrorS(err, "Failed listing services", "namespace", klog.KRef("", namespace))
		return nil, fmt.Errorf("failed listing services: %w", err)
	}
	return objs, nil
}

func (s *ServiceREST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return tableConvertor.ConvertToTable(ctx, object, tableOptions)
}

func (s *ServiceREST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	resourceVersion := ""
	if options != nil {
		resourceVersion = options.ResourceVersion
	}
	grv := aggregatedlister.NewGlobalResourceVersionFromString(resourceVersion)
	retGrv := grv.Clone()
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}
	field := fields.Everything()
	if options != nil && options.FieldSelector != nil {
		field = options.FieldSelector
	}

	namespace := genericapirequest.NamespaceValue(ctx)

	ctx, logger := logging.InjectLoggerValues(
		ctx,
		"label_selector", label,
		"field_selector", field,
		"resourceVersion", resourceVersion,
		"namespace", namespace,
	)

	clusters, err := s.federatedInformerManager.GetReadyClusters()
	if err != nil {
		logger.Error(err, "Failed to get ready clusters")
		return nil, fmt.Errorf("failed watching services: %w", err)
	}

	// TODO: support cluster addition and deletion during the watch
	var lock sync.Mutex
	isProxyChClosed := false
	proxyCh := make(chan watch.Event)
	proxyWatcher := watch.NewProxyWatcher(proxyCh)
	for i := range clusters {
		client, exist := s.federatedInformerManager.GetClusterKubeClient(clusters[i].Name)
		if !exist {
			continue
		}
		watcher, err := client.CoreV1().Services(namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector:   label.String(),
			FieldSelector:   field.String(),
			TimeoutSeconds:  pointer.Int64(1200),
			ResourceVersion: grv.Get(clusters[i].Name),
		})
		if err != nil {
			logger.Error(err, "Failed watching services")
			continue
		}
		go func(cluster string) {
			defer func() {
				logger.WithValues("cluster", cluster).Info("Stopped cluster watcher")
				watcher.Stop()
			}()
			for {
				select {
				case <-proxyWatcher.StopChan():
					return
				case event, ok := <-watcher.ResultChan():
					if !ok {
						lock.Lock()
						if !isProxyChClosed {
							close(proxyCh)
							isProxyChClosed = true
							logger.WithValues("cluster", cluster).Info("Closed proxy watcher channel")
						}
						lock.Unlock()

						return
					}
					if svc, ok := event.Object.(*corev1.Service); ok {
						clusterobject.MakeObjectUnique(svc, cluster)
						retGrv.Set(cluster, svc.ResourceVersion)
						svc.SetResourceVersion(retGrv.String())
						event.Object = svc
					}

					lock.Lock()
					if !isProxyChClosed {
						proxyCh <- event
					}
					lock.Unlock()
				}
			}
		}(clusters[i].Name)
	}
	return proxyWatcher, nil
}
