//go:build exclude
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

package sync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/history"
)

// syncRevisions update current history revision number, or create current history if need to.
// It also deduplicates current history, and keeps quantity of history within historyLimit.
// It returns collision count, latest revision name, current revision name and possible error
func (s *SyncController) syncRevisions(ctx context.Context, fedResource FederatedResource) (*int32, string, string, error) {
	var (
		oldRevisions     []*appsv1.ControllerRevision
		currentRevisions []*appsv1.ControllerRevision
	)

	collisionCount := fedResource.CollisionCount()

	historyLimit := fedResource.RevisionHistoryLimit()
	if historyLimit == 0 {
		return collisionCount, "", "", nil
	}

	// create a new revision from the current fedResource, figure out the right revision number later
	updateRevision, err := newRevision(fedResource, 0, collisionCount)
	if err != nil {
		return collisionCount, "", "", err
	}

	revisions, err := s.listRevisions(fedResource)
	if err != nil {
		return collisionCount, "", "", err
	}
	for _, revision := range revisions {
		if history.EqualRevision(revision, updateRevision) {
			currentRevisions = append(currentRevisions, revision)
		} else {
			oldRevisions = append(oldRevisions, revision)
		}
	}

	keyedLogger := klog.FromContext(ctx)
	revisionNumber := maxRevision(oldRevisions) + 1
	switch len(currentRevisions) {
	case 0:
		// create a new revision if the current one isn't found
		if _, err := s.controllerHistory.CreateControllerRevision(fedResource.Object(), updateRevision, collisionCount); err != nil {
			return collisionCount, "", "", err
		}
	default:
		update, err := s.dedupCurRevisions(currentRevisions)
		if err != nil {
			return collisionCount, "", "", err
		}
		// Update revision number if necessary
		if update.Revision < revisionNumber {
			if _, err := s.controllerHistory.UpdateControllerRevision(update, revisionNumber); err != nil {
				return collisionCount, "", "", err
			}
		} else if update.Revision >= revisionNumber {
			labels := revisionLabelsWithOriginalLabel(fedResource)
			if err := s.updateRevisionLabel(ctx, update, labels); err != nil {
				return collisionCount, "", "", err
			}
		}
	}

	// maintain the revision history limit.
	if err := s.truncateRevisions(ctx, oldRevisions, historyLimit); err != nil {
		// it doesn't hurt much on failure
		keyedLogger.Error(err, "Failed to truncate revisionHistory for federated object")
	}
	// revisions are sorted after truncation, so the last one in oldRevisions should be the latest revision
	var lastRevisionNameWithHash string
	if len(oldRevisions) >= 1 && historyLimit >= 1 {
		lastRevisionNameWithHash = oldRevisions[len(oldRevisions)-1].Name
	}
	if lastRevisionNameWithHash != "" {
		podTemplateHash, err := getPodTemplateHash(fedResource)
		if err == nil {
			// suffix lastRevisionNameWithHash with podTemplateHash for check before rollback
			lastRevisionNameWithHash = lastRevisionNameWithHash + "|" + podTemplateHash
		}
	}

	for _, revision := range oldRevisions {
		if err := s.updateRevisionLabel(ctx, revision, revisionLabelsWithOriginalLabel(fedResource)); err != nil {
			return collisionCount, "", "", err
		}
	}
	return collisionCount, lastRevisionNameWithHash, updateRevision.Name, nil
}

// check if y is a subset of x
func IsLabelSubset(x, y map[string]string) bool {
	if y == nil || len(y) == 0 {
		return true
	}
	if x == nil || len(x) == 0 {
		return false
	}
	for k, v := range y {
		if x[k] != v {
			return false
		}
	}
	return true
}

func (s *SyncController) updateRevisionLabel(
	ctx context.Context,
	revision *appsv1.ControllerRevision,
	labels map[string]string,
) error {
	keyedLogger := klog.FromContext(ctx)
	if !IsLabelSubset(revision.GetLabels(), labels) {
		clone := revision.DeepCopy()
		for k, v := range labels {
			clone.Labels[k] = v
		}
		revisionNumber := clone.Revision
		// set revision to 0 to update forcely
		clone.Revision = 0
		if _, err := s.controllerHistory.UpdateControllerRevision(clone, revisionNumber); err != nil {
			if apierrors.IsNotFound(err) {
				keyedLogger.WithValues("controller-revision-name", revision.Name).
					Error(err, "Failed to update the revision")
			} else {
				return err
			}
		}
	}
	return nil
}

