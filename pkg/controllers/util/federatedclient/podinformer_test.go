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

package federatedclient

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	kubeinformer "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	fakecorev1 "k8s.io/client-go/kubernetes/typed/core/v1/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

type fakePodsWithConcurrencyLimit struct {
	corev1client.PodInterface
	concurrentLists *atomic.Int64
}

func (c *fakePodsWithConcurrencyLimit) List(ctx context.Context, opts metav1.ListOptions) (result *corev1.PodList, err error) {
	cur := c.concurrentLists.Add(-1)
	defer c.concurrentLists.Add(1)
	if cur < 0 {
		panic("limit exceeded")
	}
	return c.PodInterface.List(ctx, opts)
}

type fakeCoreV1WithConcurrencyLimit struct {
	fakecorev1.FakeCoreV1
	concurrentLists *atomic.Int64
}

func (c *fakeCoreV1WithConcurrencyLimit) Pods(namespace string) corev1client.PodInterface {
	return &fakePodsWithConcurrencyLimit{c.FakeCoreV1.Pods(namespace), c.concurrentLists}
}

var _ kubeclient.Interface = &fakeClientsetWithConcurrencyLimit{}

type fakeClientsetWithConcurrencyLimit struct {
	*fake.Clientset
	concurrentLists *atomic.Int64
}

func (c *fakeClientsetWithConcurrencyLimit) CoreV1() corev1client.CoreV1Interface {
	return &fakeCoreV1WithConcurrencyLimit{fakecorev1.FakeCoreV1{Fake: &c.Clientset.Fake}, c.concurrentLists}
}

func newFakeClientset(concurrentLists int64, objects ...runtime.Object) *fakeClientsetWithConcurrencyLimit {
	client := fake.NewSimpleClientset(objects...)
	var limit atomic.Int64
	limit.Add(concurrentLists)
	return &fakeClientsetWithConcurrencyLimit{
		Clientset:       client,
		concurrentLists: &limit,
	}
}

func setupFakeClient(concurrentLists int64, numPods int) (pods []runtime.Object, client kubeclient.Interface) {
	pods = make([]runtime.Object, numPods)
	for i := 0; i < numPods; i++ {
		ns := fmt.Sprintf("test-ns-%d", i%10)
		name := fmt.Sprintf("test-name-%d", i)
		pods[i] = newPod(ns, name)
	}
	return pods, newFakeClientset(concurrentLists, pods...)
}

func newPod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Labels:    map[string]string{"foo": "bar"},
			Namespace: ns,
		},
		Spec: corev1.PodSpec{
			Overhead: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1m"),
			},
			InitContainers: []corev1.Container{
				{
					Name: "initcontainer",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1m"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1m"),
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name: "container",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("10m"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("10m"),
						},
					},
				},
			},
		},
	}
}

