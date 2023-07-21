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

package sync

import (
	//nolint:gosec
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/dispatch"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/version"
	"github.com/kubewharf/kubeadmiral/pkg/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
	overridesutil "github.com/kubewharf/kubeadmiral/pkg/util/overrides"
)

// FederatedResource encapsulates the behavior of a logical federated
// resource which may be implemented by one or more kubernetes
// resources in the cluster hosting the KubeFed control plane.
type FederatedResource interface {
	dispatch.FederatedResourceForDispatch

	FederatedName() common.QualifiedName
	UpdateVersions(selectedClusters []string, versionMap map[string]string) error
	DeleteVersions()
	ComputePlacement(clusters []*fedcorev1a1.FederatedCluster) sets.Set[string]
	SetObject(obj fedcorev1a1.GenericFederatedObject)
}

var _ FederatedResource = &federatedResource{}

type federatedResource struct {
	sync.RWMutex

	typeConfig      *fedcorev1a1.FederatedTypeConfig
	federatedName   common.QualifiedName
	targetName      common.QualifiedName
	federatedObject fedcorev1a1.GenericFederatedObject
	template        *unstructured.Unstructured
	versionManager  *version.VersionManager
	overridesMap    overridesutil.OverridesMap
	versionMap      map[string]string
	eventRecorder   record.EventRecorder
}

func (r *federatedResource) FederatedName() common.QualifiedName {
	return r.federatedName
}

func (r *federatedResource) TargetName() common.QualifiedName {
	return r.targetName
}

func (r *federatedResource) TargetGVK() schema.GroupVersionKind {
	return r.typeConfig.GetSourceTypeGVK()
}

func (r *federatedResource) TargetGVR() schema.GroupVersionResource {
	return r.typeConfig.GetSourceTypeGVR()
}

func (r *federatedResource) FederatedGVK() schema.GroupVersionKind {
	// NOTE: remember to update this method when we switch to a different apiVersion.
	return fedcorev1a1.SchemeGroupVersion.WithKind(reflect.TypeOf(r.federatedObject).Elem().Name())
}

func (r *federatedResource) TypeConfig() *fedcorev1a1.FederatedTypeConfig {
	return r.typeConfig
}

func (r *federatedResource) Object() fedcorev1a1.GenericFederatedObject {
	return r.federatedObject
}

func (r *federatedResource) SetObject(obj fedcorev1a1.GenericFederatedObject) {
	r.federatedObject = obj
}

func (r *federatedResource) TemplateVersion() (string, error) {
	if hash, err := hashUnstructured(r.template); err != nil {
		return "", fmt.Errorf("failed to hash template: %w", err)
	} else {
		return hash, nil
	}
}

func (r *federatedResource) OverrideVersion() (string, error) {
	// TODO Consider hashing overrides per cluster to minimize
	// unnecessary updates.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"overrides": r.federatedObject.GetSpec().Overrides,
		},
	}

	if hash, err := hashUnstructured(obj); err != nil {
		return "", fmt.Errorf("failed to hash overrides: %w", err)
	} else {
		return hash, nil
	}
}

func (r *federatedResource) VersionForCluster(clusterName string) (string, error) {
	r.Lock()
	defer r.Unlock()
	if r.versionMap == nil {
		var err error
		r.versionMap, err = r.versionManager.Get(r)
		if err != nil {
			return "", err
		}
	}
	return r.versionMap[clusterName], nil
}

func (r *federatedResource) UpdateVersions(selectedClusters []string, versionMap map[string]string) error {
	return r.versionManager.Update(r, selectedClusters, versionMap)
}

func (r *federatedResource) DeleteVersions() {
	r.versionManager.Delete(r.federatedName)
}

func (r *federatedResource) ComputePlacement(clusters []*fedcorev1a1.FederatedCluster) sets.Set[string] {
	return computePlacement(r.federatedObject, clusters)
}

func (r *federatedResource) ObjectForCluster(clusterName string) (*unstructured.Unstructured, error) {
	obj := r.template.DeepCopy()

	switch r.TargetGVK() {
	case common.JobGVK:
		if err := dropJobFields(obj); err != nil {
			return nil, err
		}

		if err := addRetainObjectFinalizer(obj); err != nil {
			return nil, err
		}
	case common.ServiceGVK:
		if err := dropServiceFields(obj); err != nil {
			return nil, err
		}
	case common.PodGVK:
		if err := dropPodFields(obj); err != nil {
			return nil, err
		}

		if err := addRetainObjectFinalizer(obj); err != nil {
			return nil, err
		}
	}

	return obj, nil
}

