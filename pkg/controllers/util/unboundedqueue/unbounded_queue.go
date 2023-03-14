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

package unboundedqueue

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/runtime"
)

// UnboundedQueue is an unbounded channel.
type UnboundedQueue struct {
	deque    *deque
	notifier chan<- struct{}
	receiver <-chan interface{}
	stopCh   chan<- struct{}
}

// Creates a new UnboundedQueue with the specified initial capacity.
func New(initialCapacity int) *UnboundedQueue {
	deque := newDeque(initialCapacity)
	notifier := make(chan struct{}, 1)
	receiver := make(chan interface{})
	stopCh := make(chan struct{}, 1)

	go receiverLoop(deque, notifier, receiver, stopCh)

	return &UnboundedQueue{
		deque:    deque,
		notifier: notifier,
		receiver: receiver,
		stopCh:   stopCh,
	}
}

// Receiver returns the channel that can be used for receiving from this UnboundedQueue.
func (uq *UnboundedQueue) Receiver() <-chan interface{} {
	return uq.receiver
}

type MetricData struct {
	// Length is the current length of the queue.
	Length int
	// MaxLength is the maximum length of the queue since the last sample.
	MaxLength int
	// Capacity is the current capacity of the queue.
	Capacity int
}

func (uq *UnboundedQueue) RunMetricLoop(stopCh <-chan struct{}, interval time.Duration, sender func(MetricData)) {
	defer runtime.HandleCrash()

	for {
		select {
		case <-stopCh:
			return
		case <-time.After(interval):
			metricData := uq.deque.getMetricData()
			sender(metricData)
		}
	}
}

func (uq *UnboundedQueue) Close() {
	uq.stopCh <- struct{}{}
}

// Sends an item to the queue.
//
// Since the channel capacity is unbounded, send operations always succeed and never block.
func (uq *UnboundedQueue) Send(obj interface{}) {
	uq.deque.pushBack(obj)

	select {
	case uq.notifier <- struct{}{}:
		// notified the receiver goroutine
	default:
		// no goroutine is receiving, but notifier is already nonempty anyway
	}
}

func receiverLoop(deque *deque, notifier <-chan struct{}, receiver chan<- interface{}, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()

	for {
		item := deque.popFront()

		if item != nil {
			select {
			case <-stopCh:
				// queue stopped, close the receiver
				close(receiver)
				return
			case receiver <- item:
				// since notifier only has one buffer signal, there may be multiple actual items.
				// therefore, we call `popFront` again without waiting for the notifier.
				continue
			}
		}

		// item is nil, we have to wait
		// since there is one buffer signal in notifier,
		// even if a new item is sent on this line,
		// notifier will still receive something.

		select {
		case <-stopCh:
			// queue stopped, close the receiver
			close(receiver)
			return
		case <-notifier:
			// deque has been updated
		}
	}
}

// deque is a typical double-ended queue implemented through a ring buffer.
//
// All operations on deque locks on its own mutex, so all operations are concurrency-safe.
type deque struct {
	data      []interface{}
	start     int
	end       int
	maxLength int
	lock      sync.Mutex
}

// newDeque constructs a new deque with the specified initial capacity.
//
// deque capacity is doubled when the length reaches the capacity,
// i.e. a deque can never be full.
func newDeque(initialCapacity int) *deque {
	return &deque{
		data:  make([]interface{}, initialCapacity, initialCapacity),
		start: 0,
		end:   0,
	}
}

// pushBack pushes an object to the end of the queue.
//
// This method has amortized O(1) time complexity and expands the capacity on demand.
func (q *deque) pushBack(obj interface{}) {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.data[q.end] = obj
	q.end = (q.end + 1) % len(q.data)

	if q.end == q.start {
		// when deque is unlocked, q.end == q.start implies empty deque.
		// therefore, we need to expand it now.

		newData := make([]interface{}, len(q.data)*2, len(q.data)*2)

		for i := q.start; i < len(q.data); i++ {
			newData[i-q.start] = q.data[i]
		}

		leftOffset := len(q.data) - q.start
		for i := 0; i < q.end; i++ {
			newData[leftOffset+i] = q.data[i]
		}

		q.start = 0
		q.end = len(q.data)

		q.data = newData
	}

	length := q.lockedGetLength()
	if q.maxLength < length {
		q.maxLength = length
	}
}

// popFront pops an object from the start of the queue.
//
// This method has O(1) time complexity.
func (q *deque) popFront() interface{} {
	q.lock.Lock()
	defer q.lock.Unlock()

	if q.start == q.end {
		// we assume the deque is in sound state,
		// i.e. q.start == q.end implies empty queue.
		return nil
	}

	ret := q.data[q.start]

	// we need to unset this pointer to allow GC
	q.data[q.start] = nil

	q.start = (q.start + 1) % len(q.data)

	return ret
}

func (q *deque) getMetricData() MetricData {
	q.lock.Lock()
	defer q.lock.Unlock()

	length := q.lockedGetLength()

	maxLength := q.maxLength
	q.maxLength = 0

	capacity := len(q.data)

	return MetricData{
		Length:    length,
		MaxLength: maxLength,
		Capacity:  capacity,
	}
}

func (q *deque) lockedGetLength() int {
	if q.start <= q.end {
		return q.end - q.start
	}

	front := len(q.data) - q.start
	back := q.end
	return front + back
}
