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

package informermanager

import (
	"context"
	"time"

	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

func addPodInformer(ctx context.Context,
	informer informers.SharedInformerFactory,
	client kubeclient.Interface,
	podListerSemaphore *semaphore.Weighted,
	enablePodPruning bool,
) {
	informer.InformerFor(
		&corev1.Pod{},
		func(k kubeclient.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
			return cache.NewSharedIndexInformer(
				podListerWatcher(ctx, client, podListerSemaphore, enablePodPruning),
				&corev1.Pod{},
				resyncPeriod,
				cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			)
		},
	)
}

func podListerWatcher(
	ctx context.Context,
	client kubeclient.Interface,
	semaphore *semaphore.Weighted,
	enablePodPruning bool,
) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			if semaphore != nil {
				if err := semaphore.Acquire(ctx, 1); err != nil {
					return nil, err
				}
				defer semaphore.Release(1)
			}
			pods, err := client.CoreV1().Pods(corev1.NamespaceAll).List(ctx, options)
			if err != nil {
				return nil, err
			}
			if enablePodPruning {
				for i := range pods.Items {
					PrunePod(&pods.Items[i])
				}
			}
			return pods, nil
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			watcher, err := client.CoreV1().Pods(corev1.NamespaceAll).Watch(ctx, options)
			if err != nil {
				return nil, err
			}
			if !enablePodPruning {
				return watcher, nil
			}

			// It's easy for a consumer to add buffering via an extra
			// goroutine/channel, but impossible for them to remove it,
			// so nonbuffered is better. -- from watch.NewStreamWatcher
			proxyCh := make(chan watch.Event)
			proxyWatcher := watch.NewProxyWatcher(proxyCh)
			go func() {
				defer watcher.Stop()
				// Closing proxyCh will notify the reflector to stop the current
				// watching cycle and then restart the list and watch.
				defer close(proxyCh)
				for {
					select {
					case <-proxyWatcher.StopChan():
						return
					case event, ok := <-watcher.ResultChan():
						if !ok {
							// the watcher has been closed, stop the proxy
							return
						}
						if pod, ok := event.Object.(*corev1.Pod); ok {
							PrunePod(pod)
						}
						proxyCh <- event
					}
				}
			}()
			return proxyWatcher, nil
		},
	}
}

func PrunePod(pod *corev1.Pod) {
	containers := make([]corev1.Container, len(pod.Spec.Containers))
	initContainers := make([]corev1.Container, len(pod.Spec.InitContainers))
	for i := range pod.Spec.Containers {
		containers[i] = corev1.Container{Resources: pod.Spec.Containers[i].Resources}
	}
	for i := range pod.Spec.InitContainers {
		initContainers[i] = corev1.Container{Resources: pod.Spec.InitContainers[i].Resources}
	}
	*pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              pod.Name,
			Namespace:         pod.Namespace,
			Generation:        pod.Generation,
			ResourceVersion:   pod.ResourceVersion,
			UID:               pod.UID,
			DeletionTimestamp: pod.DeletionTimestamp,
			Labels:            pod.Labels,
			CreationTimestamp: pod.CreationTimestamp,
		},
		Spec: corev1.PodSpec{
			NodeName:           pod.Spec.NodeName,
			Overhead:           pod.Spec.Overhead,
			Containers:         containers,
			InitContainers:     initContainers,
			RestartPolicy:      pod.Spec.RestartPolicy,
			SchedulerName:      pod.Spec.SchedulerName,
			ServiceAccountName: pod.Spec.ServiceAccountName,
			HostNetwork:        pod.Spec.HostNetwork,
		},
		Status: corev1.PodStatus{
			Phase:             pod.Status.Phase,
			Conditions:        pod.Status.Conditions,
			PodIP:             pod.Status.PodIP,
			NominatedNodeName: pod.Status.NominatedNodeName,
		},
	}
}
