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

package history

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	core "k8s.io/client-go/testing"
)

type FakeStore struct {
	lister appsv1listers.ControllerRevisionLister
}

// Add adds the given object to the accumulator associated with the given object's key
func (f *FakeStore) Add(obj interface{}) error {
	return nil
}

// Update updates the given object in the accumulator associated with the given object's key
func (f *FakeStore) Update(obj interface{}) error {
	return nil
}

// Delete deletes the given object from the accumulator associated with the given object's key
func (f *FakeStore) Delete(obj interface{}) error {
	return nil
}

// List returns a list of all the currently non-empty accumulators
func (f *FakeStore) List() []interface{} {
	crs, _ := f.lister.List(labels.Everything())
	rets := make([]interface{}, 0)
	for i := range crs {
		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(crs[i])
		if err == nil {
			rets = append(rets, &unstructured.Unstructured{Object: obj})
		}
	}
	return rets
}

// ListKeys returns a list of all the keys currently associated with non-empty accumulators
func (f *FakeStore) ListKeys() []string {
	return nil
}

// Get returns the accumulator associated with the given object's key
func (f *FakeStore) Get(obj interface{}) (item interface{}, exists bool, err error) {
	return nil, false, nil
}

// GetByKey returns the accumulator associated with the given key
func (f *FakeStore) GetByKey(key string) (item interface{}, exists bool, err error) {
	return nil, false, nil
}

// Replace will delete the contents of the store, using instead the
// given list. Store takes ownership of the list, you should not reference
// it after calling this function.
func (f *FakeStore) Replace([]interface{}, string) error {
	return nil
}

// Resync is meaningless in the terms appearing here but has
// meaning in some implementations that have non-trivial
// additional behavior (e.g., DeltaFIFO).
func (f *FakeStore) Resync() error {
	return nil
}

