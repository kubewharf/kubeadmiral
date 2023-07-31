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
	"context"
	"math"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"

	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

type ReconcileFunc[Key any] func(context.Context, Key) Result

type ReconcileWorker[Key any] interface {
	Enqueue(key Key)
	EnqueueWithBackoff(key Key)
	EnqueueWithDelay(key Key, delay time.Duration)
	Run(ctx context.Context)
}

type RateLimiterOptions struct {
	// The initial delay for a failed item.
	InitialDelay time.Duration
	// The maximum delay for a failed item.
	MaxDelay time.Duration
	// The overall reconcile qps.
	OverallQPS float64
	// The overall reconcile burst.
	OverallBurst int
}

type asyncWorker[Key any] struct {
	// Name of this reconcile worker.
	name string

	// Work queue holding keys to be processed.
	queue workqueue.RateLimitingInterface

	// Function called to reconcile keys popped from the queue.
	reconcile ReconcileFunc[Key]

	// Number of parallel workers to reconcile keys popped from the queue.
	workerCount int

	// Metrics implementation.
	// TODO: export workqueue metrics by providing a MetricsProvider implementation.
	metrics stats.Metrics
}

func NewReconcileWorker[Key any](
	name string,
	reconcile ReconcileFunc[Key],
	timing RateLimiterOptions,
	workerCount int,
	metrics stats.Metrics,
) ReconcileWorker[Key] {
	if timing.InitialDelay <= 0 {
		timing.InitialDelay = 5 * time.Second
	}
	if timing.MaxDelay <= 0 {
		timing.MaxDelay = time.Minute
	}
	if timing.OverallQPS <= 0 {
		timing.OverallQPS = float64(rate.Inf)
	}
	if timing.OverallBurst <= 0 {
		timing.OverallBurst = math.MaxInt
	}

	rateLimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(timing.InitialDelay, timing.MaxDelay),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(timing.OverallQPS), timing.OverallBurst)},
	)
	queue := workqueue.NewNamedRateLimitingQueue(rateLimiter, name)

	if workerCount <= 0 {
		workerCount = 1
	}

	return &asyncWorker[Key]{
		name:        name,
		reconcile:   reconcile,
		queue:       queue,
		workerCount: workerCount,
		metrics:     metrics,
	}
}

func (w *asyncWorker[Key]) Enqueue(key Key) {
	w.queue.Add(key)
}

func (w *asyncWorker[Key]) EnqueueWithBackoff(key Key) {
	w.queue.AddRateLimited(key)
}

func (w *asyncWorker[Key]) EnqueueWithDelay(key Key, delay time.Duration) {
	w.queue.AddAfter(key, delay)
}

func (w *asyncWorker[Key]) Run(ctx context.Context) {
	for i := 0; i < w.workerCount; i++ {
		go w.worker(ctx)
	}

	// Ensure all goroutines are cleaned up when the stop channel closes
	go func() {
		<-ctx.Done()
		w.queue.ShutDown()
	}()
}

func (w *asyncWorker[Key]) processNextItem(ctx context.Context) bool {
	keyAny, quit := w.queue.Get()
	if quit {
		return false
	}

	key := keyAny.(Key)
	result := w.reconcile(ctx, key)
	w.queue.Done(keyAny)

	if result.Backoff {
		w.EnqueueWithBackoff(key)
	} else {
		w.queue.Forget(keyAny)

		if result.RequeueAfter != nil {
			w.EnqueueWithDelay(key, *result.RequeueAfter)
		}
	}

	return true
}

func (w *asyncWorker[Key]) worker(ctx context.Context) {
	for w.processNextItem(ctx) {
	}
}
