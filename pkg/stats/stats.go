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

	log "github.com/sirupsen/logrus"
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
	}
}

type mockMetrics struct {
	prefix     string
	logMetrics bool
}

func convertTToFields(tags []Tag) log.Fields {
	fields := make(map[string]interface{}, len(tags))
	for _, v := range tags {
		fields[v.Name] = v.Value
	}
	return log.Fields(fields)
}

func (s *mockMetrics) Store(name string, val interface{}, tags ...Tag) {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("store %s_%s = %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
}

func (s *mockMetrics) Counter(name string, val interface{}, tags ...Tag) {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("rate %s_%s + %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
}

func (s *mockMetrics) Summary(name string, val interface{}, tags ...Tag) {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("duration %s_%s <- %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
}

func (s *mockMetrics) Duration(name string, start time.Time, tags ...Tag) {
	duration := time.Since(start).Seconds()
	key := fmt.Sprintf("%s_seconds", name)
	s.Summary(key, duration, tags...)
}
