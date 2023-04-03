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
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/listwatcher"
)

// ClusterCollector collects the allocated and available resources in a cluster by processing the node and pod objects
// in a cluster. ClusterCollector does this in a way that does not require pod objects to be cached, keeping memory
// usage low. Correctness is based on the premise that a pod's resource requirement canot be modified after it is
// created. This means that we only need to add the resource requirements on pod creation, and subtract the resource
// requirements on pod completion/deletion.
type ClusterCollector struct {
	mu          sync.Mutex
	listWatcher listwatcher.ListWatcher
	receiver    chan listwatcher.Event
	cancel      context.CancelFunc

	nodeLister       corev1listers.NodeLister
	nodeListerSynced cache.InformerSynced

	started   bool
	hasSynced bool

	requested     corev1.ResourceList
	completedPods sets.Set[string]
}

func NewClusterCollector(client kubernetes.Interface, nodeInformer corev1informers.NodeInformer) *ClusterCollector {
	listWatcher := listwatcher.NewPagedListWatcher(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().Pods(corev1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return client.CoreV1().Pods(corev1.NamespaceAll).Watch(context.TODO(), options)
			},
		},
		500,
	)

	return &ClusterCollector{
		mu:               sync.Mutex{},
		listWatcher:      listWatcher,
		receiver:         make(chan listwatcher.Event, 100),
		nodeLister:       nodeInformer.Lister(),
		nodeListerSynced: nodeInformer.Informer().HasSynced,
	}
}

type ClusterResources struct {
	Available        corev1.ResourceList
	Allocatable      corev1.ResourceList
	SchedulableNodes int64
}

func (c *ClusterCollector) GetClusterResources() (ClusterResources, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	nodeList, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return ClusterResources{}, fmt.Errorf("failed to list cluster nodes: %w", err)
	}

	schedulableNodes := int64(0)
	allocatable := make(corev1.ResourceList)

	for _, node := range nodeList {
		if isNodeSchedulable(node) {
			schedulableNodes++
			addResources(allocatable, node.Status.Allocatable)
		}
	}

	available := allocatable.DeepCopy()
	subResources(available, c.requested)

	return ClusterResources{
		Available:        available,
		Allocatable:      allocatable,
		SchedulableNodes: schedulableNodes,
	}, nil
}

func (c *ClusterCollector) HasSynced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasSynced
}

func (c *ClusterCollector) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return
	}

	if !cache.WaitForCacheSync(ctx.Done(), c.nodeListerSynced) {
		return
	}

	ctx, c.cancel = context.WithCancel(ctx)

	c.listWatcher.AddReceiver(c.receiver)
	c.listWatcher.Start(ctx)

	go c.startProcessing(ctx)
	c.started = true
}

func (c *ClusterCollector) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return
	}

	c.cancel()
}

func (c *ClusterCollector) startProcessing(ctx context.Context) {
	syncing := false
	var requested corev1.ResourceList

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-c.receiver:
			switch event.Type {
			case listwatcher.StartSync:
				syncing = true
				requested = corev1.ResourceList{}
				c.mu.Lock()
				c.completedPods = sets.New[string]()
				c.mu.Unlock()

			case listwatcher.EndSync:
				syncing = false
				c.mu.Lock()
				c.hasSynced = true
				c.requested = requested
				c.mu.Unlock()

			case listwatcher.Add:
				pod := event.Object.(*corev1.Pod)
				if syncing {
					if isPodCompleted(pod) {
						// when syncing, we may get existing pods that have already completed
						c.completedPods.Insert(string(pod.GetUID()))
					} else {
						podRequest := getPodResourceRequests(pod)
						addResources(requested, podRequest)
					}
				} else {
					// otherwise, new pods cannot be completed
					c.mu.Lock()
					podRequest := getPodResourceRequests(pod)
					addResources(c.requested, podRequest)
					c.mu.Unlock()
				}

			case listwatcher.Update:
				pod := event.Object.(*corev1.Pod)
				c.mu.Lock()
				if isPodCompleted(pod) && !c.completedPods.Has(string(pod.GetUID())) {
					// account for newly completed pods
					podRequest := getPodResourceRequests(pod)
					subResources(c.requested, podRequest)
					c.completedPods.Insert(string(pod.GetUID()))
				}
				c.mu.Unlock()

			case listwatcher.Delete:
				pod := event.Object.(*corev1.Pod)
				c.mu.Lock()
				if !c.completedPods.Has(string(pod.GetUID())) {
					// only subtract a deleted pod if we have not previously accounted for its completion
					podRequest := getPodResourceRequests(pod)
					subResources(c.requested, podRequest)
				} else {
					c.completedPods.Delete(string(pod.GetUID()))
				}
				c.mu.Unlock()

			default:
				continue
			}
		}
	}
}

func isPodCompleted(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func getPodResourceRequests(pod *corev1.Pod) corev1.ResourceList {
	reqs := make(corev1.ResourceList)

	for _, container := range pod.Spec.Containers {
		addResources(reqs, container.Resources.Requests)
	}

	for _, container := range pod.Spec.InitContainers {
		maxResources(reqs, container.Resources.Requests)
	}

	// if PodOverhead feature is supported, add overhead for running a pod
	// to the sum of requests and to non-zero limits:
	if pod.Spec.Overhead != nil {
		addResources(reqs, pod.Spec.Overhead)
	}

	return reqs
}

// maxResources sets dst to the greater of dst/src for every resource in src
func maxResources(src, dst corev1.ResourceList) {
	for name, srcQuantity := range src {
		if dstQuantity, ok := dst[name]; !ok || srcQuantity.Cmp(dstQuantity) > 0 {
			dst[name] = srcQuantity.DeepCopy()
		}
	}
}

func addResources(base, toAdd corev1.ResourceList) {
	for k, v := range toAdd {
		if prevVal, ok := base[k]; ok {
			prevVal.Add(v)
			base[k] = prevVal
		} else {
			base[k] = v.DeepCopy()
		}
	}
}

func subResources(base, diff corev1.ResourceList) {
	for k, v := range diff {
		if prevVal, ok := base[k]; ok {
			prevVal.Sub(v)
			base[k] = prevVal
		}
	}
}

func isNodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}

	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule ||
			taint.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}

	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
			return false
		}
	}
	return true
}
