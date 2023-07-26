/*
Copyright 2018 The Kubernetes Authors.

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
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
)

var mutex = sync.Mutex{}

// meters to store the number of objects in informers, for example:
// {"/v1/secrets": {"11111": 10; "22222": 12}}
// "11111" is a random string to identify a informer.
var meters = make(map[string]map[string]int)

// NewResourceInformer returns an unfiltered informer.
func NewResourceInformer(
	client ResourceClient,
	namespace string,
	triggerFunc func(pkgruntime.Object),
	metrics stats.Metrics,
) (cache.Store, cache.Controller) {
	return newResourceInformer(client, namespace, NewTriggerOnAllChanges(triggerFunc), "", map[string]string{}, metrics)
}

func NewResourceInformerWithEventHandler(
	client ResourceClient,
	namespace string,
	eventHandler cache.ResourceEventHandler,
	metrics stats.Metrics,
) (cache.Store, cache.Controller) {
	return newResourceInformer(client, namespace, eventHandler, "", map[string]string{}, metrics)
}

// NewManagedResourceInformer returns an informer limited to resources
// managed by KubeFed as indicated by labeling.
func NewManagedResourceInformer(
	client ResourceClient,
	namespace string,
	triggerFunc func(pkgruntime.Object),
	extraTags map[string]string,
	metrics stats.Metrics,
) (cache.Store, cache.Controller) {
	labelSelector := labels.Set(map[string]string{managedlabel.ManagedByKubeAdmiralLabelKey: managedlabel.ManagedByKubeAdmiralLabelValue}).
		AsSelector().
		String()
	return newResourceInformer(
		client,
		namespace,
		NewTriggerOnAllChanges(triggerFunc),
		labelSelector,
		extraTags,
		metrics,
	)
}

func newResourceInformer(
	client ResourceClient,
	namespace string,
	eventHandler cache.ResourceEventHandler,
	labelSelector string,
	extraTags map[string]string,
	metrics stats.Metrics,
) (cache.Store, cache.Controller) {
	store, controller := cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (pkgruntime.Object, error) {
				options.LabelSelector = labelSelector
				return client.Resources(namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector
				return client.Resources(namespace).Watch(context.TODO(), options)
			},
		},
		nil, // Skip checks for expected type since the type will depend on the client
		NoResyncPeriod,
		eventHandler,
	)

	rand.Seed(time.Now().UnixNano())
	uid := strconv.Itoa(rand.Int())
	gvk := client.GVKString()
	klog.V(4).Infof("LabelSelector: %s, namespace: %s", labelSelector, namespace)

	go func(metrics stats.Metrics) {
		baseTags := []stats.Tag{{Name: "type", Value: client.GVKString()}}
		tags := []stats.Tag{{Name: "type", Value: client.GVKString()}}
		for k, v := range extraTags {
			tags = append(tags, stats.Tag{Name: k, Value: v})
		}
		for {
			select {
			case <-time.After(time.Second * 30):
			}

			num := len(store.ListKeys())
			metrics.Store("store.count", num, tags...)

			mutex.Lock()
			v, ok := meters[gvk]
			if ok {
				v[uid] = num
			} else {
				m := map[string]int{uid: num}
				meters[gvk] = m
			}

			sum := 0
			for _, vv := range meters[gvk] {
				sum = sum + vv
			}
			klog.V(7).Infof("Type %s has total %d object, %d informer", gvk, sum, len(meters[gvk]))
			mutex.Unlock()
			metrics.Store("store.totalcount", sum, baseTags...)
		}
	}(metrics)
	return store, controller
}

func ObjFromCache(store cache.Store, kind, key string) (*unstructured.Unstructured, error) {
	obj, err := rawObjFromCache(store, kind, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*unstructured.Unstructured), nil
}

func rawObjFromCache(store cache.Store, kind, key string) (pkgruntime.Object, error) {
	cachedObj, exist, err := store.GetByKey(key)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "Failed to query %s store for %q", kind, key)
		runtime.HandleError(wrappedErr)
		return nil, err
	}
	if !exist {
		return nil, nil
	}
	return cachedObj.(pkgruntime.Object).DeepCopyObject(), nil
}