func (s *SyncController) truncateRevisions(ctx context.Context, revisions []*appsv1.ControllerRevision, limit int64) error {
	history.SortControllerRevisions(revisions)
	toKill := len(revisions) - int(limit)
	keyedLogger := klog.FromContext(ctx)
	for _, revision := range revisions {
		if toKill <= 0 {
			break
		}
		if err := s.controllerHistory.DeleteControllerRevision(revision); err != nil {
			if apierrors.IsNotFound(err) {
				keyedLogger.WithValues("controller-revision-name", revision.Name).
					Error(err, "Failed to delete the revision")
			} else {
				return err
			}
		}
		toKill--
	}
	return nil
}

func (s *SyncController) listRevisions(fedResource FederatedResource) ([]*appsv1.ControllerRevision, error) {
	selector := labels.SelectorFromSet(revisionLabels(fedResource))
	return s.controllerHistory.ListControllerRevisions(fedResource.Object(), selector)
}

// newRevision creates a new ControllerRevision containing a patch that reapplies the target state of federatedResource.
func newRevision(
	fedResource FederatedResource,
	revision int64,
	collisionCount *int32,
) (*appsv1.ControllerRevision, error) {
	patch, err := getPatch(fedResource)
	if err != nil {
		return nil, err
	}
	cr, err := history.NewControllerRevision(fedResource.Object(),
		fedResource.FederatedGVK(),
		revisionLabelsWithOriginalLabel(fedResource),
		runtime.RawExtension{Raw: patch},
		revision,
		collisionCount,
	)
	if err != nil {
		return nil, err
	}
	return cr, nil
}

// dedupCurRevisions deduplicates current revisions and returns the revision to keep
func (s *SyncController) dedupCurRevisions(
	dupRevisions []*appsv1.ControllerRevision,
) (*appsv1.ControllerRevision, error) {
	if len(dupRevisions) == 0 {
		return nil, fmt.Errorf("empty input for duplicate revisions")
	}

	keepCur := dupRevisions[0]
	maxRevision := dupRevisions[0].Revision
	for _, cur := range dupRevisions {
		if cur.Revision > maxRevision {
			keepCur = cur
			maxRevision = cur.Revision
		}
	}

	// Clean up duplicates
	for _, cur := range dupRevisions {
		if cur.Name == keepCur.Name {
			continue
		}
		if err := s.controllerHistory.DeleteControllerRevision(cur); err != nil {
			return nil, err
		}
	}
	return keepCur, nil
}

func maxRevision(revisions []*appsv1.ControllerRevision) int64 {
	max := int64(0)
	for _, revision := range revisions {
		if revision.Revision > max {
			max = revision.Revision
		}
	}
	return max
}

func getPatch(fedResource FederatedResource) ([]byte, error) {
	// Create a patch of the fedResource that replaces spec.template.spec.template
	template, ok, err := unstructured.NestedMap(fedResource.Object().Object, "spec", "template", "spec", "template")
	if err != nil {
		fedResource.RecordError(
			string(fedtypesv1a1.SyncRevisionsFailed),
			errors.Wrap(err, "Failed to get template.spec.template"),
		)
		return nil, err
	}
	if !ok {
		fedResource.RecordError(
			string(fedtypesv1a1.SyncRevisionsFailed),
			fmt.Errorf("failed to find template.spec.template"),
		)
		return nil, fmt.Errorf(
			"spec.template.spec.template is not found, fedResource: %+v",
			fedResource.Object().Object,
		)
	}

	patchObj := make(map[string]interface{})
	patchObj["op"] = "replace"
	patchObj["path"] = "/spec/template/spec/template"
	patchObj["value"] = template
	return json.Marshal([]interface{}{patchObj})
}

func getPodTemplateHash(fedResource FederatedResource) (string, error) {
	template, ok, err := unstructured.NestedMap(fedResource.Object().Object, "spec", "template", "spec", "template")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("spec.template.spec.template is not found, fedResource: %+v", fedResource.Object().Object)
	}
	return history.HashObject(template), nil
}

func revisionLabels(fedResource FederatedResource) map[string]string {
	return map[string]string{
		"uid": string(fedResource.Object().GetUID()),
	}
}

func revisionLabelsWithOriginalLabel(fedResource FederatedResource) map[string]string {
	ret := revisionLabels(fedResource)
	labels := fedResource.Object().GetLabels()
	for key, value := range labels {
		ret[key] = value
	}
	return ret
}
