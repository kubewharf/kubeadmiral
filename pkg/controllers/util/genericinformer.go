/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package util

import (
	"context"
	"reflect"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	NoResyncPeriod = 0 * time.Second
)

func NewGenericInformer(
	config *rest.Config,
	namespace string,
	obj pkgruntime.Object,
	resyncPeriod time.Duration,
	triggerFunc func(pkgruntime.Object),
	metrics stats.Metrics,
) (cache.Store, cache.Controller, error) {
	return NewGenericInformerWithEventHandler(
		config,
		namespace,
		obj,
		resyncPeriod,
		NewTriggerOnAllChanges(triggerFunc),
		metrics,
	)
}

func NewGenericInformerWithEventHandler(
	config *rest.Config,
	namespace string,
	obj pkgruntime.Object,
	resyncPeriod time.Duration,
	resourceEventHandlerFuncs *cache.ResourceEventHandlerFuncs,
	metrics stats.Metrics,
) (cache.Store, cache.Controller, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme.Scheme)
	if err != nil {
		return nil, nil, err
	}
	mapper, err := apiutil.NewDiscoveryRESTMapper(config)
	if err != nil {
		return nil, nil, errors.Wrap(err, "Could not create RESTMapper from config")
	}

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, nil, err
	}

	client, err := apiutil.RESTClientForGVK(gvk, false, config, scheme.Codecs)
	if err != nil {
		return nil, nil, err
	}

	listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")
	listObj, err := scheme.Scheme.New(listGVK)
	if err != nil {
		return nil, nil, err
	}
	klog.V(4).Infof("New informer for gvk: %+v", gvk)
	store, controller := cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(opts metav1.ListOptions) (pkgruntime.Object, error) {
				res := listObj.DeepCopyObject()
				isNamespaceScoped := namespace != "" &&
					mapping.Scope.Name() != meta.RESTScopeNameRoot
				err := client.Get().
					NamespaceIfScoped(namespace, isNamespaceScoped).
					Resource(mapping.Resource.Resource).
					VersionedParams(&opts, scheme.ParameterCodec).
					Do(context.TODO()).
					Into(res)
				return res, err
			},
			WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
				// Watch needs to be set to true separately
				opts.Watch = true
				isNamespaceScoped := namespace != "" &&
					mapping.Scope.Name() != meta.RESTScopeNameRoot
				return client.Get().
					NamespaceIfScoped(namespace, isNamespaceScoped).
					Resource(mapping.Resource.Resource).
					VersionedParams(&opts, scheme.ParameterCodec).
					Watch(context.TODO())
			},
		},
		obj,
		resyncPeriod,
		resourceEventHandlerFuncs,
	)
	go func(metrics stats.Metrics) {
		tags := []stats.Tag{{Name: "type", Value: gvk.Group + "/" + gvk.Version + "/" + gvk.Kind}}
		for {
			select {
			case <-time.After(time.Second * 30):
			}
			metrics.Store("store.count", len(store.ListKeys()), tags...)
		}
	}(metrics)
	return store, controller, nil
}

// NewTriggerOnAllChanges returns cache.ResourceEventHandlerFuncs that trigger the given function
// on all object changes.
func NewTriggerOnAllChanges(triggerFunc func(pkgruntime.Object)) *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(old interface{}) {
			if deleted, ok := old.(cache.DeletedFinalStateUnknown); ok {
				// This object might be stale but ok for our current usage.
				old = deleted.Obj
				if old == nil {
					return
				}
			}
			oldObj := old.(pkgruntime.Object)
			triggerFunc(oldObj)
		},
		AddFunc: func(cur interface{}) {
			curObj := cur.(pkgruntime.Object)
			triggerFunc(curObj)
		},
		UpdateFunc: func(old, cur interface{}) {
			curObj := cur.(pkgruntime.Object)
			if !reflect.DeepEqual(old, cur) {
				triggerFunc(curObj)
			}
		},
	}
}

// NewTriggerOnGenerationChanges returns cache.ResourceEventHandlerFuncs that trigger the given function
// on generation changes.
func NewTriggerOnGenerationChanges(
	triggerFunc func(pkgruntime.Object),
) *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(old interface{}) {
			if deleted, ok := old.(cache.DeletedFinalStateUnknown); ok {
				// This object might be stale but ok for our current usage.
				old = deleted.Obj
				if old == nil {
					return
				}
			}
			oldObj := old.(pkgruntime.Object)
			triggerFunc(oldObj)
		},
		AddFunc: func(cur interface{}) {
			curObj := cur.(pkgruntime.Object)
			triggerFunc(curObj)
		},
		UpdateFunc: func(old, cur interface{}) {
			oldObj, err := meta.Accessor(old)
			if err != nil {
				return
			}
			curObj, err := meta.Accessor(cur)
			if err != nil {
				return
			}
			if oldObj.GetGeneration() != curObj.GetGeneration() {
				triggerFunc(cur.(pkgruntime.Object))
			}
		},
	}
}

func NewTriggerOnGenerationAndMetadataChanges(
	triggerFunc func(pkgruntime.Object),
	shouldTrigger func(oldMeta metav1.Object, newMeta metav1.Object) bool,
) *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(old interface{}) {
			if deleted, ok := old.(cache.DeletedFinalStateUnknown); ok {
				// This object might be stale but ok for our current usage.
				old = deleted.Obj
				if old == nil {
					return
				}
			}
			oldObj := old.(pkgruntime.Object)
			triggerFunc(oldObj)
		},
		AddFunc: func(cur interface{}) {
			curObj := cur.(pkgruntime.Object)
			triggerFunc(curObj)
		},
		UpdateFunc: func(old, cur interface{}) {
			oldObj, err := meta.Accessor(old)
			if err != nil {
				return
			}
			curObj, err := meta.Accessor(cur)
			if err != nil {
				return
			}
			if oldObj.GetGeneration() != curObj.GetGeneration() ||
				shouldTrigger(oldObj, curObj) {
				triggerFunc(cur.(pkgruntime.Object))
			}
		},
	}
}
