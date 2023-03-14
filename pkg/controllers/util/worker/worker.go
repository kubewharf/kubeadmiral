/*
Copyright 2018 The Kubernetes Authors.

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

package worker

import (
	"time"

	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/workqueue"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	deliverutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

type ReconcileFunc func(qualifiedName common.QualifiedName) Result

type ReconcileWorker interface {
	Enqueue(qualifiedName common.QualifiedName)
	EnqueueObject(obj pkgruntime.Object)
	EnqueueForBackoff(qualifiedName common.QualifiedName)
	EnqueueWithDelay(qualifiedName common.QualifiedName, delay time.Duration)
	Run(stopChan <-chan struct{})
}

type WorkerTiming struct {
	Interval       time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type asyncWorker struct {
	reconcile ReconcileFunc

	timing WorkerTiming

	// For triggering reconciliation of a single resource. This is
	// used when there is an add/update/delete operation on a resource
	// in either the API of the cluster hosting KubeFed or in the API
	// of a member cluster.
	deliverer *deliverutil.DelayingDeliverer

	// Work queue allowing parallel processing of resources
	queue workqueue.Interface

	// Backoff manager
	backoff *flowcontrol.Backoff

	workerCount int

	metrics    stats.Metrics
	metricTags deliverutil.MetricTags
}

func NewReconcileWorker(
	reconcile ReconcileFunc,
	timing WorkerTiming,
	workerCount int,
	metrics stats.Metrics,
	metricTags deliverutil.MetricTags,
) ReconcileWorker {
	if timing.Interval == 0 {
		timing.Interval = time.Second * 1
	}
	if timing.InitialBackoff == 0 {
		timing.InitialBackoff = time.Second * 5
	}
	if timing.MaxBackoff == 0 {
		timing.MaxBackoff = time.Minute
	}

	if workerCount == 0 {
		workerCount = 1
	}
	return &asyncWorker{
		reconcile:   reconcile,
		timing:      timing,
		deliverer:   deliverutil.NewDelayingDeliverer(),
		queue:       workqueue.New(),
		backoff:     flowcontrol.NewBackOff(timing.InitialBackoff, timing.MaxBackoff),
		workerCount: workerCount,
		metrics:     metrics,
		metricTags:  metricTags,
	}
}

func (w *asyncWorker) Enqueue(qualifiedName common.QualifiedName) {
	w.deliver(qualifiedName, 0, false)
}

func (w *asyncWorker) EnqueueObject(obj pkgruntime.Object) {
	qualifiedName := common.NewQualifiedName(obj)
	w.Enqueue(qualifiedName)
}

func (w *asyncWorker) EnqueueForBackoff(qualifiedName common.QualifiedName) {
	w.deliver(qualifiedName, 0, true)
}

func (w *asyncWorker) EnqueueWithDelay(qualifiedName common.QualifiedName, delay time.Duration) {
	w.deliver(qualifiedName, delay, false)
}

func (w *asyncWorker) Run(stopChan <-chan struct{}) {
	util.StartBackoffGC(w.backoff, stopChan)
	w.deliverer.StartWithHandler(func(item *deliverutil.DelayingDelivererItem) {
		w.queue.Add(item.Key)
	})
	go w.deliverer.RunMetricLoop(stopChan, 30*time.Second, w.metrics, w.metricTags)

	for i := 0; i < w.workerCount; i++ {
		go wait.Until(w.worker, w.timing.Interval, stopChan)
	}

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-stopChan
		w.queue.ShutDown()
		w.deliverer.Stop()
	}()
}

// deliver adds backoff to delay if backoff is true.  Otherwise, it
// resets backoff.
func (w *asyncWorker) deliver(qualifiedName common.QualifiedName, delay time.Duration, backoff bool) {
	key := qualifiedName.String()
	if backoff {
		w.backoff.Next(key, time.Now())
		delay = delay + w.backoff.Get(key)
	} else {
		w.backoff.Reset(key)
	}
	w.deliverer.DeliverAfter(key, &qualifiedName, delay)
}

func (w *asyncWorker) worker() {
	for {
		obj, quit := w.queue.Get()
		if quit {
			return
		}

		qualifiedName := common.NewQualifiedFromString(obj.(string))
		result := w.reconcile(qualifiedName)
		w.queue.Done(obj)

		if result.Backoff {
			w.EnqueueForBackoff(qualifiedName)
		} else if result.RequeueAfter != nil {
			w.EnqueueWithDelay(qualifiedName, *result.RequeueAfter)
		}
	}
}
