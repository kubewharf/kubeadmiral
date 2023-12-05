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
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/handlers"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	api "k8s.io/kubernetes/pkg/apis/core"

	"github.com/kubewharf/kubeadmiral/pkg/hpaaggregatorapiserver/aggregatedlister"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

type PodHandler interface {
	Handler(requestInfo *genericapirequest.RequestInfo) (http.Handler, error)
}

type PodREST struct {
	podLister                cache.GenericLister
	federatedInformerManager informermanager.FederatedInformerManager
	minRequestTimeout        time.Duration
}

var _ rest.Getter = &PodREST{}
var _ rest.Lister = &PodREST{}
var _ rest.Watcher = &PodREST{}
var _ PodHandler = &PodREST{}

func NewPodREST(
	f informermanager.FederatedInformerManager,
	podLister cache.GenericLister,
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
	obj, err := p.podLister.ByNamespace(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// return not-found errors directly
			return nil, err
		}
		klog.ErrorS(err, "Failed getting pod", "pod", klog.KRef(namespace, name))
		return nil, fmt.Errorf("failed getting pod: %w", err)
	}

	pod := &api.Pod{}
	if err := scheme.Convert(obj, pod, nil); err != nil {
		return nil, fmt.Errorf("failed converting object to Pod: %w", err)
	}
	return pod, nil
}

func (p *PodREST) NewList() runtime.Object {
	return &api.PodList{}
}

func (p *PodREST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}

	namespace := genericapirequest.NamespaceValue(ctx)
	objs, err := p.podLister.ByNamespace(namespace).List(label)
	if err != nil {
		klog.ErrorS(err, "Failed listing pods", "labelSelector", label, "namespace", klog.KRef("", namespace))
		return nil, fmt.Errorf("failed listing pods: %w", err)
	}

	field := fields.Everything()
	if options != nil && options.FieldSelector != nil {
		field = options.FieldSelector
	}
	pods := convertAndFilterPodObject(objs, field)
	return &api.PodList{Items: pods}, nil
}

func (p *PodREST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return tableConvertor.ConvertToTable(ctx, object, tableOptions)
}

func (p *PodREST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}

	namespace := genericapirequest.NamespaceValue(ctx)

	clusters, err := p.federatedInformerManager.GetReadyClusters()
	if err != nil {
		klog.ErrorS(err, "Failed watching pods", "labelSelector", label, "namespace", klog.KRef("", namespace))
		return nil, fmt.Errorf("failed watching pods: %w", err)
	}

	// TODO: support cluster addition and deletion during the watch
	watchClusters := sets.Set[string]{}
	proxyCh := make(chan watch.Event)
	proxyWatcher := watch.NewProxyWatcher(proxyCh)
	for i := range clusters {
		client, exist := p.federatedInformerManager.GetClusterKubeClient(clusters[i].Name)
		if !exist {
			continue
		}
		watcher, err := client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector: label.String(),
			FieldSelector: options.FieldSelector.String(),
		})
		if err != nil {
			continue
		}
		watchClusters.Insert(clusters[i].Name)
		go func(cluster string) {
			defer watcher.Stop()
			for {
				select {
				case <-proxyWatcher.StopChan():
					return
				case event, ok := <-watcher.ResultChan():
					if !ok {
						watchClusters.Delete(cluster)
						if watchClusters.Len() == 0 {
							close(proxyCh)
						}
						return
					}
					if pod, ok := event.Object.(*corev1.Pod); ok {
						aggregatedlister.MakePodUnique(pod, cluster)
						newPod := &api.Pod{}
						if err := scheme.Convert(pod, newPod, nil); err != nil {
							continue
						}
						event.Object = newPod
					}
					proxyCh <- event
				}
			}
		}(clusters[i].Name)
	}
	return proxyWatcher, nil
}

func convertAndFilterPodObject(objs []runtime.Object, selector fields.Selector) []api.Pod {
	newObjs := make([]api.Pod, 0, len(objs))
	for _, obj := range objs {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		fields := ToSelectableFields(pod)
		if !selector.Matches(fields) {
			continue
		}
		newPod := &api.Pod{}
		if err := scheme.Convert(obj, newPod, nil); err != nil {
			continue
		}
		newObjs = append(newObjs, *newPod)
	}
	return newObjs
}

// ToSelectableFields returns a field set that represents the object
// TODO: fields are not labels, and the validation rules for them do not apply.
func ToSelectableFields(pod *corev1.Pod) fields.Set {
	// The purpose of allocation with a given number of elements is to reduce
	// amount of allocations needed to create the fields.Set. If you add any
	// field here or the number of object-meta related fields changes, this should
	// be adjusted.
	podSpecificFieldsSet := make(fields.Set, 10)
	podSpecificFieldsSet["spec.nodeName"] = pod.Spec.NodeName
	podSpecificFieldsSet["spec.restartPolicy"] = string(pod.Spec.RestartPolicy)
	podSpecificFieldsSet["spec.schedulerName"] = pod.Spec.SchedulerName
	podSpecificFieldsSet["spec.serviceAccountName"] = pod.Spec.ServiceAccountName
	podSpecificFieldsSet["spec.hostNetwork"] = strconv.FormatBool(pod.Spec.HostNetwork)
	podSpecificFieldsSet["status.phase"] = string(pod.Status.Phase)
	// TODO: add podIPs as a downward API value(s) with proper format
	podIP := ""
	if len(pod.Status.PodIPs) > 0 {
		podIP = pod.Status.PodIPs[0].IP
	}
	podSpecificFieldsSet["status.podIP"] = podIP
	podSpecificFieldsSet["status.nominatedNodeName"] = pod.Status.NominatedNodeName
	return generic.AddObjectMetaFieldsSet(podSpecificFieldsSet, &pod.ObjectMeta, true)
}
