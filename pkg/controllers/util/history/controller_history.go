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
	"hash/fnv"
	"sort"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	appsv1informers "k8s.io/client-go/informers/apps/v1"
	clientset "k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	hashutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/hash"
)

// ControllerRevisionHashLabel is the label used to indicate the hash value of a ControllerRevision's Data.
const ControllerRevisionHashLabel = "controller.kubernetes.io/hash"

// ControllerRevisionName returns the Name for a ControllerRevision in the form prefix-hash. If the length
// of prefix is greater than 223 bytes, it is truncated to allow for a name that is no larger than 253 bytes.
func ControllerRevisionName(prefix string, hash string) string {
	if len(prefix) > 223 {
		prefix = prefix[:223]
	}

	return fmt.Sprintf("%s-%s", prefix, hash)
}

// NewControllerRevision returns a ControllerRevision with a ControllerRef pointing to parent and indicating that
// parent is of parentKind. The ControllerRevision has labels matching template labels, contains Data equal to data, and
// has a Revision equal to revision. The collisionCount is used when creating the name of the ControllerRevision
// so the name is likely unique. If the returned error is nil, the returned ControllerRevision is valid. If the
// returned error is not nil, the returned ControllerRevision is invalid for use.
func NewControllerRevision(parent metav1.Object,
	parentKind schema.GroupVersionKind,
	templateLabels map[string]string,
	data runtime.RawExtension,
	revision int64,
	collisionCount *int32,
) (*appsv1.ControllerRevision, error) {
	labelMap := make(map[string]string)
	for k, v := range templateLabels {
		labelMap[k] = v
	}
	cr := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Labels:          labelMap,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(parent, parentKind)},
		},
		Data:     data,
		Revision: revision,
	}
	hash := HashControllerRevision(cr, collisionCount)
	cr.Name = ControllerRevisionName(parent.GetName(), hash)
	cr.Labels[ControllerRevisionHashLabel] = hash
	return cr, nil
}

// HashControllerRevision hashes the contents of revision's Data using FNV hashing. If probe is not nil, the byte value
// of probe is added written to the hash as well. The returned hash will be a safe encoded string to avoid bad words.
func HashControllerRevision(revision *appsv1.ControllerRevision, probe *int32) string {
	hf := fnv.New32()
	if len(revision.Data.Raw) > 0 {
		hf.Write(revision.Data.Raw)
	}
	if revision.Data.Object != nil {
		hashutil.DeepHashObject(hf, revision.Data.Object)
	}
	if probe != nil {
		hf.Write([]byte(strconv.FormatInt(int64(*probe), 10)))
	}
	return rand.SafeEncodeString(fmt.Sprint(hf.Sum32()))
}

func HashObject(object interface{}) string {
	hf := fnv.New32()
	hashutil.DeepHashObject(hf, object)
	return rand.SafeEncodeString(fmt.Sprint(hf.Sum32()))
}

// SortControllerRevisions sorts revisions by their Revision.
func SortControllerRevisions(revisions []*appsv1.ControllerRevision) {
	sort.Stable(byRevision(revisions))
}

// EqualRevision returns true if lhs and rhs are either both nil, or both point to non-nil ControllerRevisions that
// contain semantically equivalent data. Otherwise this method returns false.
func EqualRevision(lhs *appsv1.ControllerRevision, rhs *appsv1.ControllerRevision) bool {
	var lhsHash, rhsHash *uint32
	if lhs == nil || rhs == nil {
		return lhs == rhs
	}
	if hs, found := lhs.Labels[ControllerRevisionHashLabel]; found {
		hash, err := strconv.ParseInt(hs, 10, 32)
		if err == nil {
			lhsHash = new(uint32)
			*lhsHash = uint32(hash)
		}
	}
	if hs, found := rhs.Labels[ControllerRevisionHashLabel]; found {
		hash, err := strconv.ParseInt(hs, 10, 32)
		if err == nil {
			rhsHash = new(uint32)
			*rhsHash = uint32(hash)
		}
	}
	if lhsHash != nil && rhsHash != nil && *lhsHash != *rhsHash {
		return false
	}
	return bytes.Equal(lhs.Data.Raw, rhs.Data.Raw) && apiequality.Semantic.DeepEqual(lhs.Data.Object, rhs.Data.Object)
}

