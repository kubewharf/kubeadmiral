// Copyright 2018 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resource

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/metrics"
	_ "k8s.io/metrics/pkg/apis/metrics/install"
)

type PodMetrics struct {
	groupResource schema.GroupResource
	metrics       PodMetricsGetter
	podLister     cache.GenericLister
}

var _ rest.KindProvider = &PodMetrics{}
var _ rest.Storage = &PodMetrics{}
var _ rest.Getter = &PodMetrics{}
var _ rest.Lister = &PodMetrics{}
var _ rest.TableConvertor = &PodMetrics{}
var _ rest.Scoper = &PodMetrics{}

func NewPodMetrics(groupResource schema.GroupResource, metrics PodMetricsGetter, podLister cache.GenericLister) *PodMetrics {
	registerIntoLegacyRegistryOnce.Do(func() {
		err := RegisterAPIMetrics(legacyregistry.Register)
		if err != nil {
			klog.ErrorS(err, "Failed to register resource metrics")
		}
	})

	return &PodMetrics{
		groupResource: groupResource,
		metrics:       metrics,
		podLister:     podLister,
	}
}

// New implements rest.Storage interface
func (m *PodMetrics) New() runtime.Object {
	return &metrics.PodMetrics{}
}

// Destroy implements rest.Storage interface
func (m *PodMetrics) Destroy() {
}

// Kind implements rest.KindProvider interface
func (m *PodMetrics) Kind() string {
	return "PodMetrics"
}

// NewList implements rest.Lister interface
func (m *PodMetrics) NewList() runtime.Object {
	return &metrics.PodMetricsList{}
}

// List implements rest.Lister interface
func (m *PodMetrics) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	pods, err := m.pods(ctx, options)
	if err != nil {
		return &metrics.PodMetricsList{}, err
	}
	ms, err := m.getMetrics(pods...)
	if err != nil {
		namespace := genericapirequest.NamespaceValue(ctx)
		klog.ErrorS(err, "Failed reading pods metrics", "namespace", klog.KRef("", namespace))
		return &metrics.PodMetricsList{}, fmt.Errorf("failed reading pods metrics: %w", err)
	}
	return &metrics.PodMetricsList{Items: ms}, nil
}

func (m *PodMetrics) pods(ctx context.Context, options *metainternalversion.ListOptions) ([]runtime.Object, error) {
	labelSelector := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		labelSelector = options.LabelSelector
	}

	namespace := genericapirequest.NamespaceValue(ctx)
	pods, err := m.podLister.ByNamespace(namespace).List(labelSelector)
	if err != nil {
		klog.ErrorS(err, "Failed listing pods", "labelSelector", labelSelector, "namespace", klog.KRef("", namespace))
		return nil, fmt.Errorf("failed listing pods: %w", err)
	}

	partialPods := make([]runtime.Object, 0, len(pods))
	for _, obj := range pods {
		var partialObj *metav1.PartialObjectMetadata
		switch t := obj.(type) {
		case *metav1.PartialObjectMetadata:
			partialObj = t
		case metav1.Object:
			partialObj = meta.AsPartialObjectMetadata(t)
		default:
			continue
		}
		partialPods = append(partialPods, partialObj)
	}

	if options != nil && options.FieldSelector != nil {
		partialPods = filterPartialObjectMetadata(partialPods, options.FieldSelector)
	}
	return partialPods, err
}

// Get implements rest.Getter interface
func (m *PodMetrics) Get(ctx context.Context, name string, opts *metav1.GetOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)

	obj, err := m.podLister.ByNamespace(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			// return not-found errors directly
			return &metrics.PodMetrics{}, err
		}
		klog.ErrorS(err, "Failed getting pod", "pod", klog.KRef(namespace, name))
		return &metrics.PodMetrics{}, fmt.Errorf("failed getting pod: %w", err)
	}
	if obj == nil {
		return &metrics.PodMetrics{}, errors.NewNotFound(corev1.Resource("pods"), fmt.Sprintf("%s/%s", namespace, name))
	}

	var partialPod *metav1.PartialObjectMetadata
	switch t := obj.(type) {
	case *metav1.PartialObjectMetadata:
		partialPod = t
	case metav1.Object:
		partialPod = meta.AsPartialObjectMetadata(t)
	default:
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	ms, err := m.getMetrics(meta.AsPartialObjectMetadata(partialPod))
	if err != nil {
		klog.ErrorS(err, "Failed reading pod metrics", "pod", klog.KRef(namespace, name))
		return nil, fmt.Errorf("failed pod metrics: %w", err)
	}
	if len(ms) == 0 {
		return nil, errors.NewNotFound(m.groupResource, fmt.Sprintf("%s/%s", namespace, name))
	}
	return &ms[0], nil
}

// ConvertToTable implements rest.TableConvertor interface
func (m *PodMetrics) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1beta1.Table, error) {
	var table metav1beta1.Table

	switch t := object.(type) {
	case *metrics.PodMetrics:
		table.ResourceVersion = t.ResourceVersion
		table.SelfLink = t.SelfLink //nolint:staticcheck // keep deprecated field to be backward compatible
		addPodMetricsToTable(&table, *t)
	case *metrics.PodMetricsList:
		table.ResourceVersion = t.ResourceVersion
		table.SelfLink = t.SelfLink //nolint:staticcheck // keep deprecated field to be backward compatible
		table.Continue = t.Continue
		addPodMetricsToTable(&table, t.Items...)
	default:
	}

	return &table, nil
}

func (m *PodMetrics) getMetrics(pods ...runtime.Object) ([]metrics.PodMetrics, error) {
	objs := make([]*metav1.PartialObjectMetadata, len(pods))
	for i, pod := range pods {
		objs[i] = pod.(*metav1.PartialObjectMetadata)
	}
	ms, err := m.metrics.GetPodMetrics(objs...)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		metricFreshness.WithLabelValues().Observe(myClock.Since(m.Timestamp.Time).Seconds())
	}
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Namespace != ms[j].Namespace {
			return ms[i].Namespace < ms[j].Namespace
		}
		return ms[i].Name < ms[j].Name
	})
	return ms, nil
}

// NamespaceScoped implements rest.Scoper interface
func (m *PodMetrics) NamespaceScoped() bool {
	return true
}
