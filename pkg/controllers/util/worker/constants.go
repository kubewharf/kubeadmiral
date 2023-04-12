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

package worker

import (
	"strings"
	"time"

	"k8s.io/utils/pointer"
)

func (r *Result) String() string {
	var sb strings.Builder

	if r.Success {
		sb.WriteString("OK")
	} else {
		sb.WriteString("Failed")
	}

	if r.RequeueAfter != nil {
		sb.WriteString(", RequeueAfter(")
		sb.WriteString(r.RequeueAfter.String())
		sb.WriteString(")")
	}

	if r.Backoff {
		sb.WriteString(", Backoff")
	}

	return sb.String()
}

type Result struct {
	Success bool
	// RequeueAfter is the time to wait before the worker requeues the item. RequeueAFter is ignored if Backoff is true.
	RequeueAfter *time.Duration
	// Backoff indicates if the worker's backoff should be used to requeue the item. If Backoff is true, RequeueAfter is ignored.
	Backoff bool
}

// Convenience constants for Result
var (
	// StatusAllOK indicates that reconciliation was successful and the object should not be requeued.
	StatusAllOK = Result{Success: true, RequeueAfter: nil, Backoff: false}
	// StatusError indicates that reconciliation could not be completed due to an error, and the object should be requeued with backoff.
	StatusError = Result{Success: false, RequeueAfter: nil, Backoff: true}
	// StatusConflict indicates that reconciliation could not be completed due to a conflict
	// (e.g., AlreadyExists or Conflict), and the object should be requeued with a small delay.
	StatusConflict = Result{Success: false, RequeueAfter: pointer.Duration(500 * time.Millisecond), Backoff: false}
	// StatusErrorNoRetry indicates that the reconciliation could not be completed due to an error, but the object
	// should not be reenqueued
	StatusErrorNoRetry = Result{Success: false, RequeueAfter: nil, Backoff: false}
)
