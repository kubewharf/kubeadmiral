/*
Copyright 2018 The Kubernetes Authors.

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

package leaderelection

import (
	"context"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
)

func NewFederationLeaderElector(
	config *rest.Config,
	fnRunManager func(context.Context),
	fedNamespace string,
	component string,
	healthzAdaptor *leaderelection.HealthzAdaptor,
) (*leaderelection.LeaderElector, error) {
	leaderElectionClient := kubernetes.NewForConfigOrDie(config)

	hostname, err := os.Hostname()
	if err != nil {
		klog.Errorf("Unable to get hostname: %v", err)
		return nil, err
	}

	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: leaderElectionClient.CoreV1().Events(fedNamespace)})
	eventRecorder := broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: component})

	id := hostname + "_" + string(uuid.NewUUID())
	klog.Infof("Using leader election identity %q", id)
	rl, err := resourcelock.New(resourcelock.LeasesResourceLock,
		fedNamespace,
		component,
		leaderElectionClient.CoreV1(),
		leaderElectionClient.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: eventRecorder,
		})
	if err != nil {
		klog.Errorf("Couldn't create resource lock: %v", err)
		return nil, err
	}

	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   5 * time.Second,
		WatchDog:      healthzAdaptor,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				klog.Infof("Promoted as leader")
				fnRunManager(ctx)
			},
			OnStoppedLeading: func() {
				klog.Infof("Leader election lost")
			},
		},
	})
	if err != nil {
		return nil, err
	}

	healthzAdaptor.SetLeaderElection(elector)

	return elector, nil
}
