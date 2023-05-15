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

package monitor

import (
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

// DoReport upload the meters to metrics2
func DoReport(meters *sync.Map, client stats.Metrics) {
	klog.Infof("Do report metrics.")
	DoReportLatencyCount(meters, client)
	DoReportSyncLatency(meters, client)
	DoReportStatusLatency(meters, client)
}

func DoReportStatusLatency(meters *sync.Map, client stats.Metrics) {
	throughput := 0

	meters.Range(func(k, v interface{}) bool {
		baseMeter := v.(BaseMeter)
		name := k.(string)
		// klog.Infof(
		// "Attempt to report: %s, status update %s, status success %s", name, baseMeter.lastStatusSyncedTimestamp, baseMeter.outOfSyncDuration)
		if baseMeter.lastStatusSyncedTimestamp.IsZero() {
			return true
		}
		if baseMeter.outOfSyncDuration > ReportInterval {
			tags := make([]stats.Tag, 0)
			tags = append(tags, stats.Tag{Name: "resourcename", Value: name})
			client.Timer("totalstatus.latency.ms", baseMeter.outOfSyncDuration.Milliseconds(), tags...)
			throughput += 1
		}
		return true
	})
	client.Store("totalstatus.throughput", throughput)
}

func DoReportSyncLatency(meters *sync.Map, client stats.Metrics) {
	now := metav1.Now()
	throughput := 0
	meters.Range(func(k, v interface{}) bool {
		baseMeter := v.(BaseMeter)
		name := k.(string)
		// klog.Infof("Attempt to report: ", name, baseMeter.creationTimestamp, baseMeter.lastUpdateTimestamp, baseMeter.syncSuccessTimestamp)
		if baseMeter.syncSuccessTimestamp.Add(ReportInterval).After(now.Time) {
			start := baseMeter.creationTimestamp
			if baseMeter.lastUpdateTimestamp.After(baseMeter.creationTimestamp.Time) {
				start = baseMeter.lastUpdateTimestamp
			}
			if baseMeter.syncSuccessTimestamp.After(start.Time) {
				diff := baseMeter.syncSuccessTimestamp.Sub(start.Time)
				tags := make([]stats.Tag, 0)
				tags = append(tags, stats.Tag{Name: "resourcename", Value: name})
				client.Timer("totalsync.latency.ms", diff.Milliseconds(), tags...)
				throughput += 1
			}
		}
		return true
	})

	client.Store("totalsync.throughput", throughput)
}

func DoReportLatencyCount(meters *sync.Map, client stats.Metrics) {
	latencyMetrics := make(map[string][]int)
	// []int{delayMinutes, countOfSyncDelay, countOfStatusDelay}
	latencyMetrics["latency5min"] = []int{5, 0, 0}
	latencyMetrics["latency10min"] = []int{10, 0, 0}
	latencyMetrics["latency30min"] = []int{30, 0, 0}
	latencyMetrics["latency1hour"] = []int{60, 0, 0}
	latencyMetrics["latency6hour"] = []int{180, 0, 0}
	latencyMetrics["latency1day"] = []int{1440, 0, 0}
	latency5minSyncMetrics := make(map[string]int)
	latency5minStatusMetrics := make(map[string]int)

	now := metav1.Now()
	meters.Range(func(k, v interface{}) bool {
		baseMeter := v.(BaseMeter)
		start := baseMeter.creationTimestamp
		if baseMeter.lastUpdateTimestamp.After(baseMeter.creationTimestamp.Time) {
			start = baseMeter.lastUpdateTimestamp
		}

		// the numbers object has not been synced
		name, ok := k.(string)
		if !ok {
			return true
		}

		if start.After(baseMeter.syncSuccessTimestamp.Time) {
			for mk, mv := range latencyMetrics {
				if now.After(start.Add(time.Duration(mv[0]) * time.Minute)) {
					klog.Infof(
						"Latency resource: %s with createTime: %s, updateTime: %s, syncSuccessTime: %s",
						name,
						baseMeter.creationTimestamp,
						baseMeter.lastUpdateTimestamp,
						baseMeter.syncSuccessTimestamp,
					)
					mv[1] += 1
					typeName := strings.Split(name, "/")[0]
					if mk == "latency5min" {
						latency5minSyncMetrics[typeName] += 1
					}
				}
				if baseMeter.outOfSyncDuration > time.Duration(mv[0])*time.Minute {
					klog.Infof(
						"Latency resource: %s with lastStatusSyncedTimestamp: %s, statusOutOfSyncDuration: %s",
						name,
						baseMeter.lastStatusSyncedTimestamp,
						baseMeter.outOfSyncDuration,
					)
					mv[2] += 1
					typeName := strings.Split(name, "/")[0]
					if mk == "latency5min" {
						latency5minStatusMetrics[typeName] += 1
					}
				}
			}
		}
		return true
	})

	for k, v := range latencyMetrics {
		client.Store(k+".count", v[1])
		klog.Infof("Attempt to report latency count: %s %d", k+".count: ", v[1])
	}

	for k, v := range latencyMetrics {
		client.Store(k+".status.count", v[2])
		klog.Infof("Attempt to report latency count: %s %d", k+".status.count: ", v[2])
	}

	for k, v := range latency5minSyncMetrics {
		client.Store("typelatency5min.count", v, stats.Tag{Name: "type", Value: k})
	}

	for k, v := range latency5minStatusMetrics {
		client.Store("typelatency5min.status.count", v, stats.Tag{Name: "type", Value: k})
	}
}
