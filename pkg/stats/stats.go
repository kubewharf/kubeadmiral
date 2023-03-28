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
	Store(name string, val interface{}, tags ...Tag) error
	Counter(name string, val interface{}, tags ...Tag) error
	Rate(name string, val interface{}, tags ...Tag) error
	Timer(name string, val interface{}, tags ...Tag) error
	Duration(name string, start time.Time, tags ...Tag) error
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

func (s *mockMetrics) Store(name string, val interface{}, tags ...Tag) error {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("store %s.%s = %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
	return nil
}

func (s *mockMetrics) Counter(name string, val interface{}, tags ...Tag) error {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("counter %s.%s + %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
	return nil
}

func (s *mockMetrics) Rate(name string, val interface{}, tags ...Tag) error {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("rate %s.%s + %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
	return nil
}

func (s *mockMetrics) Timer(name string, val interface{}, tags ...Tag) error {
	if s.logMetrics {
		fields := convertTToFields(tags)
		msg := fmt.Sprintf("duration %s.%s <- %v", s.prefix, name, val)
		log.WithFields(fields).Infoln(msg)
	}
	return nil
}

func (s *mockMetrics) Duration(name string, start time.Time, tags ...Tag) error {
	duration := time.Since(start).Nanoseconds() / 1000 / 1000
	key := fmt.Sprintf("%s.ms", name)
	return s.Timer(key, duration, tags...)
}
