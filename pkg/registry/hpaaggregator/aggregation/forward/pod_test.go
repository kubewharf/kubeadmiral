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

package forward

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	api "k8s.io/kubernetes/pkg/apis/core"
)

func TestPodREST_convertAndFilterPodObject(t *testing.T) {
	p1, p2 := newPod("default", "test")

	type args struct {
		objs     []runtime.Object
		selector fields.Selector
	}
	tests := []struct {
		name string
		args args
		want []api.Pod
	}{
		{
			name: "1 pod",
			args: args{
				objs:     []runtime.Object{&p2},
				selector: fields.Everything(),
			},
			want: []api.Pod{p1},
		},
		{
			name: "2 obj, 1 pod",
			args: args{
				objs:     []runtime.Object{&corev1.Node{}, &p2},
				selector: fields.Everything(),
			},
			want: []api.Pod{p1},
		},
		{
			name: "1 pod, with selector",
			args: args{
				objs:     []runtime.Object{&corev1.Node{}, &p2},
				selector: fields.ParseSelectorOrDie("metadata.name=test"),
			},
			want: []api.Pod{p1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertAndFilterPodObject(tt.args.objs, tt.args.selector)
			if !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("convertAndFilterPodObject() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func newPod(ns, name string) (api.Pod, corev1.Pod) {
	p1 := api.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{ns: name},
		},
		Spec: api.PodSpec{SecurityContext: &api.PodSecurityContext{}}, // used for convert
	}

	p2 := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{ns: name},
		},
		Spec: corev1.PodSpec{SecurityContext: &corev1.PodSecurityContext{}}, // used for convert
	}

	return p1, p2
}

// fakes both PodLister and PodNamespaceLister at once
type fakePodLister struct {
	data []*corev1.Pod
	err  error
}

func (pl fakePodLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	if pl.err != nil {
		return nil, pl.err
	}
	res := []runtime.Object{}
	for _, pod := range pl.data {
		if selector.Matches(labels.Set(pod.Labels)) {
			res = append(res, pod)
		}
	}
	return res, nil
}
func (pl fakePodLister) Get(name string) (runtime.Object, error) {
	if pl.err != nil {
		return nil, pl.err
	}
	for _, pod := range pl.data {
		if pod.Name == name {
			return pod, nil
		}
	}
	return nil, nil
}
func (pl fakePodLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return pl
}

func TestPodREST_Get(t *testing.T) {
	p1, p2 := newPod("default", "test")

	type args struct {
		ctx  context.Context
		name string
		opts *metav1.GetOptions
	}
	tests := []struct {
		name      string
		podLister cache.GenericLister
		args      args
		want      runtime.Object
		wantErr   bool
	}{
		{
			name:      "get pod",
			podLister: fakePodLister{data: []*corev1.Pod{&p2}},
			args: args{
				ctx:  context.Background(),
				name: "test",
				opts: nil,
			},
			want:    &p1,
			wantErr: false,
		},
		{
			name:      "get pod failed",
			podLister: fakePodLister{err: errors.New("fake")},
			args: args{
				ctx:  context.Background(),
				name: "test",
				opts: nil,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:      "pod not found",
			podLister: fakePodLister{err: apierrors.NewNotFound(schema.GroupResource{}, "")},
			args: args{
				ctx:  context.Background(),
				name: "test",
				opts: nil,
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodREST{
				podLister: tt.podLister,
			}
			got, err := p.Get(tt.args.ctx, tt.args.name, tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("Get() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPodREST_List(t *testing.T) {
	p1, p2 := newPod("default", "test")

	type args struct {
		ctx     context.Context
		options *metainternalversion.ListOptions
	}
	tests := []struct {
		name      string
		podLister cache.GenericLister
		args      args
		want      runtime.Object
		wantErr   bool
	}{
		{
			name:      "list pod",
			podLister: fakePodLister{data: []*corev1.Pod{&p2}},
			args: args{
				ctx:     context.Background(),
				options: nil,
			},
			want:    &api.PodList{Items: []api.Pod{p1}},
			wantErr: false,
		},
		{
			name:      "list pod with label selector",
			podLister: fakePodLister{data: []*corev1.Pod{&p2}},
			args: args{
				ctx: context.Background(),
				options: &metainternalversion.ListOptions{
					LabelSelector: labels.SelectorFromSet(map[string]string{"default": "test"}),
				},
			},
			want:    &api.PodList{Items: []api.Pod{p1}},
			wantErr: false,
		},
		{
			name:      "list pod with field selector",
			podLister: fakePodLister{data: []*corev1.Pod{&p2}},
			args: args{
				ctx: context.Background(),
				options: &metainternalversion.ListOptions{
					FieldSelector: fields.ParseSelectorOrDie("metadata.name=test"),
				},
			},
			want:    &api.PodList{Items: []api.Pod{p1}},
			wantErr: false,
		},
		{
			name:      "list pod failed",
			podLister: fakePodLister{err: errors.New("fake")},
			args: args{
				ctx:     context.Background(),
				options: nil,
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodREST{
				podLister: tt.podLister,
			}
			got, err := p.List(tt.args.ctx, tt.args.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("List() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("List() got = %v, want %v", got, tt.want)
			}
		})
	}
}