func Test_podInformer(t *testing.T) {
	tests := []struct {
		name                string
		clusters            int
		podNum              int
		availablePodListers int64
		enablePodPruning    bool
	}{
		{
			name:                "4 clusters, prune pod, limit 2",
			clusters:            4,
			podNum:              3000,
			availablePodListers: 2,
			enablePodPruning:    true,
		},
		{
			name:                "4 clusters, prune pod, no limit",
			clusters:            4,
			podNum:              3000,
			availablePodListers: 0,
			enablePodPruning:    true,
		},
		{
			name:                "4 clusters, regular pod, limit 2",
			clusters:            4,
			podNum:              3000,
			availablePodListers: 2,
			enablePodPruning:    false,
		},
		{
			name:                "4 clusters, regular pod, no limit",
			clusters:            4,
			podNum:              3000,
			availablePodListers: 0,
			enablePodPruning:    false,
		},
	}

	type memberCluster struct {
		name          string
		client        kubeclient.Interface
		informer      kubeinformer.SharedInformerFactory
		enablePruning bool
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sem *semaphore.Weighted
			if tt.availablePodListers > 0 {
				sem = semaphore.NewWeighted(tt.availablePodListers)
			}
			ctx := context.Background()
			clusters := make([]memberCluster, tt.clusters)
			for i := 0; i < tt.clusters; i++ {
				var client kubeclient.Interface
				if tt.availablePodListers == 0 {
					_, client = setupFakeClient(int64(tt.clusters), tt.podNum)
				} else {
					_, client = setupFakeClient(tt.availablePodListers, tt.podNum)
				}
				informer := kubeinformer.NewSharedInformerFactory(client, util.NoResyncPeriod)
				clusters[i] = memberCluster{
					name:          fmt.Sprintf("member-cluster-%d", i),
					client:        client,
					informer:      informer,
					enablePruning: tt.enablePodPruning,
				}
			}

			for i := range clusters {
				addPodInformer(ctx, clusters[i].informer, clusters[i].client, sem, clusters[i].enablePruning)
				clusters[i].informer.Start(ctx.Done())
			}
			var wg sync.WaitGroup
			wg.Add(tt.clusters)
			for i := range clusters {
				go func(i int) {
					defer wg.Done()

					podLister := clusters[i].informer.Core().V1().Pods().Lister()
					podsSynced := clusters[i].informer.Core().V1().Pods().Informer().HasSynced
					ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()

					// test lister
					if !cache.WaitForNamedCacheSync(clusters[i].name, ctx.Done(), podsSynced) {
						t.Errorf("%s should be synced, but it was not\n", clusters[i].name)
						return
					}
					pods, err := podLister.List(labels.Everything())
					if err != nil {
						t.Errorf("%s: unexpected error when listing pod: %v\n", clusters[i].name, err)
						return
					}
					if len(pods) != tt.podNum {
						t.Errorf("%s: Expected %d pods, got %d pods", clusters[i].name, tt.podNum, len(pods))
						return
					}

					// test watcher
					watchPodName := fmt.Sprintf("watch-pod-%d", rand.Intn(tt.podNum))
					watchPodNamespace := "watch"
					watchPod := newPod(watchPodNamespace, watchPodName)
					handlerFn := func(obj interface{}, ch chan struct{}) error {
						pod, ok := obj.(*corev1.Pod)
						if !ok {
							return fmt.Errorf("expected to handle pod, but got: %v", obj)
						}
						if pod.Name == watchPodName {
							close(ch)
						}
						if clusters[i].enablePruning && pod.Labels != nil {
							return fmt.Errorf("expect no label for pod, but got labels: %v", pod.Labels)
						}
						if !clusters[i].enablePruning && pod.Labels == nil {
							return fmt.Errorf("expected to get lables from pod, but got nil")
						}
						return nil
					}
					addCh, updateCh, deleteCh := make(chan struct{}), make(chan struct{}), make(chan struct{})
					clusters[i].informer.Core().V1().Pods().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
						AddFunc: func(obj interface{}) {
							if err := handlerFn(obj, addCh); err != nil {
								t.Errorf("unexpected err: %v", err)
							}
						},
						UpdateFunc: func(oldObj, newObj interface{}) {
							if err := handlerFn(newObj, updateCh); err != nil {
								t.Errorf("unexpected err: %v", err)
							}
						},
						DeleteFunc: func(obj interface{}) {
							if err := handlerFn(obj, deleteCh); err != nil {
								t.Errorf("unexpected err: %v", err)
							}
						},
					})

					if watchPod, err = clusters[i].client.CoreV1().Pods(watchPodNamespace).Create(
						ctx, watchPod, metav1.CreateOptions{}); err != nil {
						t.Errorf("unexpected error when creating pod: %v", err)
						return
					}
					select {
					case <-addCh:
					case <-time.After(time.Second):
						t.Errorf("timeout for adding pod")
						return
					}

					watchPod.Labels = map[string]string{watchPodName: fmt.Sprintf("foo-%d", rand.Intn(tt.podNum))}
					if watchPod, err = clusters[i].client.CoreV1().Pods(watchPodNamespace).Update(
						ctx, watchPod, metav1.UpdateOptions{}); err != nil {
						t.Errorf("unexpected error when updating pod: %v", err)
						return
					}
					if !clusters[i].enablePruning {
						select {
						case <-updateCh:
						case <-time.After(time.Second):
							t.Errorf("timeout for updating pod")
							return
						}
					}

					if err = clusters[i].client.CoreV1().Pods(watchPodNamespace).Delete(
						ctx, watchPod.Name, metav1.DeleteOptions{}); err != nil {
						t.Errorf("unexpected error when deleting pod: %v", err)
						return
					}
					select {
					case <-deleteCh:
					case <-time.After(time.Second):
						t.Errorf("timeout for deleting pod")
						return
					}
				}(i)
			}
			wg.Wait()
		})
	}
}
