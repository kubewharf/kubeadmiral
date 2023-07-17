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

package logging

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

func InjectLogger(ctx context.Context, logger klog.Logger) (context.Context, logr.Logger) {
    ctx = klog.NewContext(ctx, logger)
	return ctx, logger
}

func InjectLoggerValues(ctx context.Context, values ...interface{}) (context.Context, logr.Logger) {
	logger := klog.FromContext(ctx).WithValues(values...)
	ctx = klog.NewContext(ctx, logger)
	return ctx, logger
}

func InjectLoggerName(ctx context.Context, name string) (context.Context, logr.Logger) {
	logger := klog.LoggerWithName(klog.FromContext(ctx), name)
	ctx = klog.NewContext(ctx, logger)
	return ctx, logger
}
