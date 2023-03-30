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

package listwatcher

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestPagedListWatcher(t *testing.T) {
	var pods []runtime.Object
	var clientset kubernetes.Interface

	reset := func(numPods int) {
		pods = make([]runtime.Object, numPods)
		for i := 1; i <= numPods; i++ {
			pods[i-1] = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("pod-%d", i),
				},
			}
		}
		clientset = fake.NewSimpleClientset(pods...)
	}

	t.Run("should receive events in the correct order", func(t *testing.T) {
		reset(100)
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher("test", lw, 10)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())

		event := <-testCh
		if event.Type != StartSync {
			t.Errorf("Expected first event to be StartSync, but got %s", event.Type)
		}

		for i := 0; i < 100; i++ {
			event = <-testCh
			if event.Type != Add {
				t.Errorf("Expected event to be Add, but got %s", event.Type)
			}
			if event.Object == nil {
				t.Errorf("Expected Add event to contain object, but got nil")
			}
		}

		event = <-testCh
		if event.Type != EndSync {
			t.Errorf("Expected event to be EndSync, but got %s", event.Type)
		}
	})

	t.Run("should receive add, update and delete events", func(t *testing.T) {
		reset(100)
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher("test", lw, 10)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())

		for event := range testCh {
			if event.Type == EndSync {
				break
			}
		}

		var err error
		testPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-test",
			},
		}

		// Test add event
		if testPod, err = clientset.CoreV1().Pods(corev1.NamespaceAll).Create(
			context.TODO(),
			testPod,
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when creating pod: %v", err)
		}
		event := <-testCh
		if event.Type != Add {
			t.Errorf("Expected event to be Add, but got %s", event.Type)
		}
		obj, ok := event.Object.(*corev1.Pod)
		if !ok {
			t.Errorf("Expected event to contain pod, but got %v", reflect.TypeOf(obj))
		}
		if obj.Name != testPod.Name {
			t.Errorf("Expected event to contain pod %s, but got %s", testPod.Name, obj.Name)
		}

		// Test update event
		testPod.Annotations = map[string]string{
			"test": "test",
		}
		if testPod, err = clientset.CoreV1().Pods(corev1.NamespaceAll).Update(
			context.TODO(),
			testPod,
			metav1.UpdateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when updating pod: %v", err)
		}
		event = <-testCh
		if event.Type != Update {
			t.Errorf("Expected event to be Update, but got %s", event.Type)
		}
		obj, ok = event.Object.(*corev1.Pod)
		if !ok {
			t.Errorf("Expected event to contain pod, but got %v", reflect.TypeOf(obj))
		}
		if obj.Name != testPod.Name {
			t.Errorf("Expected event to contain pod %s, but got %s", testPod.Name, obj.Name)
		}
		if obj.Annotations == nil || obj.Annotations["test"] != "test" {
			t.Errorf("Expected event to contain latest pod %s, but got %v+", testPod.Name, obj)
		}

		// Test delete event
		if err := clientset.CoreV1().Pods(corev1.NamespaceAll).Delete(
			context.TODO(),
			testPod.Name,
			metav1.DeleteOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when deleting pod: %v", err)
		}
		event = <-testCh
		if event.Type != Delete {
			t.Errorf("Expected event to be Delete, but got %s", event.Type)
		}
		obj, ok = event.Object.(*corev1.Pod)
		if !ok {
			t.Errorf("Expected event to contain pod, but got %v", reflect.TypeOf(obj))
		}
		if obj.Name != testPod.Name {
			t.Errorf("Expected event to contain pod %s, but got %s", testPod.Name, obj.Name)
		}
	})

	t.Run("list should respect page size", func(t *testing.T) {
		numPods := 100
		pageSize := int64(10)
		numReqs := numPods / int(pageSize)

		reset(numPods)
		listCh := make(chan metav1.ListOptions, numReqs)

		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				// we implement our own paging mechanism as the fake client does not support it
				startIdx := 0
				if len(options.Continue) > 0 {
					var err error
					if startIdx, err = strconv.Atoi(options.Continue); err != nil {
						t.Fatalf("Unexpected error while listing: %v", err)
					}
				}

				endIdx := len(pods)
				if options.Limit > 0 {
					endIdx = startIdx + int(options.Limit)
				}

				items := []corev1.Pod{}
				for i := startIdx; i < endIdx; i++ {
					pod := pods[i].(*corev1.Pod)
					items = append(items, *pod)
				}

				result := &corev1.PodList{
					Items: items,
				}
				if endIdx < len(pods) {
					result.Continue = strconv.Itoa(endIdx)
				}

				listCh <- options
				return result, nil
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher("test", lw, pageSize)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())

		for event := range testCh {
			if event.Type == EndSync {
				break
			}
		}

		close(listCh)
		// At this stage, we should expect to see 10 list requests
		counter := 0
		for opt := range listCh {
			counter++
			if opt.Limit != pageSize {
				t.Errorf("Expected limit to be %d, got %d", pageSize, opt.Limit)
			}
		}
		if counter != numReqs {
			t.Errorf("Expected %d list requests, got %d", numReqs, counter)
		}
	})

	t.Run("should handle relists correctly", func(t *testing.T) {
		reset(0)
		failedWatches := 0
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				// Fail the watch 3 times
				if failedWatches < 3 {
					failedWatches++
					return nil, fmt.Errorf("watch failed")
				}

				return clientset.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher("test", lw, 10)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())

		// We should expect 3 relists since we configured the watch to fail 3 times
		for i := 0; i < 4; i++ {
			event := <-testCh
			if event.Type != StartSync {
				t.Errorf("Expected event to be ListStart, got %s", event.Type)
			}
			event = <-testCh
			if event.Type != EndSync {
				t.Errorf("Expected event to be ListSuccess, got %s", event.Type)
			}
		}

		if _, err := clientset.CoreV1().Pods(corev1.NamespaceAll).Create(
			context.TODO(),
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-test",
				},
			},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when creating pod: %v", err)
		}

		event := <-testCh
		if event.Type != Add {
			t.Errorf("Expected event to be Add, got %s", event.Type)
		}
	})

	t.Run("should handle failed lists correctly", func(t *testing.T) {
		reset(0)
		failedLists := 0
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				// Fail the list 3 times
				if failedLists < 3 {
					failedLists++
					return nil, fmt.Errorf("List failed")
				}

				return clientset.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientset.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher("test", lw, 10)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())

		// we should expect 3 relists
		for i := 0; i < 3; i++ {
			event := <-testCh
			if event.Type != StartSync {
				t.Errorf("Expected event to be StartSync, got %s", event.Type)
			}
		}

		event := <-testCh
		if event.Type != StartSync {
			t.Errorf("Expected event to be StartSync, got %s", event.Type)
		}
		event = <-testCh
		if event.Type != EndSync {
			t.Errorf("Expected event to be EndSync, got %s", event.Type)
		}
	})
}
