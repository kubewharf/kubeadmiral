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
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// ListWatcher lists and watches a given resource, emitting events to registered receivers. This allows receivers to
// track changes to a resource starting from a consistent snapshot.
//
// Before any list request, ListWatcher must emit a StartSync event to notify receivers. This allows receivers to start
// performing a resync if required. After all the objects from a succesful list request is emitted, ListWatcher must
// also emit a EndSync event to notify receivers.
//
// The design of ListWatcher and its implementations is loosely based on client-go's reflector.
type ListWatcher interface {
	Start(ctx context.Context)
	AddReceiver(chan<- Event)
}

type Event struct {
	Type   EventType
	Object runtime.Object
}

type EventType string

const (
	// StartSync is emmited when a ListWatcher starts a new list. Receivers should use this as a signal to start a
	// resync should they require a consistent view of the resource.
	StartSync EventType = "StartSync"
	// EndSync is emmited when a ListWatcher successfully completes a list. Receivers can use all the Add events
	// received since the last StartSync to construct a consistent snapshot of the resource.
	EndSync EventType = "EndSync"

	// Add is emmited for each object in a list request, and any subsequent Added watch events.
	Add EventType = "Add"
	// Update is emmited for any Modified watch events.
	Update EventType = "Update"
	// Delete is emmited for any Deleted watch events.
	Delete EventType = "Delete"
)

type pagedListWatcher struct {
	lw        cache.ListerWatcher
	mux       chan Event
	receivers []chan<- Event
	pageSize  int64

	mu sync.Mutex
}

// NewPagedListWatcher returns a ListWatcher implementation that uses paging when listing from the apiserver. Each page
// is processed immediately to allow the allocated memory to be potentially garbage collected before the next request.
func NewPagedListWatcher(lw cache.ListerWatcher, pageSize int64) ListWatcher {
	return &pagedListWatcher{
		lw:        lw,
		mux:       make(chan Event, 100),
		receivers: []chan<- Event{},
		pageSize:  pageSize,
		mu:        sync.Mutex{},
	}
}

func (p *pagedListWatcher) AddReceiver(receiver chan<- Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.receivers = append(p.receivers, receiver)
}

func (p *pagedListWatcher) Start(ctx context.Context) {
	logger := klog.FromContext(ctx).WithValues("component", "paged-list-watcher")
	listCh := make(chan string, 1)

	go func() {
		for {
			// list resource
			select {
			case <-ctx.Done():
				return
			default:
				p.mux <- Event{Type: StartSync}

				resourceVersion, err := p.list(ctx)
				if err != nil {
					logger.Error(err, "Failed to list resource, will retry")
					continue
				}

				p.mux <- Event{Type: EndSync}
				listCh <- resourceVersion
			}

			// watch resource
			select {
			case <-ctx.Done():
				return
			case resourceVersion := <-listCh:
				if err := p.watch(ctx, resourceVersion); err != nil {
					logger.Error(err, "Watch resource ended with error, relist will occur")
				} else {
					logger.V(4).Info("Watch resource ended gracefully, relist will occur")
				}
			}
		}
	}()

	go func() {
		for event := range p.mux {
			p.mu.Lock()
			for _, receiver := range p.receivers {
				receiver <- event
			}
			p.mu.Unlock()
		}
	}()
}

func (p *pagedListWatcher) list(ctx context.Context) (rv string, err error) {
	resourceVersion := ""
	listOpts := metav1.ListOptions{
		Limit:    p.pageSize,
		Continue: "",
	}

	for {
		select {
		case <-ctx.Done():
			return "", nil
		default:
			resp, err := p.lw.List(listOpts)
			if err != nil {
				return "", fmt.Errorf("failed to list resource: %w", err)
			}

			list, err := meta.ListAccessor(resp)
			if err != nil {
				return "", fmt.Errorf("failed to list resource: %w", err)
			}

			listOpts.Continue = list.GetContinue()
			resourceVersion = list.GetResourceVersion()

			err = meta.EachListItem(resp, func(o runtime.Object) error {
				p.mux <- Event{
					Type:   Add,
					Object: o.DeepCopyObject(),
				}
				return nil
			})

			if err != nil {
				return "", fmt.Errorf("failed to process list item: %w", err)
			}
		}

		if len(listOpts.Continue) == 0 {
			break
		}
	}

	return resourceVersion, nil
}

func (p *pagedListWatcher) watch(ctx context.Context, resourceVersion string) error {
	watcher, err := p.lw.Watch(metav1.ListOptions{ResourceVersion: resourceVersion})
	if err != nil {
		return fmt.Errorf("failed to watch resource: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return nil
			}

			switch event.Type {
			case watch.Added:
				p.mux <- Event{
					Type:   Add,
					Object: event.Object,
				}
			case watch.Modified:
				p.mux <- Event{
					Type:   Update,
					Object: event.Object,
				}
			case watch.Deleted:
				p.mux <- Event{
					Type:   Delete,
					Object: event.Object,
				}
			case watch.Error:
				return fmt.Errorf("watch channel closed unexpectedly: %v", event.Object)
			default:
				continue
			}
		}
	}
}
