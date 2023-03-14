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
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/history"
)

type Client interface {
	Create(ctx context.Context, obj client.Object) error
	Get(ctx context.Context, obj client.Object, namespace, name string) error
	Update(ctx context.Context, obj client.Object) error
	Delete(ctx context.Context, obj client.Object, namespace, name string) error
	List(ctx context.Context, obj client.ObjectList, namespace string) error
	UpdateStatus(ctx context.Context, obj client.Object) error
	Patch(ctx context.Context, obj client.Object, patch client.Patch) error
	Rollback(ctx context.Context, obj client.Object, toRevision int64) error
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

func (c *genericClient) Delete(ctx context.Context, obj client.Object, namespace, name string) error {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	if accessor == nil {
		return fmt.Errorf("nil accessor for generic client")
	}
	accessor.SetNamespace(namespace)
	accessor.SetName(name)
	return c.client.Delete(ctx, obj)
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

// Rollback rollbacks federated Object such as FederatedDeployment
func (c *genericClient) Rollback(ctx context.Context, obj client.Object, toRevision int64) error {
	if toRevision < 0 {
		return fmt.Errorf("unable to find specified revision %v in history", toRevision)
	}
	if toRevision == 0 {
		// try to get last revision from annotations, fallback to list all revisions on error
		if err := c.rollbackToLastRevision(ctx, obj); err == nil {
			return nil
		}
	}

	history, err := c.controlledHistory(ctx, obj)
	if err != nil {
		return fmt.Errorf("failed to list history: %s", err)
	}
	if toRevision == 0 && len(history) <= 1 {
		return fmt.Errorf("no last revision to roll back to")
	}

	toHistory := findHistory(toRevision, history)
	if toHistory == nil {
		return fmt.Errorf("unable to find specified revision %v in history", toHistory)
	}

	// Restore revision
	if err := c.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, toHistory.Data.Raw)); err != nil {
		return fmt.Errorf("failed restoring revision %d: %v", toRevision, err)
	}
	return nil
}

func (c *genericClient) rollbackToLastRevision(ctx context.Context, obj client.Object) error {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	lastRevisionNameWithHash := accessor.GetAnnotations()[common.LastRevisionAnnotation]
	if len(lastRevisionNameWithHash) == 0 {
		return fmt.Errorf("annotation: %s not found", common.LastRevisionAnnotation)
	}

	lastRevisionName, err := c.checkLastRevisionNameWithHash(lastRevisionNameWithHash, obj)
	if err != nil {
		return fmt.Errorf("failed to check last revision name, err: %v", err)
	}

	latestRevision := &appsv1.ControllerRevision{}
	if err := c.Get(ctx, latestRevision, accessor.GetNamespace(), lastRevisionName); err != nil {
		return err
	}

	// restore latest revision
	if err := c.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, latestRevision.Data.Raw)); err != nil {
		return fmt.Errorf("failed restoring latest revision: %v", err)
	}
	return nil
}

func (c *genericClient) checkLastRevisionNameWithHash(lastRevisionNameWithHash string, obj client.Object) (string, error) {
	parts := strings.Split(lastRevisionNameWithHash, "|")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid lastRevisionNameWithHash: %s", lastRevisionNameWithHash)
	}
	lastRevisionName, hash := parts[0], parts[1]

	utdObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return "", err
	}

	template, ok, err := unstructured.NestedMap(utdObj, "spec", "template", "spec", "template")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("spec.template.spec.template is not found, fedResource: %+v", obj)
	}

	if templateHash := history.HashObject(template); templateHash != hash {
		return "", fmt.Errorf("pod template hash: %s, last revision name suffix: %s, they should be equal", templateHash, hash)
	}
	return lastRevisionName, nil
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

// findHistory returns a controllerrevision of a specific revision from the given controllerrevisions.
// It returns nil if no such controllerrevision exists.
// If toRevision is 0, the last previously used history is returned.
func findHistory(toRevision int64, allHistory []*appsv1.ControllerRevision) *appsv1.ControllerRevision {
	if toRevision == 0 && len(allHistory) <= 1 {
		return nil
	}

	// Find the history to rollback to
	var toHistory *appsv1.ControllerRevision
	if toRevision == 0 {
		// If toRevision == 0, find the latest revision (2nd max)
		history.SortControllerRevisions(allHistory)
		toHistory = allHistory[len(allHistory)-2]
	} else {
		for _, h := range allHistory {
			if h.Revision == toRevision {
				// If toRevision != 0, find the history with matching revision
				return h
			}
		}
	}
	return toHistory
}
