/*
Copyright 2019 The Kubernetes Authors.

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

package generic

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
)

type Client interface {
	Create(ctx context.Context, obj client.Object) error
	Get(ctx context.Context, obj client.Object, namespace, name string) error
	Update(ctx context.Context, obj client.Object) error
	Delete(ctx context.Context, obj client.Object, namespace, name string, opts ...client.DeleteOption) error
	List(ctx context.Context, obj client.ObjectList, namespace string) error
	UpdateStatus(ctx context.Context, obj client.Object) error
	Patch(ctx context.Context, obj client.Object, patch client.Patch) error
	DeleteHistory(ctx context.Context, obj client.Object) error

	ListWithOptions(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error
}

type genericClient struct {
	client client.Client
}

func New(config *rest.Config) (Client, error) {
	client, err := client.New(config, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, err
	}
	return &genericClient{client}, err
}

func NewForConfigOrDie(config *rest.Config) Client {
	client, err := New(config)
	if err != nil {
		panic(err)
	}
	return client
}

func NewForConfigOrDieWithUserAgent(config *rest.Config, userAgent string) Client {
	configCopy := rest.CopyConfig(config)
	rest.AddUserAgent(configCopy, userAgent)
	return NewForConfigOrDie(configCopy)
}

func (c *genericClient) Create(ctx context.Context, obj client.Object) error {
	return c.client.Create(ctx, obj)
}

func (c *genericClient) Get(ctx context.Context, obj client.Object, namespace, name string) error {
	return c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj)
}

func (c *genericClient) Update(ctx context.Context, obj client.Object) error {
	return c.client.Update(ctx, obj)
}

func (c *genericClient) Delete(ctx context.Context, obj client.Object, namespace, name string, opts ...client.DeleteOption) error {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	if accessor == nil {
		return fmt.Errorf("nil accessor for generic client")
	}
	accessor.SetNamespace(namespace)
	accessor.SetName(name)
	return c.client.Delete(ctx, obj, opts...)
}

func (c *genericClient) List(ctx context.Context, obj client.ObjectList, namespace string) error {
	return c.client.List(ctx, obj, client.InNamespace(namespace))
}

func (c *genericClient) ListWithOptions(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	return c.client.List(ctx, obj, opts...)
}

func (c *genericClient) UpdateStatus(ctx context.Context, obj client.Object) error {
	return c.client.Status().Update(ctx, obj)
}

func (c *genericClient) Patch(ctx context.Context, obj client.Object, patch client.Patch) error {
	return c.client.Patch(ctx, obj, patch)
}

// controlledHistories returns all ControllerRevisions in namespace that selected by selector and owned by accessor
func (c *genericClient) controlledHistory(ctx context.Context, obj client.Object) ([]*appsv1.ControllerRevision, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to create accessor for kind %v: %s", obj.GetObjectKind(), err.Error())
	}
	selector := labels.SelectorFromSet(labels.Set{
		"uid": string(accessor.GetUID()),
	})

	opt1 := client.InNamespace(accessor.GetNamespace())
	opt2 := client.MatchingLabelsSelector{Selector: selector}
	historyList := &appsv1.ControllerRevisionList{}
	if err := c.ListWithOptions(ctx, historyList, opt1, opt2); err != nil {
		return nil, err
	}

	var result []*appsv1.ControllerRevision
	for i := range historyList.Items {
		history := historyList.Items[i]
		// Only add history that belongs to the API object
		if metav1.IsControlledBy(&history, accessor) {
			result = append(result, &history)
		}
	}
	return result, nil
}

func (c *genericClient) DeleteHistory(ctx context.Context, obj client.Object) error {
	historyList, err := c.controlledHistory(ctx, obj)
	if err != nil {
		return err
	}
	for _, history := range historyList {
		if err := c.Delete(ctx, history, history.Namespace, history.Name); err != nil {
			return err
		}
	}
	return nil
}
