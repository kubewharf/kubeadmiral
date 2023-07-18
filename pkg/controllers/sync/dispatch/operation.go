//go:build exclude
/*
Copyright 2019 The Kubernetes Authors.

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

package dispatch

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
)

type (
	clientAccessorFunc func(clusterName string) (generic.Client, error)
	targetAccessorFunc func(clusterName string) (*unstructured.Unstructured, error)
)

type dispatchRecorder interface {
	recordEvent(clusterName, operation, operationContinuous string)
	recordOperationError(ctx context.Context, status fedtypesv1a1.PropagationStatus, clusterName, operation string, err error) bool
}

// OperationDispatcher provides an interface to wait for operations
// dispatched to member clusters.
type OperationDispatcher interface {
	// Wait returns true for ok if all operations completed
	// successfully and false if only some operations completed
	// successfully.  An error is returned on timeout.
	Wait() (ok bool, timeoutErr error)
}

type operationDispatcherImpl struct {
	clientAccessor clientAccessorFunc

	resultChan          chan bool
	operationsInitiated atomic.Int32

	timeout time.Duration

	recorder dispatchRecorder
}

func newOperationDispatcher(clientAccessor clientAccessorFunc, recorder dispatchRecorder) *operationDispatcherImpl {
	return &operationDispatcherImpl{
		clientAccessor: clientAccessor,
		resultChan:     make(chan bool),
		timeout:        30 * time.Second, // TODO Make this configurable
		recorder:       recorder,
	}
}

func (d *operationDispatcherImpl) Wait() (bool, error) {
	ok := true
	timedOut := false
	start := time.Now()
	for i := int32(0); i < d.operationsInitiated.Load(); i++ {
		now := time.Now()
		if !now.Before(start.Add(d.timeout)) {
			timedOut = true
			break
		}
		select {
		case operationOK := <-d.resultChan:
			if !operationOK {
				ok = false
			}
			break
		case <-time.After(start.Add(d.timeout).Sub(now)):
			timedOut = true
			break
		}
	}
	if timedOut {
		return false, errors.Errorf("Failed to finish %d operations in %v", d.operationsInitiated.Load(), d.timeout)
	}
	return ok, nil
}

func (d *operationDispatcherImpl) clusterOperation(ctx context.Context, clusterName, op string, opFunc func(generic.Client) bool) {
	// TODO Support cancellation of client calls on timeout.
	client, err := d.clientAccessor(clusterName)
	logger := klog.FromContext(ctx)
	if err != nil {
		wrappedErr := errors.Wrapf(err, "Error retrieving client for cluster")
		if d.recorder == nil {
			logger.Error(wrappedErr, "Failed to retrieve client for cluster")
		} else {
			d.recorder.recordOperationError(ctx, fedtypesv1a1.ClientRetrievalFailed, clusterName, op, wrappedErr)
		}
		d.resultChan <- false
		return
	}

	// TODO Retry on recoverable errors (e.g. IsConflict, AlreadyExists)
	ok := opFunc(client)
	d.resultChan <- ok
}

func (d *operationDispatcherImpl) incrementOperationsInitiated() {
	d.operationsInitiated.Add(1)
}