// FindEqualRevisions returns all ControllerRevisions in revisions that are equal to needle using EqualRevision as the
// equality test. The returned slice preserves the order of revisions.
func FindEqualRevisions(
	revisions []*appsv1.ControllerRevision,
	needle *appsv1.ControllerRevision,
) []*appsv1.ControllerRevision {
	var eq []*appsv1.ControllerRevision
	for i := range revisions {
		if EqualRevision(revisions[i], needle) {
			eq = append(eq, revisions[i])
		}
	}
	return eq
}

// byRevision implements sort.Interface to allow ControllerRevisions to be sorted by Revision.
type byRevision []*appsv1.ControllerRevision

func (br byRevision) Len() int {
	return len(br)
}

// Less breaks ties first by creation timestamp, then by name
func (br byRevision) Less(i, j int) bool {
	if br[i].Revision == br[j].Revision {
		if br[j].CreationTimestamp.Equal(&br[i].CreationTimestamp) {
			return br[i].Name < br[j].Name
		}
		return br[j].CreationTimestamp.After(br[i].CreationTimestamp.Time)
	}
	return br[i].Revision < br[j].Revision
}

func (br byRevision) Swap(i, j int) {
	br[i], br[j] = br[j], br[i]
}

// Interface provides an interface allowing for management of a Controller's history as realized by recorded
// ControllerRevisions. An instance of Interface can be retrieved from NewHistory. Implementations must treat all
// pointer parameters as "in" parameter, and they must not be mutated.
type Interface interface {
	// ListControllerRevisions lists all ControllerRevisions matching selector and owned by parent or no other
	// controller. If the returned error is nil the returned slice of ControllerRevisions is valid. If the
	// returned error is not nil, the returned slice is not valid.
	ListControllerRevisions(parent metav1.Object, selector labels.Selector) ([]*appsv1.ControllerRevision, error)
	// CreateControllerRevision attempts to create the revision as owned by parent via a ControllerRef. If name
	// collision occurs, collisionCount (incremented each time collision occurs except for the first time) is
	// added to the hash of the revision and it is renamed using ControllerRevisionName. Implementations may
	// cease to attempt to retry creation after some number of attempts and return an error. If the returned
	// error is not nil, creation failed. If the returned error is nil, the returned ControllerRevision has been
	// created.
	// Callers must make sure that collisionCount is not nil. An error is returned if it is.
	CreateControllerRevision(
		parent metav1.Object,
		revision *appsv1.ControllerRevision,
		collisionCount *int32,
	) (*appsv1.ControllerRevision, error)
	// DeleteControllerRevision attempts to delete revision. If the returned error is not nil, deletion has failed.
	DeleteControllerRevision(revision *appsv1.ControllerRevision) error
	// UpdateControllerRevision updates revision such that its Revision is equal to newRevision. Implementations
	// may retry on conflict. If the returned error is nil, the update was successful and returned ControllerRevision
	// is valid. If the returned error is not nil, the update failed and the returned ControllerRevision is invalid.
	UpdateControllerRevision(revision *appsv1.ControllerRevision, newRevision int64) (*appsv1.ControllerRevision, error)
	// AdoptControllerRevision attempts to adopt revision by adding a ControllerRef indicating that the parent
	// Object of parentKind is the owner of revision. If revision is already owned, an error is returned. If the
	// resource patch fails, an error is returned. If no error is returned, the returned ControllerRevision is
	// valid.
	AdoptControllerRevision(
		parent metav1.Object,
		parentKind schema.GroupVersionKind,
		revision *appsv1.ControllerRevision,
	) (*appsv1.ControllerRevision, error)
	// ReleaseControllerRevision attempts to release parent's ownership of revision by removing parent from the
	// OwnerReferences of revision. If an error is returned, parent remains the owner of revision. If no error is
	// returned, the returned ControllerRevision is valid.
	ReleaseControllerRevision(
		parent metav1.Object,
		revision *appsv1.ControllerRevision,
	) (*appsv1.ControllerRevision, error)
}

