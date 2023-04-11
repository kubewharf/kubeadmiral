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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	errorutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
)

type failure[T any] struct {
	t       T
	message string
}

// Runs fn for each item in items and fails the test if any of the fn calls fail.
func AssertForItems[T any](
	items []T,
	fn func(g gomega.Gomega, t T),
) {
	ginkgo.GinkgoHelper()

	mu := sync.Mutex{}
	failures := make([]failure[T], 0, len(items))

	wg := sync.WaitGroup{}
	for _, item := range items {
		wg.Add(1)
		go func(item T) {
			defer wg.Done()
			g := gomega.NewWithT(ginkgo.GinkgoT())
			g.ConfigureWithFailHandler(func(message string, callerSkip ...int) {
				mu.Lock()
				defer mu.Unlock()
				failures = append(failures, failure[T]{t: item, message: message})
			})
			fn(g, item)
		}(item)
	}

	wg.Wait()

	if len(failures) == 0 {
		return
	}

	buf := strings.Builder{}
	for _, failure := range failures {
		buf.WriteString("\n")
		buf.WriteString(fmt.Sprintf("For item `%v`:", failure.t))
		buf.WriteString("\n")
		for _, line := range strings.Split(failure.message, "\n") {
			buf.WriteString("    ")
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}
	ginkgo.Fail(buf.String())
}

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
