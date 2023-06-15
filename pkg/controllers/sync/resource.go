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
	"sort"
	"strings"
	"sync"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/dispatch"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/version"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/managedlabel"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/controllers/util/unstructured"
)

// FederatedResource encapsulates the behavior of a logical federated
// resource which may be implemented by one or more kubernetes
// resources in the cluster hosting the KubeFed control plane.
type FederatedResource interface {
	dispatch.FederatedResourceForDispatch

	FederatedName() common.QualifiedName
	FederatedKind() string
	FederatedGVK() schema.GroupVersionKind
	CollisionCount() *int32
	RevisionHistoryLimit() int64
	UpdateVersions(selectedClusters []string, versionMap map[string]string) error
	DeleteVersions()
	ComputePlacement(clusters []*fedcorev1a1.FederatedCluster) (selectedClusters sets.String, err error)
	NamespaceNotFederated() bool
}

type federatedResource struct {
	sync.RWMutex

	limitedScope      bool
	typeConfig        *fedcorev1a1.FederatedTypeConfig
	targetName        common.QualifiedName
	federatedKind     string
	federatedName     common.QualifiedName
	federatedResource *unstructured.Unstructured
	versionManager    *version.VersionManager
	overridesMap      util.OverridesMap
	versionMap        map[string]string
	fedNamespace      *unstructured.Unstructured
	eventRecorder     record.EventRecorder
}

func (r *federatedResource) FederatedName() common.QualifiedName {
	return r.federatedName
}

func (r *federatedResource) FederatedKind() string {
	return r.typeConfig.GetFederatedType().Kind
}

func (r *federatedResource) FederatedGVK() schema.GroupVersionKind {
	apiResource := r.typeConfig.GetFederatedType()
	return schemautil.APIResourceToGVK(&apiResource)
}

func (r *federatedResource) TargetName() common.QualifiedName {
	return r.targetName
}

func (r *federatedResource) TargetKind() string {
	return r.typeConfig.GetTargetType().Kind
}

func (r *federatedResource) TargetGVK() schema.GroupVersionKind {
	apiResource := r.typeConfig.GetTargetType()
	return schemautil.APIResourceToGVK(&apiResource)
}

func (r *federatedResource) TypeConfig() *fedcorev1a1.FederatedTypeConfig {
	return r.typeConfig
}

func (r *federatedResource) Replicas() (*int64, error) {
	return utilunstructured.GetInt64FromPath(
		r.federatedResource,
		r.typeConfig.Spec.PathDefinition.ReplicasSpec,
		common.TemplatePath,
	)
}

func (r *federatedResource) Object() *unstructured.Unstructured {
	return r.federatedResource
}

func (r *federatedResource) CollisionCount() *int32 {
	val, _, _ := unstructured.NestedInt64(r.Object().Object, "status", "collisionCount")
	v := int32(val)
	return &v
}

func (r *federatedResource) RevisionHistoryLimit() int64 {
	val, _, _ := unstructured.NestedInt64(r.Object().Object, "spec", "revisionHistoryLimit")
	return val
}

func (r *federatedResource) TemplateVersion() (string, error) {
	obj := r.federatedResource
	return GetTemplateHash(obj.Object)
}