// NewHistory returns an instance of Interface that uses client to communicate with the API Server and lister to list
// ControllerRevisions. This method should be used to create an Interface for all scenarios other than testing.
func NewHistory(client clientset.Interface, store cache.Store) Interface {
	return &realHistory{client, store}
}

// NewFakeHistory returns an instance of Interface that uses informer to create, update, list, and delete
// ControllerRevisions. This method should be used to create an Interface for testing purposes.
func NewFakeHistory(informer appsv1informers.ControllerRevisionInformer) Interface {
	return &fakeHistory{informer.Informer().GetIndexer(), informer.Lister()}
}

type realHistory struct {
	client clientset.Interface
	store  cache.Store
}

func (rh *realHistory) ListControllerRevisions(
	parent metav1.Object,
	selector labels.Selector,
) ([]*appsv1.ControllerRevision, error) {
	// List all revisions in the namespace that match the selector
	history := make([]*appsv1.ControllerRevision, 0)
	appendFn := func(m interface{}) {
		utd, ok := m.(*unstructured.Unstructured)
		if !ok {
			klog.Errorf("Failed to convert %v", m)
			return
		}
		controllerRevision := &appsv1.ControllerRevision{}
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(utd.Object, controllerRevision)
		if err != nil {
			klog.Errorf("Failed to convert unstructured to controllerRevision.")
			return
		}

		if controllerRevision.Namespace == parent.GetNamespace() {
			history = append(history, controllerRevision)
		}
	}
	cache.ListAll(rh.store, selector, appendFn)

	var owned []*appsv1.ControllerRevision
	for i := range history {
		ref := metav1.GetControllerOf(history[i])
		if ref == nil || ref.UID == parent.GetUID() {
			owned = append(owned, history[i])
		}
	}
	return owned, nil
}

func (rh *realHistory) CreateControllerRevision(
	parent metav1.Object,
	revision *appsv1.ControllerRevision,
	collisionCount *int32,
) (*appsv1.ControllerRevision, error) {
	if collisionCount == nil {
		return nil, fmt.Errorf("collisionCount should not be nil")
	}

	// Clone the input
	clone := revision.DeepCopy()

	// Continue to attempt to create the revision updating the name with a new hash on each iteration
	for {
		hash := HashControllerRevision(revision, collisionCount)
		// Update the revisions name
		clone.Name = ControllerRevisionName(parent.GetName(), hash)
		ns := parent.GetNamespace()
		created, err := rh.client.AppsV1().ControllerRevisions(ns).Create(context.TODO(), clone, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			exists, err := rh.client.AppsV1().
				ControllerRevisions(ns).
				Get(context.TODO(), clone.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			if bytes.Equal(exists.Data.Raw, clone.Data.Raw) {
				return exists, nil
			}
			*collisionCount++
			continue
		}
		return created, err
	}
}

func (rh *realHistory) UpdateControllerRevision(
	revision *appsv1.ControllerRevision,
	newRevision int64,
) (*appsv1.ControllerRevision, error) {
	clone := revision.DeepCopy()
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if clone.Revision == newRevision {
			return nil
		}
		clone.Revision = newRevision
		updated, updateErr := rh.client.AppsV1().
			ControllerRevisions(clone.Namespace).
			Update(context.TODO(), clone, metav1.UpdateOptions{})
		if updateErr == nil {
			return nil
		}
		klog.Errorf("Failed to update revision %s with newRevision %d: %v", clone.Name, newRevision, updateErr)
		updated, getErr := rh.client.AppsV1().
			ControllerRevisions(clone.Namespace).
			Get(context.TODO(), clone.Name, metav1.GetOptions{ResourceVersion: "0"})
		if getErr != nil {
			return getErr
		}
		klog.V(4).Infof("Get latest revision %s: %d", updated.Name, updated.Revision)
		updated.Data = clone.Data
		clone = updated
		//
		//if updated != nil {
		//	clone = updated
		//}
		//
		//key, getErr := cache.MetaNamespaceKeyFunc(clone)
		//if getErr != nil {
		//	return getErr
		//}
		//
		//updatedObj, exists, getErr := rh.store.GetByKey(key)
		//if exists && getErr != nil {
		//	utd, ok := updatedObj.(*unstructured.Unstructured)
		//	if !ok {
		//		klog.Errorf("Failed to convert %v", updatedObj)
		//		return fmt.Errorf("Failed to convert %v", updatedObj)
		//	}
		//	updatedRevisionController := &apps.ControllerRevision{}
		//	convertErr := runtime.DefaultUnstructuredConverter.FromUnstructured(utd.Object, updatedRevisionController)
		//	if convertErr != nil {
		//		klog.Errorf("Failed to convert unstructured to controllerRevision.")
		//		return convertErr
		//	}
		//	clone = updatedRevisionController.DeepCopy()
		//}
		return updateErr
	})
	return clone, err
}

