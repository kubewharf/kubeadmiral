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

package util

import (
	"context"
	"errors"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	errorutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
)

// PollUntilForItems polls each item in items with condFn until
// it returns true, an error, or until ctx is cancelled.
func PollUntilForItems[T any](
	ctx context.Context,
	items []T,
	condFn func(T) (bool, error),
	interval time.Duration,
) ([]T, error) {
	wg := sync.WaitGroup{}
	errCh := make(chan error, len(items))
	failedCh := make(chan T, len(items))

	for _, item := range items {
		wg.Add(1)
		go func(t T) {
			defer wg.Done()

			if err := wait.PollUntil(
				interval,
				func() (done bool, err error) {
					return condFn(t)
				},
				ctx.Done(),
			); errors.Is(err, wait.ErrWaitTimeout) {
				failedCh <- t
			} else if err != nil {
				errCh <- err
			}
		}(item)
	}

	go func() {
		wg.Wait()
		close(errCh)
		close(failedCh)
	}()

	failed := []T{}
	for item := range failedCh {
		failed = append(failed, item)
	}

	errList := []error{}
	for err := range errCh {
		errList = append(errList, err)
	}
	if len(errList) > 0 {
		return failed, errorutil.NewAggregate(errList)
	}

	return failed, nil
}

func NameList[T metav1.Object](objs []T) (names []string) {
	for _, o := range objs {
		names = append(names, o.GetName())
	}
	return
}

func FilterOutE2ETestObjects[T metav1.Object](objs []T) (ret []T) {
	for _, o := range objs {
		if _, ok := o.GetLabels()[framework.E2ETestObjectLabel]; !ok {
			ret = append(ret, o)
		}
	}
	return
}