func (r *federatedResource) OverrideVersion() (string, error) {
	// TODO Consider hashing overrides per cluster to minimize
	// unnecessary updates.
	return GetOverrideHash(r.federatedResource)
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

func (r *federatedResource) ComputePlacement(clusters []*fedcorev1a1.FederatedCluster) (sets.String, error) {
	if r.typeConfig.GetNamespaced() {
		return computeNamespacedPlacement(r.federatedResource, r.fedNamespace, clusters, r.limitedScope)
	}
	return computePlacement(r.federatedResource, clusters)
}

func (r *federatedResource) NamespaceNotFederated() bool {
	return r.typeConfig.GetNamespaced() && r.fedNamespace == nil
}

// TODO Marshall the template once per reconcile, not per-cluster
func (r *federatedResource) ObjectForCluster(clusterName string) (*unstructured.Unstructured, error) {
	templateBody, ok, err := unstructured.NestedMap(r.federatedResource.Object, common.SpecField, common.TemplateField)
	if err != nil {
		return nil, errors.Wrap(err, "Error retrieving template body")
	}
	if !ok {
		// Some resources (like namespaces) can be created from an
		// empty template.
		templateBody = make(map[string]interface{})
	}
	obj := &unstructured.Unstructured{Object: templateBody}

	notSupportedTemplate := "metadata.%s cannot be set via template to avoid conflicting with controllers " +
		"in member clusters. Consider using an override to add or remove elements from this collection."
	if len(obj.GetFinalizers()) > 0 {
		r.RecordError("FinalizersNotSupported", errors.Errorf(notSupportedTemplate, "finalizers"))
		obj.SetFinalizers(nil)
	}

	// Avoid having to duplicate these details in the template or have
	// the name/namespace vary between host and member clusters.
	// TODO: consider omitting these fields in the template created by federate controller
	obj.SetName(r.federatedResource.GetName())
	namespace := util.NamespaceForCluster(clusterName, r.federatedResource.GetNamespace())
	obj.SetNamespace(namespace)
	targetAPIResource := r.typeConfig.GetTargetType()
	obj.SetKind(targetAPIResource.Kind)

	// deprecated: federated generation is currently unused and might increase the dispatcher load
	// if _, err = annotationutil.AddAnnotation(
	//	  obj, common.FederatedGenerationAnnotation, fmt.Sprintf("%d", r.Object().GetGeneration())); err != nil {
	//    return nil, err
	// }

	if _, err = annotationutil.AddAnnotation(obj, common.SourceGenerationAnnotation, fmt.Sprintf("%d", obj.GetGeneration())); err != nil {
		return nil, err
	}

	// If the template does not specify an api version, default it to
	// the one configured for the target type in the FTC.
	if len(obj.GetAPIVersion()) == 0 {
		obj.SetAPIVersion(schema.GroupVersion{Group: targetAPIResource.Group, Version: targetAPIResource.Version}.String())
	}

	// set current revision
	revision, ok := r.Object().GetAnnotations()[common.CurrentRevisionAnnotation]
	if ok {
		if _, err := annotationutil.AddAnnotation(obj, common.CurrentRevisionAnnotation, revision); err != nil {
			return nil, err
		}
	}

	if schemautil.IsJobGvk(r.TargetGVK()) {
		if err := dropJobFields(obj); err != nil {
			return nil, err
		}

		if err := addRetainObjectFinalizer(obj); err != nil {
			return nil, err
		}
	}

	if schemautil.IsServiceGvk(r.TargetGVK()) {
		if err := dropServiceFields(obj); err != nil {
			return nil, err
		}
	}

	if schemautil.IsPodGvk(r.TargetGVK()) {
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
	otherOverrides fedtypesv1a1.OverridePatches,
) error {
	overrides, err := r.overridesForCluster(clusterName)
	if err != nil {
		return err
	}
	if overrides != nil {
		if err := util.ApplyJsonPatch(obj, overrides); err != nil {
			return err
		}
	}
	if len(otherOverrides) != 0 {
		if err := util.ApplyJsonPatch(obj, otherOverrides); err != nil {
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

func (r *federatedResource) overridesForCluster(clusterName string) (fedtypesv1a1.OverridePatches, error) {
	r.Lock()
	defer r.Unlock()
	if r.overridesMap == nil {
		obj, err := util.UnmarshalGenericOverrides(r.federatedResource)
		if err != nil {
			return nil, fmt.Errorf("unmarshal cluster overrides: %w", err)
		}

		r.overridesMap = make(util.OverridesMap)

		// Order overrides based on the controller name specified in the FTC
		// Put overrides from unknown sources at the end, but preserve their relative orders
		controllerOrder := make(map[string]int)
		for _, controllerGroup := range r.typeConfig.GetControllers() {
			for _, controller := range controllerGroup {
				controllerOrder[controller] = len(controllerOrder)
			}
		}
		sort.SliceStable(obj.Spec.Overrides, func(i, j int) bool {
			lhs, isKnown := controllerOrder[obj.Spec.Overrides[i].Controller]
			if !isKnown {
				// lhs is unknown
				// if rhs is known, return false so rhs can precede lhs
				// if rhs is unknown, return false to preserve the relative order
				return false
			}
			rhs, isKnown := controllerOrder[obj.Spec.Overrides[j].Controller]
			if !isKnown {
				// lhs controller is known and rhs controller is unknown
				// lhs should precede rhs
				return true
			}
			return lhs < rhs
		})

		// Merge overrides in the specified order
		for _, controllerOverride := range obj.Spec.Overrides {
			for _, clusterOverride := range controllerOverride.Clusters {
				r.overridesMap[clusterOverride.ClusterName] = append(
					r.overridesMap[clusterOverride.ClusterName], clusterOverride.Patches...,
				)
			}
		}
	}
	return r.overridesMap[clusterName], nil
}

// FIXME: Since override operator is not limited to "replace" and there can be multiple patches affecting the same key,
// we can only determine the correct replicas overrides after all overrides have ben applied.
func (r *federatedResource) ReplicasOverrideForCluster(clusterName string) (int32, bool, error) {
	overrides, err := r.overridesForCluster(clusterName)
	if err != nil {
		return 0, false, err
	}
	for _, o := range overrides {
		if o.Path == "/spec/replicas" && o.Value != nil {
			r, ok := o.Value.(float64)
			if !ok {
				return 0, false, errors.Errorf("failed to retrieve replicas override for %s", clusterName)
			}
			return int32(r), true, nil
		}
	}

	defaultReplicas, err := r.Replicas()
	if err != nil {
		return 0, false, err
	}
	if defaultReplicas == nil {
		return 0, false, errors.Errorf("failed to retrieve replicas override for %s", clusterName)
	}
	return int32(*defaultReplicas), false, nil
}

func (r *federatedResource) TotalReplicas(clusterNames sets.String) (int32, error) {
	var replicas int32
	for clusterName := range clusterNames {
		val, _, err := r.ReplicasOverrideForCluster(clusterName)
		if err != nil {
			return 0, err
		}
		replicas += val
	}
	return replicas, nil
}

func GetTemplateHash(fieldMap map[string]interface{}) (string, error) {
	fields := []string{common.SpecField, common.TemplateField}
	fieldMap, ok, err := unstructured.NestedMap(fieldMap, fields...)
	if err != nil {
		return "", errors.Wrapf(err, "Error retrieving %q", strings.Join(fields, "."))
	}
	if !ok {
		return "", nil
	}
	obj := &unstructured.Unstructured{Object: fieldMap}
	description := strings.Join(fields, ".")
	return hashUnstructured(obj, description)
}

func GetOverrideHash(rawObj *unstructured.Unstructured) (string, error) {
	override := fedtypesv1a1.GenericObjectWithOverrides{}
	err := util.UnstructuredToInterface(rawObj, &override)
	if err != nil {
		return "", errors.Wrap(err, "Error retrieving overrides")
	}
	if override.Spec == nil {
		return "", nil
	}
	// Only hash the overrides
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"overrides": override.Spec.Overrides,
		},
	}

	return hashUnstructured(obj, "overrides")
}

// TODO Investigate alternate ways of computing the hash of a field map.
func hashUnstructured(obj *unstructured.Unstructured, description string) (string, error) {
	jsonBytes, err := obj.MarshalJSON()
	if err != nil {
		return "", errors.Wrapf(err, "Failed to marshal %q to json", description)
	}
	//nolint:gosec
	hash := md5.New()
	if _, err := hash.Write(jsonBytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
