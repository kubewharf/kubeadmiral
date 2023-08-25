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

package eventsink

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sjson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

// a thread-safe event list
type eventList struct {
	mu        sync.Mutex
	slice     []*corev1.Event
	immutable bool
}

func (list *eventList) push(event *corev1.Event) {
	list.mu.Lock()
	defer list.mu.Unlock()
	if list.immutable {
		panic("push to eventList after freeze")
	}
	list.slice = append(list.slice, event)
}

func (list *eventList) len() int {
	list.mu.Lock()
	defer list.mu.Unlock()
	return len(list.slice)
}

func (list *eventList) freeze() []*corev1.Event {
	list.mu.Lock()
	defer list.mu.Unlock()
	list.immutable = true
	return list.slice
}

func TestDefederateTransformer(t *testing.T) {
	t.Run("should emit event for source object", func(t *testing.T) {
		sourceObj := appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: appsv1.SchemeGroupVersion.String(),
				Kind:       common.DeploymentKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-dp",
				UID:  "testuid",
			},
		}
		testObj := fedcorev1a1.FederatedObject{}

		testObj.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: sourceObj.APIVersion,
				Kind:       sourceObj.Kind,
				Name:       sourceObj.Name,
				UID:        sourceObj.UID,
				Controller: pointer.Bool(true),
			},
		})

		assert := assert.New(t)
		if data, err := k8sjson.Marshal(&sourceObj); err != nil {
			assert.NoError(err)
			return
		} else {
			testObj.Spec.Template.Raw = data
		}

		events := &eventList{}
		testBroadcaster := record.NewBroadcasterForTests(0)
		testBroadcaster.StartEventWatcher(events.push)

		mux := &EventRecorderMux{}
		mux = mux.WithDefederateTransformer(
			testBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "test"}),
		)
		mux.Event(&testObj, corev1.EventTypeWarning, "testReason", "testMessage")
		testBroadcaster.Shutdown()

		assert.Eventually(
			func() bool { return events.len() == 1 },
			time.Second,
			100*time.Millisecond,
		)

		eventSlice := events.freeze()
		emitted := eventSlice[0]
		eventObj := emitted.InvolvedObject
		assert.Equal(sourceObj.Kind, eventObj.Kind)
		assert.Equal(sourceObj.ResourceVersion, eventObj.ResourceVersion)
		assert.Equal(sourceObj.Name, eventObj.Name)
		assert.Equal(sourceObj.UID, eventObj.UID)
		assert.Equal(sourceObj.APIVersion, eventObj.APIVersion)
	})
	t.Run("should not emit event if source owner reference does not exist", func(t *testing.T) {
		sourceObj := appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: appsv1.SchemeGroupVersion.String(),
				Kind:       common.DeploymentKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-dp",
				UID:  "testuid",
			},
		}
		testObj := fedcorev1a1.FederatedObject{}

		assert := assert.New(t)
		if data, err := k8sjson.Marshal(&sourceObj); err != nil {
			assert.NoError(err)
			return
		} else {
			testObj.Spec.Template.Raw = data
		}

		events := &eventList{}
		testBroadcaster := record.NewBroadcasterForTests(0)
		testBroadcaster.StartEventWatcher(events.push)

		mux := &EventRecorderMux{}
		mux = mux.WithDefederateTransformer(
			testBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "test"}),
		)
		mux.Event(&testObj, corev1.EventTypeWarning, "testReason", "testMessage")
		testBroadcaster.Shutdown()

		assert.Never(
			func() bool { return events.len() > 0 },
			time.Second,
			100*time.Millisecond,
		)
	})
	t.Run("should not emit event if owner reference is not the source object", func(t *testing.T) {
		sourceObj := appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: appsv1.SchemeGroupVersion.String(),
				Kind:       common.DeploymentKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-dp",
				UID:  "testuid",
			},
		}
		testObj := fedcorev1a1.FederatedObject{}

		testObj.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: "v1",
				Kind:       "Secret",
				Name:       "test-secret",
				UID:        "testuid",
				Controller: pointer.Bool(true),
			},
		})

		assert := assert.New(t)
		if data, err := k8sjson.Marshal(&sourceObj); err != nil {
			assert.NoError(err)
			return
		} else {
			testObj.Spec.Template.Raw = data
		}

		events := &eventList{}
		testBroadcaster := record.NewBroadcasterForTests(0)
		testBroadcaster.StartEventWatcher(events.push)

		mux := &EventRecorderMux{}
		mux = mux.WithDefederateTransformer(
			testBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "test"}),
		)
		mux.Event(&testObj, corev1.EventTypeWarning, "testReason", "testMessage")
		testBroadcaster.Shutdown()

		assert.Never(
			func() bool { return events.len() > 0 },
			time.Second,
			100*time.Millisecond,
		)
	})
}
