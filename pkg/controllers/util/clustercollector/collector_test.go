/*
Copyright 2016 The Kubernetes Authors.

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

package clustercollector

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/listwatcher"
)

func setupFakeClient(
	numPods, podResourceUnits, numNodes, nodeResourceUnits int,
) (pods []runtime.Object, nodes []runtime.Object, client kubernetes.Interface) {
	podsAndNodes := make([]runtime.Object, numPods+numNodes)

	pods = make([]runtime.Object, numPods)
	for i := 1; i <= numPods; i++ {
		pods[i-1] = newPod(i, 1)
		podsAndNodes[i-1] = pods[i-1]
	}
	nodes = make([]runtime.Object, numNodes)
	for i := 1; i <= numNodes; i++ {
		nodes[i-1] = newNode(i, 10)
		podsAndNodes[i+numPods-1] = nodes[i-1]
	}

	client = fake.NewSimpleClientset(podsAndNodes...)
	return pods, nodes, client
}

var resourceUnit = corev1.ResourceList{
	corev1.ResourceCPU:    resource.MustParse("8"),
	corev1.ResourceMemory: resource.MustParse("1Gi"),
	"custom-resource":     resource.MustParse("1"),
}

// newResourceList generates a new ResourceList corresponding to number of resource units (see resourceUnit)
func newResourceList(numUnits int) corev1.ResourceList {
	resource := corev1.ResourceList{}
	for i := 0; i < numUnits; i++ {
		for k, v := range resourceUnit {
			if quant, ok := resource[k]; ok {
				quant.Add(v)
				resource[k] = quant
			} else {
				resource[k] = v
			}
		}
	}
	return resource
}

// newPod generates a new pod with the given number of requested resource units (see resourceUnit)
func newPod(id, requestUnits int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("pod-%d", id),
			UID:  uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: newResourceList(requestUnits),
					},
				},
			},
		},
	}
}

// newNode generates a new node with the given number of allocatable resource units (see resourceUnit)
func newNode(id, allocatableUnits int) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("node-%d", id),
		},
		Status: corev1.NodeStatus{
			Allocatable: newResourceList(allocatableUnits),
		},
	}
}

func newCollectorFromClient(client kubernetes.Interface) *ClusterCollector {
	return &ClusterCollector{
		mu: sync.Mutex{},
		listWatcher: listwatcher.NewPagedListWatcher(
			&cache.ListWatch{
				ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
					return client.CoreV1().Pods(corev1.NamespaceAll).List(context.Background(), options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					return client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.Background(), options)
				},
			},
			500,
		),
		receiver:         make(chan listwatcher.Event),
		nodeLister:       &FakeNodeLister{clientset: client},
		nodeListerSynced: func() bool { return true },
	}
}

func completePod(pod *corev1.Pod, client kubernetes.Interface) error {
	if rand.Int()%2 == 0 {
		pod.Status.Phase = corev1.PodFailed
	} else {
		pod.Status.Phase = corev1.PodSucceeded
	}

	_, err := client.CoreV1().Pods(corev1.NamespaceAll).Update(
		context.Background(),
		pod,
		metav1.UpdateOptions{},
	)

	return err
}

//nolint:dupl
func pollUntilResourceMatch(collector *ClusterCollector, expected ClusterResources, timeout time.Duration) (ClusterResources, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var resources ClusterResources

	pollFn := func() (done bool, err error) {
		resources, err = collector.GetClusterResources()
		if err != nil {
			return false, err
		}

		if !equality.Semantic.DeepEqual(expected.Allocatable, resources.Allocatable) {
			return false, nil
		}
		if !equality.Semantic.DeepEqual(expected.Available, resources.Available) {
			return false, nil
		}
		if expected.SchedulableNodes != resources.SchedulableNodes {
			return false, nil
		}

		return true, nil
	}
	err := wait.PollUntil(time.Millisecond, pollFn, ctx.Done())

	if errors.Is(err, wait.ErrWaitTimeout) {
		return resources, false, nil
	}

	return resources, true, err
}

//nolint:dupl
func ensureReourceAlwaysMatch(
	collector *ClusterCollector,
	expected ClusterResources,
	duration time.Duration,
) (ClusterResources, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var resources ClusterResources

	pollFn := func() (done bool, err error) {
		resources, err = collector.GetClusterResources()
		if err != nil {
			return false, err
		}

		if !equality.Semantic.DeepEqual(expected.Allocatable, resources.Allocatable) {
			return true, nil
		}
		if !equality.Semantic.DeepEqual(expected.Available, resources.Available) {
			return true, nil
		}
		if expected.SchedulableNodes != resources.SchedulableNodes {
			return true, nil
		}

		return false, nil
	}
	err := wait.PollUntil(time.Millisecond, pollFn, ctx.Done())

	if errors.Is(err, wait.ErrWaitTimeout) {
		// if the poll times out, it means that the the resource always matched
		return resources, true, nil
	}

	return resources, false, err
}

func TestCollectorShouldHandleInitialList(t *testing.T) {
	_, _, client := setupFakeClient(30, 1, 10, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	resources, err := collector.GetClusterResources()
	if err != nil {
		t.Fatalf("Unexpected error when getting cluster resources: %v", err)
	}

	expectedAllocatable := newResourceList(100)
	expectedAvailable := newResourceList(70)
	expectedSchedulableNodes := int64(10)

	if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
		t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
	}
	if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
		t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
	}
	if expectedSchedulableNodes != resources.SchedulableNodes {
		t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
	}
}

func TestCollectorShouldHandleInitialListWithCompletedPods(t *testing.T) {
	pods, _, client := setupFakeClient(30, 1, 10, 10)
	for i := 0; i < 10; i++ {
		pod := pods[i].(*corev1.Pod)
		if err := completePod(pod, client); err != nil {
			t.Fatalf("Unexpected error when updating pod: %v", err)
		}
	}
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	resources, err := collector.GetClusterResources()
	if err != nil {
		t.Fatalf("Unexpected error when getting cluster resources: %v", err)
	}

	expectedAllocatable := newResourceList(100)
	expectedAvailable := newResourceList(80)
	expectedSchedulableNodes := int64(10)

	if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
		t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
	}
	if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
		t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
	}
	if expectedSchedulableNodes != resources.SchedulableNodes {
		t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
	}
}

func TestCollectorShouldHandlePodCreation(t *testing.T) {
	_, _, client := setupFakeClient(30, 1, 12, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	for i := 1; i <= 40; i++ {
		if _, err := client.CoreV1().Pods(corev1.NamespaceAll).Create(
			context.Background(),
			newPod(i+30, 1),
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when creating pods: %v", err)
		}
	}

	expectedAllocatable := newResourceList(120)
	expectedAvailable := newResourceList(50)
	expectedSchedulableNodes := int64(12)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := pollUntilResourceMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

func TestCollectorShouldHandlePodCompletions(t *testing.T) {
	pods, _, client := setupFakeClient(80, 1, 15, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	for i := 0; i < 30; i++ {
		pod := pods[i].(*corev1.Pod)
		if err := completePod(pod, client); err != nil {
			t.Fatalf("Unexpected error when updating pod: %v", err)
		}
	}

	expectedAllocatable := newResourceList(150)
	expectedAvailable := newResourceList(100)
	expectedSchedulableNodes := int64(15)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := pollUntilResourceMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

func TestCollectorShouldIgnorePodUpdatesThatAreNotCompletions(t *testing.T) {
	pods, _, client := setupFakeClient(80, 1, 15, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	for i := 0; i <= 30; i++ {
		pod := pods[i].(*corev1.Pod)
		pod.Annotations = map[string]string{"test": "test"}

		if _, err := client.CoreV1().Pods(corev1.NamespaceAll).Update(
			context.Background(),
			pod,
			metav1.UpdateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when updating pods: %v", err)
		}
	}

	var resources ClusterResources
	expectedAllocatable := newResourceList(150)
	expectedAvailable := newResourceList(70)
	expectedSchedulableNodes := int64(15)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := ensureReourceAlwaysMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

func TestCollectorShouldHandlePodDeletion(t *testing.T) {
	pods, _, client := setupFakeClient(80, 1, 12, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	for i := 0; i < 10; i++ {
		pod := pods[i].(*corev1.Pod).Name
		if err := client.CoreV1().Pods(corev1.NamespaceAll).Delete(
			context.Background(),
			pod,
			metav1.DeleteOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when deleting pods: %v", err)
		}
	}

	var resources ClusterResources
	expectedAllocatable := newResourceList(120)
	expectedAvailable := newResourceList(50)
	expectedSchedulableNodes := int64(12)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := pollUntilResourceMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

func TestCollectorShouldIgnoreDeletionOfCompletedPods(t *testing.T) {
	pods, _, client := setupFakeClient(80, 1, 12, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	for i := 0; i < 10; i++ {
		pod := pods[i].(*corev1.Pod)
		if err := completePod(pod, client); err != nil {
			t.Fatalf("Unexpected error when updating pod: %v", err)
		}
	}

	for i := 0; i < 10; i++ {
		pod := pods[i].(*corev1.Pod).Name
		if err := client.CoreV1().Pods(corev1.NamespaceAll).Delete(
			context.Background(),
			pod,
			metav1.DeleteOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when deleting pods: %v", err)
		}
	}

	var resources ClusterResources
	expectedAllocatable := newResourceList(120)
	expectedAvailable := newResourceList(50)
	expectedSchedulableNodes := int64(12)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := pollUntilResourceMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}

	resources, match, err = ensureReourceAlwaysMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

func TestCollectorShouldHandleUnschedulableNodes(t *testing.T) {
	_, nodes, client := setupFakeClient(0, 0, 10, 10)
	collector := newCollectorFromClient(client)

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	for i := 0; i < 5; i++ {
		node := nodes[i].(*corev1.Node)
		node.Spec.Unschedulable = true

		if _, err := client.CoreV1().Nodes().Update(
			context.Background(),
			node,
			metav1.UpdateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when updating nodes: %v", err)
		}
	}

	var resources ClusterResources
	expectedAllocatable := newResourceList(50)
	expectedAvailable := newResourceList(50)
	expectedSchedulableNodes := int64(5)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := pollUntilResourceMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

func TestCollectorShouldHandleRelists(t *testing.T) {
	_, _, client := setupFakeClient(50, 1, 10, 10)

	// listCh is used to receive notifications of list requests
	listCh := make(chan struct{}, 100)
	// watchCh is used to start termination of watch connections
	watchCh := make(chan struct{})
	collector := &ClusterCollector{
		mu: sync.Mutex{},
		listWatcher: listwatcher.NewPagedListWatcher(
			&cache.ListWatch{
				ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
					listCh <- struct{}{}
					return client.CoreV1().Pods(corev1.NamespaceAll).List(context.Background(), options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					w, err := client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.Background(), options)
					// close watch connection to trigger relists
					go func() {
						<-watchCh
						time.AfterFunc(5*time.Microsecond, func() {
							w.Stop()
						})
					}()
					return w, err
				},
			},
			500,
		),
		receiver:         make(chan listwatcher.Event),
		nodeLister:       &FakeNodeLister{clientset: client},
		nodeListerSynced: func() bool { return true },
	}

	ctx := context.Background()
	collector.Start(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
		t.Fatal("Failed to wait for cache sync")
	}

	close(watchCh)

	// create some pods
	for i := 1; i <= 20; i++ {
		if _, err := client.CoreV1().Pods(corev1.NamespaceAll).Create(
			context.Background(),
			newPod(i+50, 1),
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when creating pods: %v", err)
		}
	}

	// ensure a few relists have occurred
	for i := 0; i < 3; i++ {
		<-listCh
	}

	var resources ClusterResources
	expectedAllocatable := newResourceList(100)
	expectedAvailable := newResourceList(30)
	expectedSchedulableNodes := int64(10)
	expectedResources := ClusterResources{
		Allocatable:      expectedAllocatable,
		Available:        expectedAvailable,
		SchedulableNodes: expectedSchedulableNodes,
	}

	resources, match, err := pollUntilResourceMatch(collector, expectedResources, 2*time.Second)
	if err != nil {
		t.Fatalf("Unexpected error when polling resources: %v", err)
	}
	if !match {
		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d schedulable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	}
}

type FakeNodeLister struct {
	clientset kubernetes.Interface
}

func (*FakeNodeLister) Get(name string) (*corev1.Node, error) {
	panic("unimplemented")
}

func (l *FakeNodeLister) List(selector labels.Selector) ([]*corev1.Node, error) {
	list, err := l.clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	ret := make([]*corev1.Node, len(list.Items))
	for i := range list.Items {
		ret[i] = &list.Items[i]
	}

	return ret, nil
}

var _ corev1listers.NodeLister = &FakeNodeLister{}
