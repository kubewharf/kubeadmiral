package resource

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
	v1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/metrics"
	_ "k8s.io/metrics/pkg/apis/metrics/install"
)

type NodeMetrics struct {
	groupResource schema.GroupResource
	metrics       NodeMetricsGetter
	nodeLister    v1listers.NodeLister
	nodeSelector  []labels.Requirement
}

var _ rest.KindProvider = &NodeMetrics{}
var _ rest.Storage = &NodeMetrics{}
var _ rest.Getter = &NodeMetrics{}
var _ rest.Lister = &NodeMetrics{}
var _ rest.Scoper = &NodeMetrics{}
var _ rest.TableConvertor = &NodeMetrics{}

func NewNodeMetrics(
	groupResource schema.GroupResource,
	metrics NodeMetricsGetter,
	nodeLister v1listers.NodeLister,
	nodeSelector []labels.Requirement,
) *NodeMetrics {
	registerIntoLegacyRegistryOnce.Do(func() {
		err := RegisterAPIMetrics(legacyregistry.Register)
		if err != nil {
			klog.ErrorS(err, "Failed to register resource metrics")
		}
	})

	return &NodeMetrics{
		groupResource: groupResource,
		metrics:       metrics,
		nodeLister:    nodeLister,
		nodeSelector:  nodeSelector,
	}
}

// New implements rest.Storage interface
func (m *NodeMetrics) New() runtime.Object {
	return &metrics.NodeMetrics{}
}

// Destroy implements rest.Storage interface
func (m *NodeMetrics) Destroy() {
}

// Kind implements rest.KindProvider interface
func (m *NodeMetrics) Kind() string {
	return "NodeMetrics"
}

// NewList implements rest.Lister interface
func (m *NodeMetrics) NewList() runtime.Object {
	return &metrics.NodeMetricsList{}
}

// List implements rest.Lister interface
func (m *NodeMetrics) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	nodes, err := m.nodes(ctx, options)
	if err != nil {
		return &metrics.NodeMetricsList{}, err
	}

	ms, err := m.getMetrics(nodes...)
	if err != nil {
		klog.ErrorS(err, "Failed reading nodes metrics")
		return &metrics.NodeMetricsList{}, fmt.Errorf("failed reading nodes metrics: %w", err)
	}
	return &metrics.NodeMetricsList{Items: ms}, nil
}

func (m *NodeMetrics) nodes(ctx context.Context, options *metainternalversion.ListOptions) ([]*corev1.Node, error) {
	labelSelector := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		labelSelector = options.LabelSelector
	}
	if m.nodeSelector != nil {
		labelSelector = labelSelector.Add(m.nodeSelector...)
	}
	nodes, err := m.nodeLister.List(labelSelector)
	if err != nil {
		klog.ErrorS(err, "Failed listing nodes", "labelSelector", labelSelector)
		return nil, fmt.Errorf("failed listing nodes: %w", err)
	}
	if options != nil && options.FieldSelector != nil {
		nodes = filterNodes(nodes, options.FieldSelector)
	}
	return nodes, nil
}

// Get implements rest.Getter interface
func (m *NodeMetrics) Get(ctx context.Context, name string, opts *metav1.GetOptions) (runtime.Object, error) {
	node, err := m.nodeLister.Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			// return not-found errors directly
			return nil, err
		}
		klog.ErrorS(err, "Failed getting node", "node", klog.KRef("", name))
		return nil, fmt.Errorf("failed getting node: %w", err)
	}
	if node == nil {
		return nil, errors.NewNotFound(m.groupResource, name)
	}
	ms, err := m.getMetrics(node)
	if err != nil {
		klog.ErrorS(err, "Failed reading node metrics", "node", klog.KRef("", name))
		return nil, fmt.Errorf("failed reading node metrics: %w", err)
	}
	if len(ms) == 0 {
		return nil, errors.NewNotFound(m.groupResource, name)
	}
	return &ms[0], nil
}

// ConvertToTable implements rest.TableConvertor interface
func (m *NodeMetrics) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1beta1.Table, error) {
	var table metav1beta1.Table

	switch t := object.(type) {
	case *metrics.NodeMetrics:
		table.ResourceVersion = t.ResourceVersion
		table.SelfLink = t.SelfLink //nolint:staticcheck // keep deprecated field to be backward compatible
		addNodeMetricsToTable(&table, *t)
	case *metrics.NodeMetricsList:
		table.ResourceVersion = t.ResourceVersion
		table.SelfLink = t.SelfLink //nolint:staticcheck // keep deprecated field to be backward compatible
		table.Continue = t.Continue
		addNodeMetricsToTable(&table, t.Items...)
	default:
	}

	return &table, nil
}

func (m *NodeMetrics) getMetrics(nodes ...*corev1.Node) ([]metrics.NodeMetrics, error) {
	ms, err := m.metrics.GetNodeMetrics(nodes...)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		metricFreshness.WithLabelValues().Observe(myClock.Since(m.Timestamp.Time).Seconds())
	}
	// maintain the same ordering invariant as the Kube API would over nodes
	sort.Slice(ms, func(i, j int) bool {
		return ms[i].Name < ms[j].Name
	})
	return ms, nil
}

// NamespaceScoped implements rest.Scoper interface
func (m *NodeMetrics) NamespaceScoped() bool {
	return false
}
