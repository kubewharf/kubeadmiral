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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
)

type EventRecorderMux struct {
	recorders []transformedEventRecorder
}

func NewDefederatingRecorderMux(
	kubeClient kubernetes.Interface,
	userAgent string,
	withLoggingVerbosity klog.Level,
) *EventRecorderMux {
	baseBroadcaster := record.NewBroadcaster()
	baseBroadcaster.StartStructuredLogging(withLoggingVerbosity)
	baseBroadcaster.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	return new(EventRecorderMux).
		With(baseBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: userAgent})).
		WithDefederateTransformer(baseBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: userAgent}))
}

func (mux *EventRecorderMux) With(recorder record.EventRecorder) *EventRecorderMux {
	mux.recorders = append(mux.recorders, transformedEventRecorder{recorder: recorder})
	return mux
}

func (mux *EventRecorderMux) WithTransform(
	recorder record.EventRecorder,
	transformObject func(runtime.Object) runtime.Object,
) *EventRecorderMux {
	mux.recorders = append(
		mux.recorders,
		transformedEventRecorder{recorder: recorder, transformObject: transformObject},
	)
	return mux
}

func (mux *EventRecorderMux) WithDefederateTransformer(recorder record.EventRecorder) *EventRecorderMux {
	return mux.WithTransform(recorder, func(obj runtime.Object) runtime.Object {
		fedObject, ok := obj.(fedcorev1a1.GenericFederatedObject)
		if !ok {
			return nil
		}

		templateMeta, err := fedObject.GetSpec().GetTemplateMetadata()
		if err != nil {
			return nil
		}

		for _, owner := range fedObject.GetOwnerReferences() {
			ownerIsSourceObject := owner.Controller != nil && *owner.Controller &&
				owner.APIVersion == templateMeta.APIVersion && owner.Kind == templateMeta.Kind &&
				owner.Name == templateMeta.Name
			if !ownerIsSourceObject {
				continue
			}

			return &corev1.ObjectReference{
				APIVersion:      owner.APIVersion,
				Kind:            owner.Kind,
				Namespace:       fedObject.GetNamespace(),
				Name:            owner.Name,
				UID:             owner.UID,
				ResourceVersion: "", // this value should not be used
			}
		}

		return nil
	})
}

type transformedEventRecorder struct {
	transformObject func(runtime.Object) runtime.Object
	recorder        record.EventRecorder
}

func (mux *EventRecorderMux) Event(object runtime.Object, eventtype, reason, message string) {
	for _, recorder := range mux.recorders {
		thisObject := object
		if recorder.transformObject != nil {
			thisObject = recorder.transformObject(object)
		}

		if thisObject != nil {
			recorder.recorder.Event(thisObject, eventtype, reason, message)
		}
	}
}

// Eventf is just like Event, but with Sprintf for the message field.
func (mux *EventRecorderMux) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	mux.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

// AnnotatedEventf is just like eventf, but with annotations attached
func (mux *EventRecorderMux) AnnotatedEventf(
	object runtime.Object,
	annotations map[string]string,
	eventtype, reason, messageFmt string,
	args ...interface{},
) {
	for _, recorder := range mux.recorders {
		thisObject := object
		if recorder.transformObject != nil {
			thisObject = recorder.transformObject(object)
		}

		if thisObject != nil {
			recorder.recorder.AnnotatedEventf(thisObject, annotations, eventtype, reason, messageFmt, args...)
		}
	}
}
