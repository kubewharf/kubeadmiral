// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1alpha1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeClusterCollectedStatuses implements ClusterCollectedStatusInterface
type FakeClusterCollectedStatuses struct {
	Fake *FakeCoreV1alpha1
}

var clustercollectedstatusesResource = schema.GroupVersionResource{Group: "core.kubeadmiral.io", Version: "v1alpha1", Resource: "clustercollectedstatuses"}

var clustercollectedstatusesKind = schema.GroupVersionKind{Group: "core.kubeadmiral.io", Version: "v1alpha1", Kind: "ClusterCollectedStatus"}

// Get takes name of the clusterCollectedStatus, and returns the corresponding clusterCollectedStatus object, and an error if there is any.
func (c *FakeClusterCollectedStatuses) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.ClusterCollectedStatus, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(clustercollectedstatusesResource, name), &v1alpha1.ClusterCollectedStatus{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ClusterCollectedStatus), err
}

// List takes label and field selectors, and returns the list of ClusterCollectedStatuses that match those selectors.
func (c *FakeClusterCollectedStatuses) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.ClusterCollectedStatusList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(clustercollectedstatusesResource, clustercollectedstatusesKind, opts), &v1alpha1.ClusterCollectedStatusList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.ClusterCollectedStatusList{ListMeta: obj.(*v1alpha1.ClusterCollectedStatusList).ListMeta}
	for _, item := range obj.(*v1alpha1.ClusterCollectedStatusList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested clusterCollectedStatuses.
func (c *FakeClusterCollectedStatuses) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(clustercollectedstatusesResource, opts))
}

// Create takes the representation of a clusterCollectedStatus and creates it.  Returns the server's representation of the clusterCollectedStatus, and an error, if there is any.
func (c *FakeClusterCollectedStatuses) Create(ctx context.Context, clusterCollectedStatus *v1alpha1.ClusterCollectedStatus, opts v1.CreateOptions) (result *v1alpha1.ClusterCollectedStatus, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(clustercollectedstatusesResource, clusterCollectedStatus), &v1alpha1.ClusterCollectedStatus{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ClusterCollectedStatus), err
}

// Update takes the representation of a clusterCollectedStatus and updates it. Returns the server's representation of the clusterCollectedStatus, and an error, if there is any.
func (c *FakeClusterCollectedStatuses) Update(ctx context.Context, clusterCollectedStatus *v1alpha1.ClusterCollectedStatus, opts v1.UpdateOptions) (result *v1alpha1.ClusterCollectedStatus, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(clustercollectedstatusesResource, clusterCollectedStatus), &v1alpha1.ClusterCollectedStatus{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ClusterCollectedStatus), err
}

// Delete takes name of the clusterCollectedStatus and deletes it. Returns an error if one occurs.
func (c *FakeClusterCollectedStatuses) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteAction(clustercollectedstatusesResource, name), &v1alpha1.ClusterCollectedStatus{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeClusterCollectedStatuses) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(clustercollectedstatusesResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.ClusterCollectedStatusList{})
	return err
}

// Patch applies the patch and returns the patched clusterCollectedStatus.
func (c *FakeClusterCollectedStatuses) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.ClusterCollectedStatus, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(clustercollectedstatusesResource, name, pt, data, subresources...), &v1alpha1.ClusterCollectedStatus{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ClusterCollectedStatus), err
}