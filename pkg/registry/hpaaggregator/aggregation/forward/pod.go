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
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kubewharf/kubeadmiral/pkg/util/aggregatedlister"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/handlers"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/kubewharf/kubeadmiral/pkg/util/clusterobject"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
)

type PodHandler interface {
	Handler(requestInfo *genericapirequest.RequestInfo) (http.Handler, error)
}

type PodREST struct {
	podLister                aggregatedlister.AggregatedLister
	federatedInformerManager informermanager.FederatedInformerManager
	minRequestTimeout        time.Duration
}

var (
	_ rest.Getter  = &PodREST{}
	_ rest.Lister  = &PodREST{}
	_ rest.Watcher = &PodREST{}
	_ PodHandler   = &PodREST{}
)

func NewPodREST(
	f informermanager.FederatedInformerManager,
	podLister aggregatedlister.AggregatedLister,
	minRequestTimeout time.Duration,
) *PodREST {
	return &PodREST{
		federatedInformerManager: f,
		podLister:                podLister,
		minRequestTimeout:        minRequestTimeout,
	}
}

func (p *PodREST) Handler(requestInfo *genericapirequest.RequestInfo) (http.Handler, error) {
	switch requestInfo.Verb {
	case "get":
		return handlers.GetResource(p, scope), nil
	case "list", "watch":
		return handlers.ListResource(p, p, scope, false, p.minRequestTimeout), nil
	default:
		return nil, apierrors.NewMethodNotSupported(schema.GroupResource{
			Group:    requestInfo.APIGroup,
			Resource: requestInfo.Resource,
		}, requestInfo.Verb)
	}
}

// Get ...
func (p *PodREST) Get(ctx context.Context, name string, opts *metav1.GetOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	obj, err := p.podLister.ByNamespace(namespace).Get(ctx, name, *opts)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// return not-found errors directly
			return nil, err
		}
		klog.ErrorS(err, "Failed getting pod", "pod", klog.KRef(namespace, name))
		return nil, fmt.Errorf("failed getting pod: %w", err)
	}

	return obj, nil
}

func (p *PodREST) NewList() runtime.Object {
	return &corev1.PodList{}
}

func (p *PodREST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	objs, err := p.podLister.ByNamespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector:   options.LabelSelector.String(),
		FieldSelector:   options.FieldSelector.String(),
		ResourceVersion: options.ResourceVersion,
	})
	if err != nil {
		klog.ErrorS(err, "Failed listing pods", "namespace", klog.KRef("", namespace))
		return nil, fmt.Errorf("failed listing pods: %w", err)
	}
	return objs, nil
}

func (p *PodREST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return tableConvertor.ConvertToTable(ctx, object, tableOptions)
}

func (p *PodREST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	grv := aggregatedlister.NewGlobalResourceVersionFromString(options.ResourceVersion)
	retGrv := grv.Clone()
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}

	namespace := genericapirequest.NamespaceValue(ctx)

	ctx, logger := logging.InjectLoggerValues(
		ctx,
		"label_selector", label,
		"field_selector", options.FieldSelector,
		"namespace", namespace,
	)

	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		logger.Error(err, "Failed to get ready clusters")
		return nil, fmt.Errorf("failed watching pods: %w", err)
	}

	// TODO: support cluster addition and deletion during the watch
	var lock sync.Mutex
	isProxyChClosed := false
	proxyCh := make(chan watch.Event)
	proxyWatcher := watch.NewProxyWatcher(proxyCh)
	for i := range clusters {
		client, exist := p.federatedInformerManager.GetClusterKubeClient(clusters[i].Name)
		if !exist {
			continue
		}
		watcher, err := client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector:   label.String(),
			FieldSelector:   options.FieldSelector.String(),
			TimeoutSeconds:  pointer.Int64(1200),
			ResourceVersion: grv.Get(clusters[i].Name),
		})
		if err != nil {
			logger.Error(err, "Failed watching pods")
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
					if pod, ok := event.Object.(*corev1.Pod); ok {
						clusterobject.MakePodUnique(pod, cluster)
						retGrv.Set(cluster, pod.ResourceVersion)
						pod.SetResourceVersion(retGrv.String())
						event.Object = pod
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