func (rh *realHistory) DeleteControllerRevision(revision *appsv1.ControllerRevision) error {
	return rh.client.AppsV1().
		ControllerRevisions(revision.Namespace).
		Delete(context.TODO(), revision.Name, metav1.DeleteOptions{})
}

type objectForPatch struct {
	Metadata objectMetaForPatch `json:"metadata"`
}

// objectMetaForPatch define object meta struct for patch operation
type objectMetaForPatch struct {
	OwnerReferences []metav1.OwnerReference `json:"ownerReferences"`
	UID             types.UID               `json:"uid"`
}

func (rh *realHistory) AdoptControllerRevision(
	parent metav1.Object,
	parentKind schema.GroupVersionKind,
	revision *appsv1.ControllerRevision,
) (*appsv1.ControllerRevision, error) {
	blockOwnerDeletion := true
	isController := true
	// Return an error if the parent does not own the revision
	if owner := metav1.GetControllerOf(revision); owner != nil {
		return nil, fmt.Errorf("attempt to adopt revision owned by %v", owner)
	}
	addControllerPatch := objectForPatch{
		Metadata: objectMetaForPatch{
			UID: revision.UID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         parentKind.GroupVersion().String(),
				Kind:               parentKind.Kind,
				Name:               parent.GetName(),
				UID:                parent.GetUID(),
				Controller:         &isController,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
	}
	patchBytes, err := json.Marshal(&addControllerPatch)
	if err != nil {
		return nil, err
	}
	// Use strategic merge patch to add an owner reference indicating a controller ref
	return rh.client.AppsV1().ControllerRevisions(parent.GetNamespace()).Patch(context.TODO(), revision.GetName(),
		types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}

func (rh *realHistory) ReleaseControllerRevision(
	parent metav1.Object,
	revision *appsv1.ControllerRevision,
) (*appsv1.ControllerRevision, error) {
	// Use strategic merge patch to add an owner reference indicating a controller ref
	released, err := rh.client.AppsV1().
		ControllerRevisions(revision.GetNamespace()).
		Patch(context.TODO(), revision.GetName(),
			types.StrategicMergePatchType,
			[]byte(
				fmt.Sprintf(
					`{"metadata":{"ownerReferences":[{"$patch":"delete","uid":"%s"}],"uid":"%s"}}`,
					parent.GetUID(),
					revision.UID,
				),
			),
			metav1.PatchOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// We ignore deleted revisions
			return nil, nil
		}
		if apierrors.IsInvalid(err) {
			// We ignore cases where the parent no longer owns the revision or where the revision has no
			// owner.
			return nil, nil
		}
	}
	return released, err
}

type fakeHistory struct {
	indexer cache.Indexer
	lister  appsv1listers.ControllerRevisionLister
}

func (fh *fakeHistory) ListControllerRevisions(
	parent metav1.Object,
	selector labels.Selector,
) ([]*appsv1.ControllerRevision, error) {
	history, err := fh.lister.ControllerRevisions(parent.GetNamespace()).List(selector)
	if err != nil {
		return nil, err
	}

	var owned []*appsv1.ControllerRevision
	for i := range history {
		ref := metav1.GetControllerOf(history[i])
		if ref == nil || ref.UID == parent.GetUID() {
			owned = append(owned, history[i])
		}

	}
	return owned, err
}

func (fh *fakeHistory) addRevision(revision *appsv1.ControllerRevision) (*appsv1.ControllerRevision, error) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(revision)
	if err != nil {
		return nil, err
	}
	obj, found, err := fh.indexer.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if found {
		foundRevision := obj.(*appsv1.ControllerRevision)
		return foundRevision, apierrors.NewAlreadyExists(appsv1.Resource("controllerrevision"), revision.Name)
	}
	return revision, fh.indexer.Update(revision)
}

