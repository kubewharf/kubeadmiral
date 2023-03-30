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
	"fmt"
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

func TestCollector(t *testing.T) {
	var collector ClusterCollector
	var clientset kubernetes.Interface

	var pods []runtime.Object
	var nodes []runtime.Object

	reset := func(numPods, numNodes int) {
		pods = make([]runtime.Object, numPods)
		for i := 1; i <= numPods; i++ {
			pods[i-1] = getPod(i)
		}
		nodes = make([]runtime.Object, numNodes)
		for i := 1; i <= numNodes; i++ {
			nodes[i-1] = getNode(i)
		}
		clientset = fake.NewSimpleClientset(append(pods, nodes...)...)

		collector = ClusterCollector{
			mu: sync.Mutex{},
			listWatcher: listwatcher.NewPagedListWatcher(
				"test",
				&cache.ListWatch{
					ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
						return clientset.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
					},
					WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
						w, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
						return w, err
					},
				},
				500,
			),
			receiver:         make(chan listwatcher.Event),
			nodeLister:       &FakeNodeLister{clientset: clientset},
			nodeListerSynced: func() bool { return true },
		}
	}

	t.Run("should handle initial list", func(t *testing.T) {
		ctx := context.TODO()

		reset(30, 10)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		resources, err := collector.GetClusterResources()
		if err != nil {
			t.Fatalf("Unexpected error when getting cluster resources: %v", err)
		}

		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100"),
			corev1.ResourceMemory: resource.MustParse("100Gi"),
			"custom-resource":     resource.MustParse("100"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("70"),
			corev1.ResourceMemory: resource.MustParse("70Gi"),
			"custom-resource":     resource.MustParse("70"),
		}
		expectedSchedulableNodes := int64(10)

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should handle initial list with completed pods", func(t *testing.T) {
		ctx := context.TODO()

		reset(30, 10)
		for i := 1; i <= 10; i++ {
			pod := pods[i-1].(*corev1.Pod)
			if i%2 == 0 {
				pod.Status.Phase = corev1.PodFailed
			} else {
				pod.Status.Phase = corev1.PodSucceeded
			}

			if _, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Update(
				context.Background(),
				pod,
				metav1.UpdateOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when updating pods: %v", err)
			}
		}

		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		resources, err := collector.GetClusterResources()
		if err != nil {
			t.Fatalf("Unexpected error when getting cluster resources: %v", err)
		}

		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100"),
			corev1.ResourceMemory: resource.MustParse("100Gi"),
			"custom-resource":     resource.MustParse("100"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("80"),
			corev1.ResourceMemory: resource.MustParse("80Gi"),
			"custom-resource":     resource.MustParse("80"),
		}
		expectedSchedulableNodes := int64(10)

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should handle pod creation", func(t *testing.T) {
		ctx := context.TODO()

		reset(30, 12)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		for i := 1; i <= 40; i++ {
			if _, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Create(
				context.Background(),
				getPod(30+i),
				metav1.CreateOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when creating pods: %v", err)
			}
		}

		var resources ClusterResources
		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("120"),
			corev1.ResourceMemory: resource.MustParse("120Gi"),
			"custom-resource":     resource.MustParse("120"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50"),
			corev1.ResourceMemory: resource.MustParse("50Gi"),
			"custom-resource":     resource.MustParse("50"),
		}
		expectedSchedulableNodes := int64(12)

		timeout, _ := context.WithTimeout(context.Background(), time.Second*2)
		wait.PollUntil(time.Millisecond, func() (done bool, err error) {
			resources, err = collector.GetClusterResources()
			if err != nil {
				t.Fatalf("Unexpected error when getting cluster resources: %v", err)
			}
			return equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) &&
				equality.Semantic.DeepEqual(expectedAvailable, resources.Available) &&
				expectedSchedulableNodes == resources.SchedulableNodes, nil
		}, timeout.Done())

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should handle pod completions", func(t *testing.T) {
		ctx := context.TODO()

		reset(80, 15)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		for i := 1; i <= 30; i++ {
			pod := pods[i-1].(*corev1.Pod)
			if i%2 == 0 {
				pod.Status.Phase = corev1.PodFailed
			} else {
				pod.Status.Phase = corev1.PodSucceeded
			}

			if _, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Update(
				context.Background(),
				pod,
				metav1.UpdateOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when updating pods: %v", err)
			}
		}

		var resources ClusterResources
		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("150"),
			corev1.ResourceMemory: resource.MustParse("150Gi"),
			"custom-resource":     resource.MustParse("150"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100"),
			corev1.ResourceMemory: resource.MustParse("100Gi"),
			"custom-resource":     resource.MustParse("100"),
		}
		expectedSchedulableNodes := int64(15)

		timeout, _ := context.WithTimeout(context.Background(), time.Second*2)
		wait.PollUntil(time.Millisecond, func() (done bool, err error) {
			resources, err = collector.GetClusterResources()
			if err != nil {
				t.Fatalf("Unexpected error when getting cluster resources: %v", err)
			}
			return equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) &&
				equality.Semantic.DeepEqual(expectedAvailable, resources.Available) &&
				expectedSchedulableNodes == resources.SchedulableNodes, nil
		}, timeout.Done())

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should ignore pod updates that are not pod completions", func(t *testing.T) {
		ctx := context.TODO()

		reset(80, 15)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		for i := 1; i <= 30; i++ {
			pod := pods[i-1].(*corev1.Pod)
			pod.Annotations = map[string]string{
				"test": "test",
			}

			if _, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Update(
				context.Background(),
				pod,
				metav1.UpdateOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when updating pods: %v", err)
			}
		}

		var resources ClusterResources
		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("150"),
			corev1.ResourceMemory: resource.MustParse("150Gi"),
			"custom-resource":     resource.MustParse("150"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("70"),
			corev1.ResourceMemory: resource.MustParse("70Gi"),
			"custom-resource":     resource.MustParse("70"),
		}
		expectedSchedulableNodes := int64(15)

		timeout, _ := context.WithTimeout(context.Background(), time.Second*2)
		wait.PollUntil(time.Millisecond, func() (done bool, err error) {
			resources, err = collector.GetClusterResources()
			if err != nil {
				t.Fatalf("Unexpected error when getting cluster resources: %v", err)
			}
			return equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) &&
				equality.Semantic.DeepEqual(expectedAvailable, resources.Available) &&
				expectedSchedulableNodes == resources.SchedulableNodes, nil
		}, timeout.Done())

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should handle pod deletion", func(t *testing.T) {
		ctx := context.TODO()

		reset(30, 12)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		for i := 1; i <= 10; i++ {
			pod := pods[i-1].(*corev1.Pod).Name
			if err := clientset.CoreV1().Pods(corev1.NamespaceAll).Delete(
				context.Background(),
				pod,
				metav1.DeleteOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when deleting pods: %v", err)
			}
		}

		var resources ClusterResources
		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("120"),
			corev1.ResourceMemory: resource.MustParse("120Gi"),
			"custom-resource":     resource.MustParse("120"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100"),
			corev1.ResourceMemory: resource.MustParse("100Gi"),
			"custom-resource":     resource.MustParse("100"),
		}
		expectedSchedulableNodes := int64(12)

		timeout, _ := context.WithTimeout(context.Background(), time.Second*2)
		wait.PollUntil(time.Millisecond, func() (done bool, err error) {
			resources, err = collector.GetClusterResources()
			if err != nil {
				t.Fatalf("Unexpected error when getting cluster resources: %v", err)
			}
			return equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) &&
				equality.Semantic.DeepEqual(expectedAvailable, resources.Available) &&
				expectedSchedulableNodes == resources.SchedulableNodes, nil
		}, timeout.Done())

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should ignore deletion of completed pods", func(t *testing.T) {
		ctx := context.TODO()

		reset(30, 12)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		for i := 1; i <= 10; i++ {
			pod := pods[i-1].(*corev1.Pod)
			if i%2 == 0 {
				pod.Status.Phase = corev1.PodFailed
			} else {
				pod.Status.Phase = corev1.PodSucceeded
			}

			if _, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Update(
				context.Background(),
				pod,
				metav1.UpdateOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when updating pods: %v", err)
			}
		}

		for i := 1; i <= 10; i++ {
			pod := pods[i-1].(*corev1.Pod).Name
			if err := clientset.CoreV1().Pods(corev1.NamespaceAll).Delete(
				context.Background(),
				pod,
				metav1.DeleteOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when deleting pods: %v", err)
			}
		}

		var resources ClusterResources
		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("120"),
			corev1.ResourceMemory: resource.MustParse("120Gi"),
			"custom-resource":     resource.MustParse("120"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100"),
			corev1.ResourceMemory: resource.MustParse("100Gi"),
			"custom-resource":     resource.MustParse("100"),
		}
		expectedSchedulableNodes := int64(12)

		timeout, _ := context.WithTimeout(context.Background(), time.Second*2)
		wait.PollUntil(time.Millisecond, func() (done bool, err error) {
			resources, err = collector.GetClusterResources()
			if err != nil {
				t.Fatalf("Unexpected error when getting cluster resources: %v", err)
			}
			return equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) &&
				equality.Semantic.DeepEqual(expectedAvailable, resources.Available) &&
				expectedSchedulableNodes == resources.SchedulableNodes, nil
		}, timeout.Done())

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})

	t.Run("should handle handle unschedulable nodes", func(t *testing.T) {
		ctx := context.TODO()

		reset(0, 10)
		collector.Start(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), collector.HasSynced) {
			t.Fatal("Failed to wait for cache sync")
		}

		for i := 1; i <= 5; i++ {
			node := nodes[i-1].(*corev1.Node)
			node.Spec.Unschedulable = true

			if _, err := clientset.CoreV1().Nodes().Update(
				context.Background(),
				node,
				metav1.UpdateOptions{},
			); err != nil {
				t.Fatalf("Unexpected error when updating nodes: %v", err)
			}
		}

		var resources ClusterResources
		expectedAllocatable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50"),
			corev1.ResourceMemory: resource.MustParse("50Gi"),
			"custom-resource":     resource.MustParse("50"),
		}
		expectedAvailable := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50"),
			corev1.ResourceMemory: resource.MustParse("50Gi"),
			"custom-resource":     resource.MustParse("50"),
		}
		expectedSchedulableNodes := int64(5)

		timeout, _ := context.WithTimeout(context.Background(), time.Second*2)
		wait.PollUntil(time.Millisecond, func() (done bool, err error) {
			resources, err = collector.GetClusterResources()
			if err != nil {
				t.Fatalf("Unexpected error when getting cluster resources: %v", err)
			}
			return equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) &&
				equality.Semantic.DeepEqual(expectedAvailable, resources.Available) &&
				expectedSchedulableNodes == resources.SchedulableNodes, nil
		}, timeout.Done())

		if !equality.Semantic.DeepEqual(expectedAllocatable, resources.Allocatable) {
			t.Errorf("Expected allocatable %+v, got %+v", expectedAllocatable, resources.Allocatable)
		}
		if !equality.Semantic.DeepEqual(expectedAvailable, resources.Available) {
			t.Errorf("Expected available %+v, got %+v", expectedAvailable, resources.Available)
		}
		if expectedSchedulableNodes != resources.SchedulableNodes {
			t.Errorf("Expected %d unschedlable nodes but got %d", expectedSchedulableNodes, resources.SchedulableNodes)
		}
	})
}

func getPod(i int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("pod-%d", i),
			UID:  uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
							"custom-resource":     resource.MustParse("1"),
						},
					},
				},
			},
		},
	}
}

func getNode(i int) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("node-%d", i),
		},
		Spec: corev1.NodeSpec{
			Unschedulable: false,
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("10Gi"),
				"custom-resource":     resource.MustParse("10"),
			},
		},
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