func TestRealHistory_ListControllerRevisions(t *testing.T) {
	type testcase struct {
		name      string
		parent    metav1.Object
		selector  labels.Selector
		revisions []*appsv1.ControllerRevision
		want      map[string]bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)

		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		for i := range test.revisions {
			informer.Informer().GetIndexer().Add(test.revisions[i])
		}

		store := &FakeStore{
			lister: informer.Lister(),
		}
		history := NewHistory(client, store)
		revisions, err := history.ListControllerRevisions(test.parent, test.selector)
		if err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		got := make(map[string]bool)
		for i := range revisions {
			got[revisions[i].Name] = true
		}
		if !reflect.DeepEqual(test.want, got) {
			t.Errorf("%s: want %v got %v", test.name, test.want, got)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	sel1, err := metav1.LabelSelectorAsSelector(ss1.Spec.Selector)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	ss1Orphan, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		3,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Orphan.Namespace = ss1.Namespace
	ss1Orphan.OwnerReferences = nil

	tests := []testcase{
		{
			name:      "selects none",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: nil,
			want:      map[string]bool{},
		},
		{
			name:      "selects all",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2},
			want:      map[string]bool{ss1Rev1.Name: true, ss1Rev2.Name: true},
		},
		{
			name:      "doesn't select another Objects history",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2, ss2Rev1},
			want:      map[string]bool{ss1Rev1.Name: true, ss1Rev2.Name: true},
		},
		{
			name:      "selects orphans",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2, ss1Orphan},
			want:      map[string]bool{ss1Rev1.Name: true, ss1Rev2.Name: true, ss1Orphan.Name: true},
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestFakeHistory_ListControllerRevisions(t *testing.T) {
	type testcase struct {
		name      string
		parent    metav1.Object
		selector  labels.Selector
		revisions []*appsv1.ControllerRevision
		want      map[string]bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)

		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		for i := range test.revisions {
			informer.Informer().GetIndexer().Add(test.revisions[i])
		}

		history := NewFakeHistory(informer)
		revisions, err := history.ListControllerRevisions(test.parent, test.selector)
		if err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		got := make(map[string]bool)
		for i := range revisions {
			got[revisions[i].Name] = true
		}
		if !reflect.DeepEqual(test.want, got) {
			t.Errorf("%s: want %v got %v", test.name, test.want, got)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	sel1, err := metav1.LabelSelectorAsSelector(ss1.Spec.Selector)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	ss1Orphan, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		3,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Orphan.Namespace = ss1.Namespace
	ss1Orphan.OwnerReferences = nil

	tests := []testcase{
		{
			name:      "selects none",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: nil,
			want:      map[string]bool{},
		},
		{
			name:      "selects all",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2},
			want:      map[string]bool{ss1Rev1.Name: true, ss1Rev2.Name: true},
		},
		{
			name:      "doesn't select another Objects history",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2, ss2Rev1},
			want:      map[string]bool{ss1Rev1.Name: true, ss1Rev2.Name: true},
		},
		{
			name:      "selects orphans",
			parent:    &ss1.ObjectMeta,
			selector:  sel1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2, ss1Orphan},
			want:      map[string]bool{ss1Rev1.Name: true, ss1Rev2.Name: true, ss1Orphan.Name: true},
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestRealHistory_CreateControllerRevision(t *testing.T) {
	type testcase struct {
		name     string
		parent   metav1.Object
		revision *appsv1.ControllerRevision
		existing []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		rename bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)

		store := &FakeStore{
			lister: informer.Lister(),
		}
		history := NewHistory(client, store)
		var collisionCount int32
		for _, item := range test.existing {
			_, err := client.AppsV1().
				ControllerRevisions(item.parent.GetNamespace()).
				Create(context.TODO(), item.revision, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
		}
		// Clear collisionCount before creating the test revision
		collisionCount = 0
		created, err := history.CreateControllerRevision(test.parent, test.revision, &collisionCount)
		if err != nil {
			t.Errorf("%s: %s", test.name, err)
		}

		if test.rename {
			if created.Name == test.revision.Name {
				t.Errorf("%s: wanted rename got %s %s", test.name, created.Name, test.revision.Name)
			}
			expectedName := ControllerRevisionName(
				test.parent.GetName(),
				HashControllerRevision(test.revision, &collisionCount),
			)
			if created.Name != expectedName {
				t.Errorf("%s: on name collision wanted new name %s got %s", test.name, expectedName, created.Name)
			}

			// Second name collision will be caused by an identical revision, so no need to do anything
			_, err = history.CreateControllerRevision(test.parent, test.revision, &collisionCount)
			if err != nil {
				t.Errorf("%s: %s", test.name, err)
			}
			if collisionCount != 1 {
				t.Errorf("%s: on second name collision wanted collisionCount 1 got %d", test.name, collisionCount)
			}
		}
		if !test.rename && created.Name != test.revision.Name {
			t.Errorf("%s: wanted %s got %s", test.name, test.revision.Name, created.Name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace

	// Create a new revision with the same name and hash label as an existing revision, but with
	// a different template. This could happen as a result of a hash collision, but in this test
	// this situation is created by setting name and hash label to values known to be in use by
	// an existing revision.
	modTemplate := ss1.Spec.Template.DeepCopy()
	modTemplate.Labels["foo"] = "not_bar"
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(modTemplate),
		2,
		ss1.Status.CollisionCount,
	)
	ss1Rev2.Name = ss1Rev1.Name
	ss1Rev2.Labels[ControllerRevisionHashLabel] = ss1Rev1.Labels[ControllerRevisionHashLabel]
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:     "creates new",
			parent:   &ss1.ObjectMeta,
			revision: ss1Rev1,
			existing: nil,

			rename: false,
		},
		{
			name:     "create doesn't conflict when parents differ",
			parent:   &ss2.ObjectMeta,
			revision: ss2Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},

			rename: false,
		},
		{
			name:     "create renames on conflict",
			parent:   &ss1.ObjectMeta,
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev2,
				},
			},
			rename: true,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestFakeHistory_CreateControllerRevision(t *testing.T) {
	type testcase struct {
		name     string
		parent   metav1.Object
		revision *appsv1.ControllerRevision
		existing []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		rename bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)

		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		history := NewFakeHistory(informer)

		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		// Clear collisionCount before creating the test revision
		collisionCount = 0
		created, err := history.CreateControllerRevision(test.parent, test.revision, &collisionCount)
		if err != nil {
			t.Errorf("%s: %s", test.name, err)
		}

		if test.rename {
			if created.Name == test.revision.Name {
				t.Errorf("%s: wanted rename got %s %s", test.name, created.Name, test.revision.Name)
			}
			expectedName := ControllerRevisionName(
				test.parent.GetName(),
				HashControllerRevision(test.revision, &collisionCount),
			)
			if created.Name != expectedName {
				t.Errorf("%s: on name collision wanted new name %s got %s", test.name, expectedName, created.Name)
			}

			// Second name collision should have incremented collisionCount to 2
			_, err = history.CreateControllerRevision(test.parent, test.revision, &collisionCount)
			if err != nil {
				t.Errorf("%s: %s", test.name, err)
			}
			if collisionCount != 2 {
				t.Errorf("%s: on second name collision wanted collisionCount 1 got %d", test.name, collisionCount)
			}
		}
		if !test.rename && created.Name != test.revision.Name {
			t.Errorf("%s: wanted %s got %s", test.name, test.revision.Name, created.Name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:     "creates new",
			parent:   &ss1.ObjectMeta,
			revision: ss1Rev1,
			existing: nil,

			rename: false,
		},
		{
			name:     "create doesn't conflict when parents differ",
			parent:   &ss2.ObjectMeta,
			revision: ss2Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},

			rename: false,
		},
		{
			name:     "create renames on conflict",
			parent:   &ss1.ObjectMeta,
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			rename: true,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestRealHistory_UpdateControllerRevision(t *testing.T) {
	conflictAttempts := 0
	type testcase struct {
		name        string
		revision    *appsv1.ControllerRevision
		newRevision int64
		existing    []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		reactor core.ReactionFunc
		err     bool
	}
	conflictSuccess := func(action core.Action) (bool, runtime.Object, error) {
		defer func() {
			conflictAttempts++
		}()
		switch action.(type) {

		case core.UpdateActionImpl:
			update := action.(core.UpdateAction)
			if conflictAttempts < 2 {
				return true, update.GetObject(), apierrors.NewConflict(update.GetResource().GroupResource(), "", fmt.Errorf("conflict"))
			}
			return true, update.GetObject(), nil
		default:
			return false, nil, nil
		}
	}
	internalError := func(action core.Action) (bool, runtime.Object, error) {
		switch action.(type) {
		case core.UpdateActionImpl:
			return true, nil, apierrors.NewInternalError(fmt.Errorf("internal error"))
		default:
			return false, nil, nil
		}
	}

	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()

		informerFactory := informers.NewSharedInformerFactory(client, 0)
		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		store := &FakeStore{
			lister: informer.Lister(),
		}
		history := NewHistory(client, store)
		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		if test.reactor != nil {
			client.PrependReactor("*", "*", test.reactor)
		}
		updated, err := history.UpdateControllerRevision(test.revision, test.newRevision)
		if !test.err && err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		if !test.err && updated.Revision != test.newRevision {
			t.Errorf("%s: got %d want %d", test.name, updated.Revision, test.newRevision)
		}
		if test.err && err == nil {
			t.Errorf("%s: expected error", test.name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace

	tests := []testcase{
		{
			name:        "update succeeds",
			revision:    ss1Rev1,
			newRevision: ss1Rev1.Revision + 1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			reactor: nil,
			err:     false,
		},
		{
			name:        "update succeeds no noop",
			revision:    ss1Rev1,
			newRevision: ss1Rev1.Revision,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			reactor: nil,
			err:     false,
		},
		{
			name:        "update fails on error",
			revision:    ss1Rev1,
			newRevision: ss1Rev1.Revision + 10,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			reactor: internalError,
			err:     true,
		},
		{
			name:        "update on succeeds on conflict",
			revision:    ss1Rev1,
			newRevision: ss1Rev1.Revision + 1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			reactor: conflictSuccess,
			err:     false,
		},
	}
	for i := range tests {
		conflictAttempts = 0
		testFn(&tests[i], t)
	}
}

func TestFakeHistory_UpdateControllerRevision(t *testing.T) {
	type testcase struct {
		name        string
		revision    *appsv1.ControllerRevision
		newRevision int64
		existing    []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		err bool
	}

	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()

		informerFactory := informers.NewSharedInformerFactory(client, 0)
		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		history := NewFakeHistory(informer)
		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		updated, err := history.UpdateControllerRevision(test.revision, test.newRevision)
		if !test.err && err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		if !test.err && updated.Revision != test.newRevision {
			t.Errorf("%s: got %d want %d", test.name, updated.Revision, test.newRevision)
		}
		if test.err && err == nil {
			t.Errorf("%s: expected error", test.name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)

	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	tests := []testcase{
		{
			name:        "update succeeds",
			revision:    ss1Rev1,
			newRevision: ss1Rev1.Revision + 1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			err: false,
		},
		{
			name:        "update succeeds no noop",
			revision:    ss1Rev1,
			newRevision: ss1Rev1.Revision,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			err: false,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestRealHistory_DeleteControllerRevision(t *testing.T) {
	type testcase struct {
		name     string
		revision *appsv1.ControllerRevision
		existing []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		err bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)

		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		store := &FakeStore{
			lister: informer.Lister(),
		}
		history := NewHistory(client, store)
		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		err := history.DeleteControllerRevision(test.revision)
		if !test.err && err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		if test.err && err == nil {
			t.Errorf("%s: expected error", test.name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	ss2Rev2, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		2,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev2.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:     "delete empty fails",
			revision: ss1Rev1,
			existing: nil,
			err:      true,
		},
		{
			name:     "delete existing succeeds",
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			err: false,
		},
		{
			name:     "delete non-existing fails",
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss2,
					revision: ss2Rev1,
				},
				{
					parent:   ss2,
					revision: ss2Rev2,
				},
			},
			err: true,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestFakeHistory_DeleteControllerRevision(t *testing.T) {
	type testcase struct {
		name     string
		revision *appsv1.ControllerRevision
		existing []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		err bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)

		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		history := NewFakeHistory(informer)
		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		err := history.DeleteControllerRevision(test.revision)
		if !test.err && err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		if test.err && err == nil {
			t.Errorf("%s: expected error", test.name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	ss2Rev2, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		2,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev2.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:     "delete empty fails",
			revision: ss1Rev1,
			existing: nil,
			err:      true,
		},
		{
			name:     "delete existing succeeds",
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			err: false,
		},
		{
			name:     "delete non-existing fails",
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss2,
					revision: ss2Rev1,
				},
				{
					parent:   ss2,
					revision: ss2Rev2,
				},
			},
			err: true,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestFakeHistory_AdoptControllerRevision(t *testing.T) {
	type testcase struct {
		name       string
		parent     metav1.Object
		parentType *metav1.TypeMeta
		revision   *appsv1.ControllerRevision
		existing   []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		err bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()

		informerFactory := informers.NewSharedInformerFactory(client, 0)
		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)

		history := NewFakeHistory(informer)
		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		adopted, err := history.AdoptControllerRevision(test.parent, parentKind, test.revision)
		if !test.err && err != nil {
			t.Errorf("%s: %s", test.name, err)
		}
		if !test.err && !metav1.IsControlledBy(adopted, test.parent) {
			t.Errorf("%s: adoption failed", test.name)
		}
		if test.err && err == nil {
			t.Errorf("%s: expected error", test.name)
		}
	}

	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss1Rev2.OwnerReferences = []metav1.OwnerReference{}
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:       "adopting an orphan succeeds",
			parent:     ss1,
			parentType: &ss1.TypeMeta,
			revision:   ss1Rev2,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev2,
				},
			},
			err: false,
		},
		{
			name:       "adopting an owned revision fails",
			parent:     ss1,
			parentType: &ss1.TypeMeta,
			revision:   ss2Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss2,
					revision: ss2Rev1,
				},
			},
			err: true,
		},
		{
			name:       "adopting a non-existent revision fails",
			parent:     ss1,
			parentType: &ss1.TypeMeta,
			revision:   ss1Rev2,
			existing:   nil,
			err:        true,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestFakeHistory_ReleaseControllerRevision(t *testing.T) {
	type testcase struct {
		name     string
		parent   metav1.Object
		revision *appsv1.ControllerRevision
		existing []struct {
			parent   metav1.Object
			revision *appsv1.ControllerRevision
		}
		err bool
	}
	testFn := func(test *testcase, t *testing.T) {
		client := fake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(client, 0)
		stop := make(chan struct{})
		defer close(stop)
		informerFactory.Start(stop)
		informer := informerFactory.Apps().V1().ControllerRevisions()
		informerFactory.WaitForCacheSync(stop)
		history := NewFakeHistory(informer)
		var collisionCount int32
		for i := range test.existing {
			_, err := history.CreateControllerRevision(
				test.existing[i].parent,
				test.existing[i].revision,
				&collisionCount,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		adopted, err := history.ReleaseControllerRevision(test.parent, test.revision)
		if !test.err {
			if err != nil {
				t.Errorf("%s: %s", test.name, err)
			}
			if adopted == nil {
				return
			}
			if metav1.IsControlledBy(adopted, test.parent) {
				t.Errorf("%s: release failed", test.name)
			}
		}
		if test.err && err == nil {
			t.Errorf("%s: expected error", test.name)
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss1Rev2.OwnerReferences = []metav1.OwnerReference{}
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:     "releasing an owned revision succeeds",
			parent:   ss1,
			revision: ss1Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev1,
				},
			},
			err: false,
		},
		{
			name:     "releasing an orphan succeeds",
			parent:   ss1,
			revision: ss1Rev2,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss1,
					revision: ss1Rev2,
				},
			},
			err: false,
		},
		{
			name:     "releasing a revision owned by another controller succeeds",
			parent:   ss1,
			revision: ss2Rev1,
			existing: []struct {
				parent   metav1.Object
				revision *appsv1.ControllerRevision
			}{
				{
					parent:   ss2,
					revision: ss2Rev1,
				},
			},
			err: false,
		},
		{
			name:     "releasing a non-existent revision succeeds",
			parent:   ss1,
			revision: ss1Rev1,
			existing: nil,
			err:      false,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestFindEqualRevisions(t *testing.T) {
	type testcase struct {
		name      string
		revision  *appsv1.ControllerRevision
		revisions []*appsv1.ControllerRevision
		want      map[string]bool
	}
	testFn := func(test *testcase, t *testing.T) {
		found := FindEqualRevisions(test.revisions, test.revision)
		if len(found) != len(test.want) {
			t.Errorf("%s: want %d revisions found %d", test.name, len(test.want), len(found))
		}
		for i := range found {
			if !test.want[found[i].Name] {
				t.Errorf("%s: wanted %s not found", test.name, found[i].Name)
			}
		}
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss2 := newStatefulSet(3, "ss2", types.UID("ss2"), map[string]string{"goo": "car"})
	ss2.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev2.Namespace = ss1.Namespace
	ss1Rev2.OwnerReferences = []metav1.OwnerReference{}
	ss2Rev1, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		1,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev1.Namespace = ss2.Namespace
	ss2Rev2, err := NewControllerRevision(
		ss2,
		parentKind,
		ss2.Spec.Template.Labels,
		rawTemplate(&ss2.Spec.Template),
		2,
		ss2.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss2Rev2.Namespace = ss2.Namespace
	tests := []testcase{
		{
			name:      "finds equivalent",
			revision:  ss1Rev1,
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss2Rev1, ss2Rev2},
			want:      map[string]bool{ss1Rev1.Name: true},
		},
		{
			name:      "finds nothing when empty",
			revision:  ss1Rev1,
			revisions: nil,
			want:      map[string]bool{},
		},
		{
			name:      "finds nothing with no matches",
			revision:  ss1Rev1,
			revisions: []*appsv1.ControllerRevision{ss2Rev2, ss2Rev1},
			want:      map[string]bool{},
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func TestSortControllerRevisions(t *testing.T) {
	type testcase struct {
		name      string
		revisions []*appsv1.ControllerRevision
		want      []string
	}
	testFn := func(test *testcase, t *testing.T) {
		t.Run(test.name, func(t *testing.T) {
			SortControllerRevisions(test.revisions)
			for i := range test.revisions {
				if test.revisions[i].Name != test.want[i] {
					t.Errorf("%s: want %s at %d got %s", test.name, test.want[i], i, test.revisions[i].Name)
				}
			}
		})
	}
	ss1 := newStatefulSet(3, "ss1", types.UID("ss1"), map[string]string{"foo": "bar"})
	ss1.Status.CollisionCount = new(int32)
	ss1Rev1, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		1,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}

	ss1Rev1.Namespace = ss1.Namespace
	ss1Rev2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		2,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}

	ss1Rev2.Namespace = ss1.Namespace
	ss1Rev3, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		3,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}

	ss1Rev3Time2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		3,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev3Time2.Namespace = ss1.Namespace
	ss1Rev3Time2.CreationTimestamp = metav1.Time{Time: ss1Rev3.CreationTimestamp.Add(time.Second)}

	ss1Rev3Time2Name2, err := NewControllerRevision(
		ss1,
		parentKind,
		ss1.Spec.Template.Labels,
		rawTemplate(&ss1.Spec.Template),
		3,
		ss1.Status.CollisionCount,
	)
	if err != nil {
		t.Fatal(err)
	}
	ss1Rev3Time2Name2.Namespace = ss1.Namespace
	ss1Rev3Time2Name2.CreationTimestamp = metav1.Time{Time: ss1Rev3.CreationTimestamp.Add(time.Second)}

	tests := []testcase{
		{
			name:      "out of order",
			revisions: []*appsv1.ControllerRevision{ss1Rev2, ss1Rev1, ss1Rev3},
			want:      []string{ss1Rev1.Name, ss1Rev2.Name, ss1Rev3.Name},
		},
		{
			name:      "sorted",
			revisions: []*appsv1.ControllerRevision{ss1Rev1, ss1Rev2, ss1Rev3},
			want:      []string{ss1Rev1.Name, ss1Rev2.Name, ss1Rev3.Name},
		},
		{
			name:      "reversed",
			revisions: []*appsv1.ControllerRevision{ss1Rev3, ss1Rev2, ss1Rev1},
			want:      []string{ss1Rev1.Name, ss1Rev2.Name, ss1Rev3.Name},
		},
		{
			name:      "with ties",
			revisions: []*appsv1.ControllerRevision{ss1Rev3, ss1Rev3Time2, ss1Rev2, ss1Rev1},
			want:      []string{ss1Rev1.Name, ss1Rev2.Name, ss1Rev3.Name, ss1Rev3Time2.Name, ss1Rev3Time2Name2.Name},
		},
		{
			name:      "empty",
			revisions: nil,
			want:      nil,
		},
	}
	for i := range tests {
		testFn(&tests[i], t)
	}
}

func newStatefulSet(replicas int, name string, uid types.UID, labels map[string]string) *appsv1.StatefulSet {
	// Converting all the map-only selectors to set-based selectors.
	var testMatchExpressions []metav1.LabelSelectorRequirement
	for key, value := range labels {
		sel := metav1.LabelSelectorRequirement{
			Key:      key,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{value},
		}
		testMatchExpressions = append(testMatchExpressions, sel)
	}
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: corev1.NamespaceDefault,
			UID:       uid,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				// Purposely leaving MatchLabels nil, so to ensure it will break if any link
				// in the chain ignores the set-based MatchExpressions.
				MatchLabels:      nil,
				MatchExpressions: testMatchExpressions,
			},
			Replicas: func() *int32 { i := int32(replicas); return &i }(),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
							VolumeMounts: []corev1.VolumeMount{
								{Name: "datadir", MountPath: "/tmp/"},
								{Name: "home", MountPath: "/home"},
							},
						},
					},
					Volumes: []corev1.Volume{{
						Name: "home",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: fmt.Sprintf("/tmp/%v", "home"),
							},
						},
					}},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "datadir"},
					Spec: corev1.PersistentVolumeClaimSpec{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
							},
						},
					},
				},
			},
			ServiceName: "governingsvc",
		},
	}
}

var parentKind = appsv1.SchemeGroupVersion.WithKind("StatefulSet")

func rawTemplate(template *corev1.PodTemplateSpec) runtime.RawExtension {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(template); err != nil {
		panic(err)
	}
	return runtime.RawExtension{Raw: buf.Bytes()}
}