func (fh *fakeHistory) CreateControllerRevision(
	parent metav1.Object,
	revision *appsv1.ControllerRevision,
	collisionCount *int32,
) (*appsv1.ControllerRevision, error) {
	if collisionCount == nil {
		return nil, fmt.Errorf("collisionCount should not be nil")
	}

	// Clone the input
	clone := revision.DeepCopy()
	clone.Namespace = parent.GetNamespace()

	// Continue to attempt to create the revision updating the name with a new hash on each iteration
	for {
		hash := HashControllerRevision(revision, collisionCount)
		// Update the revisions name and labels
		clone.Name = ControllerRevisionName(parent.GetName(), hash)
		created, err := fh.addRevision(clone)
		if apierrors.IsAlreadyExists(err) {
			*collisionCount++
			continue
		}
		return created, err
	}
}

func (fh *fakeHistory) DeleteControllerRevision(revision *appsv1.ControllerRevision) error {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(revision)
	if err != nil {
		return err
	}
	obj, found, err := fh.indexer.GetByKey(key)
	if err != nil {
		return err
	}
	if !found {
		return apierrors.NewNotFound(appsv1.Resource("controllerrevisions"), revision.Name)
	}
	return fh.indexer.Delete(obj)
}

func (fh *fakeHistory) UpdateControllerRevision(
	revision *appsv1.ControllerRevision,
	newRevision int64,
) (*appsv1.ControllerRevision, error) {
	clone := revision.DeepCopy()
	clone.Revision = newRevision
	return clone, fh.indexer.Update(clone)
}

func (fh *fakeHistory) AdoptControllerRevision(
	parent metav1.Object,
	parentKind schema.GroupVersionKind,
	revision *appsv1.ControllerRevision,
) (*appsv1.ControllerRevision, error) {
	if owner := metav1.GetControllerOf(revision); owner != nil {
		return nil, fmt.Errorf("attempt to adopt revision owned by %v", owner)
	}
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(revision)
	if err != nil {
		return nil, err
	}
	_, found, err := fh.indexer.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, apierrors.NewNotFound(appsv1.Resource("controllerrevisions"), revision.Name)
	}
	clone := revision.DeepCopy()
	clone.OwnerReferences = append(clone.OwnerReferences, *metav1.NewControllerRef(parent, parentKind))
	return clone, fh.indexer.Update(clone)
}

func (fh *fakeHistory) ReleaseControllerRevision(
	parent metav1.Object,
	revision *appsv1.ControllerRevision,
) (*appsv1.ControllerRevision, error) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(revision)
	if err != nil {
		return nil, err
	}
	_, found, err := fh.indexer.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	clone := revision.DeepCopy()
	refs := clone.OwnerReferences
	clone.OwnerReferences = nil
	for i := range refs {
		if refs[i].UID != parent.GetUID() {
			clone.OwnerReferences = append(clone.OwnerReferences, refs[i])
		}
	}
	return clone, fh.indexer.Update(clone)
}
