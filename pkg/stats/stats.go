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

package stats

import (
	"fmt"
	"time"

	"k8s.io/klog/v2"
)

// Tag keeps tag value for a stats point
type Tag struct {
	Name  string
	Value string
}

// Metrics declares common methods provided by metrics client
type Metrics interface {
	Store(name string, val interface{}, tags ...Tag)
	Counter(name string, val interface{}, tags ...Tag)
	Summary(name string, val interface{}, tags ...Tag)
	Duration(name string, start time.Time, tags ...Tag)
}

// NewMock creates a mock metrics client which outputs stats via default logger.
func NewMock(env, component string, logMetrics bool) Metrics {
	prefix := fmt.Sprintf("%s.%s", component, env)
	return &mockMetrics{
		prefix:     prefix,
		logMetrics: logMetrics,
		logger:     klog.Background(),
	}
}

type mockMetrics struct {
	prefix     string
	logMetrics bool
	logger     klog.Logger
}

func convertTToKeysAndValues(tags []Tag) []interface{} {
	pairs := make([]interface{}, 0)
	for index := range tags {
		pairs = append(pairs, tags[index].Name)
		pairs = append(pairs, tags[index].Value)
	}
	return pairs
}

func (s *mockMetrics) Store(name string, val interface{}, tags ...Tag) {
	if s.logMetrics {
		fields := convertTToKeysAndValues(tags)
		msg := fmt.Sprintf("store %s_%s = %v", s.prefix, name, val)
		s.logger.WithValues(fields...).Info(msg)
	}
}

func (s *mockMetrics) Counter(name string, val interface{}, tags ...Tag) {
	if s.logMetrics {
		fields := convertTToKeysAndValues(tags)
		msg := fmt.Sprintf("rate %s_%s + %v", s.prefix, name, val)
		s.logger.WithValues(fields...).Info(msg)
	}
}

func (s *mockMetrics) Summary(name string, val interface{}, tags ...Tag) {
	if s.logMetrics {
		fields := convertTToKeysAndValues(tags)
		msg := fmt.Sprintf("duration %s_%s <- %v", s.prefix, name, val)
		s.logger.WithValues(fields...).Info(msg)
	}
}

func (s *mockMetrics) Duration(name string, start time.Time, tags ...Tag) {
	duration := time.Since(start).Seconds()
	key := fmt.Sprintf("%s_seconds", name)
	s.Summary(key, duration, tags...)
}