func addRetainObjectFinalizer(obj *unstructured.Unstructured) error {
	if _, err := finalizers.AddFinalizers(obj, sets.NewString(dispatch.RetainTerminatingObjectFinalizer)); err != nil {
		return err
	}
	return nil
}

func dropJobFields(obj *unstructured.Unstructured) error {
	// no need to do anything if manualSelector is true
	if manualSelector, exists, err := unstructured.NestedBool(obj.Object, "spec", "manualSelector"); err == nil &&
		exists && manualSelector {
		return nil
	}

	// otherwise, drop the selector and "controller-uid" label in the template
	unstructured.RemoveNestedField(obj.Object, "spec", "template", "metadata", "labels", "controller-uid")
	unstructured.RemoveNestedField(obj.Object, "spec", "selector", "matchLabels", "controller-uid")

	return nil
}

func dropServiceFields(obj *unstructured.Unstructured) error {
	if clusterIP, exists, err := unstructured.NestedString(obj.Object, "spec", "clusterIP"); err == nil && exists {
		if clusterIP != corev1.ClusterIPNone {
			unstructured.RemoveNestedField(obj.Object, "spec", "clusterIP")
			unstructured.RemoveNestedField(obj.Object, "spec", "clusterIPs")
		}
	}

	return nil
}

func dropPodFields(obj *unstructured.Unstructured) error {
	// A general guideline is to always drop and retain fields that are unable to be set by the user and are managed by
	// the Kubernetes control plane instead. ephemeralContainers falls into this category.
	unstructured.RemoveNestedField(obj.Object, "spec", "ephemeralContainers")
	return nil
}

// ApplyOverrides applies overrides for the named cluster to the given
// object. The managed label is added afterwards to ensure labeling even if an
// override was attempted.
func (r *federatedResource) ApplyOverrides(
	obj *unstructured.Unstructured,
	clusterName string,
) error {
	overrides, err := r.overridesForCluster(clusterName)
	if err != nil {
		return err
	}
	if overrides != nil {
		if err := overridesutil.ApplyJsonPatch(obj, overrides); err != nil {
			return err
		}
	}

	// Ensure that resources managed by KubeFed always have the
	// managed label.  The label is intended to be targeted by all the
	// KubeFed controllers.
	managedlabel.AddManagedLabel(obj)

	return nil
}

// TODO Use an enumeration for errorCode.
func (r *federatedResource) RecordError(errorCode string, err error) {
	r.eventRecorder.Eventf(r.Object(), corev1.EventTypeWarning, errorCode, err.Error())
}

func (r *federatedResource) RecordEvent(reason, messageFmt string, args ...interface{}) {
	r.eventRecorder.Eventf(r.Object(), corev1.EventTypeNormal, reason, messageFmt, args...)
}

func (r *federatedResource) overridesForCluster(clusterName string) (fedcorev1a1.OverridePatches, error) {
	r.Lock()
	defer r.Unlock()
	if r.overridesMap == nil {
		overrides := make([]fedcorev1a1.OverrideWithController, 0, len(r.federatedObject.GetSpec().Overrides))
		for _, o := range r.federatedObject.GetSpec().Overrides {
			overrides = append(overrides, *o.DeepCopy())
		}

		// Order overrides based on the controller name specified in the FTC
		// Put overrides from unknown sources at the end, but preserve their relative orders
		controllerOrder := make(map[string]int)
		for _, controllerGroup := range r.typeConfig.GetControllers() {
			for _, controller := range controllerGroup {
				controllerOrder[controller] = len(controllerOrder)
			}
		}

		sort.SliceStable(overrides, func(i, j int) bool {
			lhs, isKnown := controllerOrder[overrides[i].Controller]
			if !isKnown {
				// lhs is unknown
				// if rhs is known, return false so rhs can precede lhs
				// if rhs is unknown, return false to preserve the relative order
				return false
			}
			rhs, isKnown := controllerOrder[overrides[j].Controller]
			if !isKnown {
				// lhs controller is known and rhs controller is unknown
				// lhs should precede rhs
				return true
			}
			return lhs < rhs
		})

		r.overridesMap = make(overridesutil.OverridesMap)

		// Merge overrides in the specified order
		for _, controllerOverride := range overrides {
			for _, clusterOverride := range controllerOverride.Override {
				r.overridesMap[clusterOverride.Cluster] = append(
					r.overridesMap[clusterOverride.Cluster], clusterOverride.Patches...,
				)
			}
		}
	}
	return r.overridesMap[clusterName], nil
}

// TODO Investigate alternate ways of computing the hash of a field map.
func hashUnstructured(obj *unstructured.Unstructured) (string, error) {
	jsonBytes, err := obj.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("failed to marshal to json: %w", err)
	}
	//nolint:gosec
	hash := md5.New()
	if _, err := hash.Write(jsonBytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
