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
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func setupFakeClient(numPods int) (pods []runtime.Object, client kubernetes.Interface) {
	pods = make([]runtime.Object, numPods)
	for i := 1; i <= numPods; i++ {
		pods[i-1] = newPod(i)
	}
	return pods, fake.NewSimpleClientset(pods...)
}

func newPod(id int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-pod-%d", id),
		},
	}
}

func pagedListWatcherFromClient(client kubernetes.Interface, pageSize int64) ListWatcher {
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
		},
	}
	return NewPagedListWatcher(lw, 10)
}

func waitForFirstEndSync(eventCh chan Event) {
	for event := range eventCh {
		if event.Type == EndSync {
			break
		}
	}
}

func TestPagedListWatcher(t *testing.T) {
	t.Run("should handle events from the initial list correctly", func(t *testing.T) {
		_, client := setupFakeClient(100)
		listwatcher := pagedListWatcherFromClient(client, 10)

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

	t.Run("should handle create, update and delete events correctly", func(t *testing.T) {
		_, client := setupFakeClient(100)
		listwatcher := pagedListWatcherFromClient(client, 10)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())
		waitForFirstEndSync(testCh)

		var err error
		testPod := newPod(rand.Int() + 100)

		// test add event

		if testPod, err = client.CoreV1().Pods(corev1.NamespaceAll).Create(
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

		// test update event

		testPod.Annotations = map[string]string{"test": "test"}
		if testPod, err = client.CoreV1().Pods(corev1.NamespaceAll).Update(
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
			t.Errorf("Expected event to contain latest pod version, but got %v+", obj)
		}

		// test delete event

		if err := client.CoreV1().Pods(corev1.NamespaceAll).Delete(
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
		pods, client := setupFakeClient(100)
		pageSize := int64(10)
		expectedNumReqs := 10                                    // 100 / 10 = 10
		listCh := make(chan metav1.ListOptions, expectedNumReqs) // listCh is used to receive notifications of list requests

		// we implement our own paging mechanism as the fake client does not support it
		// this mechanism simply returns the next index to read as the continue token
		// this mechanism does not guarantee consistency if the client is used during a list
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
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

				// send a notification to listCh to record the number of lists
				listCh <- options

				return result, nil
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher(lw, pageSize)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())
		waitForFirstEndSync(testCh)

		// now that a full list is completed, we can close listCh to not receive anymore notifications
		close(listCh)

		counter := 0
		for opt := range listCh {
			counter++
			// ensure that request was sent with the correct page size
			if opt.Limit != pageSize {
				t.Errorf("Expected limit to be %d, got %d", pageSize, opt.Limit)
			}
		}
		// ensure that we recieved the correct number of list notifications
		if counter != expectedNumReqs {
			t.Errorf("Expected %d list requests, got %d", expectedNumReqs, counter)
		}
	})

	t.Run("should handle relists correctly", func(t *testing.T) {
		_, client := setupFakeClient(100)

		failedWatches := 0

		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				w, err := client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
				time.AfterFunc(time.Microsecond*5, func() {
					// close the watch 3 times
					if failedWatches < 3 {
						failedWatches++
						w.Stop()
					}

				})
				return w, err
			},
		}
		listwatcher := NewPagedListWatcher(lw, 10)

		testCh := make(chan Event)
		listwatcher.AddReceiver(testCh)
		listwatcher.Start(context.TODO())

		// we should expect 1 + 3 list requests since we configured the watch to fail 3 times
		for i := 0; i < 4; i++ {
			event := <-testCh
			if event.Type != StartSync {
				t.Errorf("Expected event to be ListStart, got %s", event.Type)
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
				t.Errorf("Expected event to be ListSuccess, got %s", event.Type)
			}
		}

		testPod := newPod(rand.Int() + 100)
		if _, err := client.CoreV1().Pods(corev1.NamespaceAll).Create(
			context.TODO(),
			testPod,
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("Unexpected error when creating pod: %v", err)
		}

		event := <-testCh
		if event.Type != Add {
			t.Errorf("Expected event to be Add, got %s", event.Type)
		}
		obj, ok := event.Object.(*corev1.Pod)
		if !ok {
			t.Errorf("Expected event to contain pod, but got %v", reflect.TypeOf(obj))
		}
		if obj.Name != testPod.Name {
			t.Errorf("Expected event to contain pod %s, but got %s", testPod.Name, obj.Name)
		}
	})

	t.Run("should handle failed lists correctly", func(t *testing.T) {
		_, client := setupFakeClient(100)

		failedLists := 0
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				// fail the list 3 times
				if failedLists < 3 {
					failedLists++
					return nil, fmt.Errorf("list failed")
				}

				return client.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		}
		listwatcher := NewPagedListWatcher(lw, 10)

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

		// the subsequent list should succeed
		event := <-testCh
		if event.Type != StartSync {
			t.Errorf("Expected event to be StartSync, got %s", event.Type)
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
			t.Errorf("Expected event to be EndSync, got %s", event.Type)
		}
	})
}
