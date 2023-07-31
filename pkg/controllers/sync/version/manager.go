/*
Copyright 2018 The Kubernetes Authors.

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

package version

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1client "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/typed/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/util/propagatedversion"
)

// VersionedResource defines the methods a federated resource must
// implement to allow versions to be tracked by the VersionManager.
type VersionedResource interface {
	// FederatedName returns the qualified name of the underlying
	// FederatedObject or ClusterFederatedObject.
	FederatedName() common.QualifiedName
	// Object returns the underlying FederatedObject or ClusterFederatedObject
	// as a GenericFederatedObject.
	Object() fedcorev1a1.GenericFederatedObject
	// TemplateVersion returns the resource's current template version.
	TemplateVersion() (string, error)
	// OverrideVersion returns the resource's current override version.
	OverrideVersion() (string, error)
	// FederatedGVK returns the GroupVersionKind of the underlying
	// FederatedObject or ClusterFederatedObject.
	FederatedGVK() schema.GroupVersionKind
}

/*
VersionManager is used by the Sync controller to record the last synced version
of a FederatedObject along with the versions of the cluster objects that were
created/updated in the process. This is important in preventing unnecessary
update requests from being sent to member clusters in subsequent reconciles. The
VersionManager persists this information in the apiserver in the form of
PropagatedVersion/ClusterPropagatedVersions, see
pkg/apis/types_propagatedversion.go.

In the context of the Sync controller, we identify the "version" of a
FederatedObject with the hash of its template and overrides and we identify the
"version" of a cluster object to be either its Generation (if available) or its
ResourceVersion.

VersionManager is required because created/updated cluster objects might not
match the template exactly due to various reasons such as default values,
admission plugins or webhooks. Thus we have to store the version returned by the
create/update request to avoid false-positives when determining if the cluster
object has diverged from the template in subsequent reconciles.
*/
type VersionManager struct {
	sync.RWMutex

	// Namespace to source propagated versions from
	namespace string

	adapter VersionAdapter

	hasSynced bool

	versions map[string]runtimeclient.Object

	client fedcorev1a1client.CoreV1alpha1Interface

	logger klog.Logger
}

func NewNamespacedVersionManager(
	logger klog.Logger,
	client fedcorev1a1client.CoreV1alpha1Interface,
	namespace string,
) *VersionManager {
	adapter := NewVersionAdapter(true)
	v := &VersionManager{
		logger:    logger.WithValues("origin", "version-manager", "type-name", adapter.TypeName()),
		namespace: namespace,
		adapter:   adapter,
		versions:  make(map[string]runtimeclient.Object),
		client:    client,
	}

	return v
}

func NewClusterVersionManager(
	logger klog.Logger,
	client fedcorev1a1client.CoreV1alpha1Interface,
) *VersionManager {
	adapter := NewVersionAdapter(false)
	v := &VersionManager{
		logger:    logger.WithValues("origin", "version-manager", "type-name", adapter.TypeName()),
		namespace: "",
		adapter:   adapter,
		versions:  make(map[string]runtimeclient.Object),
		client:    client,
	}

	return v
}

// Sync retrieves propagated versions from the api and loads it into
// memory.
func (m *VersionManager) Sync(ctx context.Context) {
	versionList, ok := m.list(ctx)
	if !ok {
		return
	}
	m.load(ctx, versionList)
}

// HasSynced indicates whether the manager's in-memory state has been
// synced with the api.
func (m *VersionManager) HasSynced() bool {
	m.RLock()
	defer m.RUnlock()
	return m.hasSynced
}

// Get retrieves a mapping of cluster names to versions for the given versioned
// resource. It returns an empty map if the desired object for the versioned
// resource is different from last recorded.
func (m *VersionManager) Get(resource VersionedResource) (map[string]string, error) {
	versionMap := make(map[string]string)

	qualifiedName := m.versionQualifiedName(resource.FederatedName())
	key := qualifiedName.String()
	m.RLock()
	obj, ok := m.versions[key]
	m.RUnlock()
	if !ok {
		return versionMap, nil
	}
	status := m.adapter.GetStatus(obj)

	templateVersion, err := resource.TemplateVersion()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to determine template version")
	}
	overrideVersion, err := resource.OverrideVersion()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to determine override version")
	}
	if templateVersion == status.TemplateVersion &&
		overrideVersion == status.OverrideVersion {
		for _, versions := range status.ClusterVersions {
			versionMap[versions.ClusterName] = versions.Version
		}
	}

	return versionMap, nil
}

// Update ensures that the propagated version for the given versioned
// resource is recorded.
func (m *VersionManager) Update(
	resource VersionedResource,
	selectedClusters []string,
	versionMap map[string]string,
) error {
	templateVersion, err := resource.TemplateVersion()
	if err != nil {
		return errors.Wrap(err, "Failed to determine template version")
	}
	overrideVersion, err := resource.OverrideVersion()
	if err != nil {
		return errors.Wrap(err, "Failed to determine override version")
	}
	qualifiedName := m.versionQualifiedName(resource.FederatedName())
	key := qualifiedName.String()

	m.Lock()

	obj, ok := m.versions[key]

	var oldStatus *fedcorev1a1.PropagatedVersionStatus
	var clusterVersions []fedcorev1a1.ClusterObjectVersion
	if ok {
		oldStatus = m.adapter.GetStatus(obj)
		// The existing versions are still valid if the template and override versions match.
		if oldStatus.TemplateVersion == templateVersion && oldStatus.OverrideVersion == overrideVersion {
			clusterVersions = oldStatus.ClusterVersions
		}
		clusterVersions = updateClusterVersions(clusterVersions, versionMap, selectedClusters)
	} else {
		clusterVersions = VersionMapToClusterVersions(versionMap)
	}

	status := &fedcorev1a1.PropagatedVersionStatus{
		TemplateVersion: templateVersion,
		OverrideVersion: overrideVersion,
		ClusterVersions: clusterVersions,
	}

	if oldStatus != nil && propagatedversion.PropagatedVersionStatusEquivalent(oldStatus, status) {
		m.Unlock()
		m.logger.WithValues("version-qualified-name", qualifiedName).
			V(4).Info("No need to update propagated version status")
		return nil
	}

	if obj == nil {
		ownerReference := ownerReferenceForFederatedObject(resource)
		obj = m.adapter.NewVersion(qualifiedName, ownerReference, status)
		m.versions[key] = obj
	} else {
		m.adapter.SetStatus(obj, status)
	}

	m.Unlock()

	// Since writeVersion calls the Kube API, the manager should be
	// unlocked to avoid blocking on calls across the network.

	return m.writeVersion(obj, qualifiedName)
}

// Delete removes the named propagated version from the manager.
// Versions are written to the API with an owner reference to the
// versioned resource, and they should be removed by the garbage
// collector when the resource is removed.
func (m *VersionManager) Delete(qualifiedName common.QualifiedName) {
	versionQualifiedName := m.versionQualifiedName(qualifiedName)
	m.Lock()
	delete(m.versions, versionQualifiedName.String())
	m.Unlock()
}

func (m *VersionManager) list(ctx context.Context) (runtimeclient.ObjectList, bool) {
	// Attempt retrieval of list of versions until success or the channel is closed.
	var versionList runtimeclient.ObjectList
	err := wait.PollImmediateInfiniteWithContext(ctx, 1*time.Second, func(ctx context.Context) (bool, error) {
		var err error
		versionList, err = m.adapter.List(
			ctx, m.client, m.namespace, metav1.ListOptions{
				ResourceVersion: "0",
			})
		if err != nil {
			m.logger.Error(err, "Failed to list propagated versions")
			// Do not return the error to allow the operation to be retried.
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, false
	}
	return versionList, true
}

// load processes a list of versions into in-memory cache.  Since the
// version manager should not be used in advance of HasSynced
// returning true, locking is assumed to be unnecessary.
func (m *VersionManager) load(ctx context.Context, versionList runtimeclient.ObjectList) bool {
	objs, err := meta.ExtractList(versionList)
	if err != nil {
		m.logger.Error(err, "Failed to extract version list")
		return false
	}
	for _, obj := range objs {
		select {
		case <-ctx.Done():
			m.logger.Info("Halting version manager load due to closed stop channel")
			return false
		default:
		}

		qualifiedName := common.NewQualifiedName(obj)
		m.versions[qualifiedName.String()] = obj.(runtimeclient.Object)
	}
	m.Lock()
	m.hasSynced = true
	m.Unlock()
	m.logger.Info("Version manager synced")
	return true
}

// versionQualifiedName derives the qualified name of a version
// resource from the qualified name of a federated object.
func (m *VersionManager) versionQualifiedName(qualifiedName common.QualifiedName) common.QualifiedName {
	return qualifiedName
}

// writeVersion serializes the current state of the named propagated
// version to the API.
//
// The manager is expected to be called synchronously by the sync
// controller which should ensure that the version object for a given
// resource is updated by at most one thread at a time.  This should
// guarantee safe manipulation of an object retrieved from the
// version map.
func (m *VersionManager) writeVersion(obj runtimeclient.Object, qualifiedName common.QualifiedName) error {
	key := qualifiedName.String()
	adapterType := m.adapter.TypeName()
	keyedLogger := m.logger.WithValues("version-qualified-name", key)

	resourceVersion := getResourceVersion(obj)
	refreshVersion := false
	// TODO Centralize polling interval and duration
	waitDuration := 30 * time.Second
	err := wait.PollImmediate(100*time.Millisecond, waitDuration, func() (bool, error) {
		var err error

		if refreshVersion {
			// Version was written to the API by another process after the last manager write.
			resourceVersion, err = m.getResourceVersionFromAPI(qualifiedName)
			if err != nil {
				keyedLogger.Error(err, "Failed to refresh the resourceVersion from the API")
				return false, nil
			}
			refreshVersion = false
		}

		if resourceVersion == "" {
			// Version resource needs to be created

			createdObj := obj.DeepCopyObject().(runtimeclient.Object)
			setResourceVersion(createdObj, "")
			keyedLogger.V(1).Info("Creating resourceVersion")
			createdObj, err = m.adapter.Create(context.TODO(), m.client, createdObj, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				keyedLogger.V(1).Info("ResourceVersion was created by another process. Will refresh the resourceVersion and attempt to update")
				refreshVersion = true
				return false, nil
			}
			// Forbidden is likely to be a permanent failure and
			// likely the result of the containing namespace being
			// deleted.
			if apierrors.IsForbidden(err) {
				return false, err
			}
			if err != nil {
				keyedLogger.Error(err, "Failed to create resourceVersion")
				return false, nil
			}

			// Update the resource version that will be used for update.
			resourceVersion = getResourceVersion(createdObj)
		}

		// Update the status of an existing object

		updatedObj := obj.DeepCopyObject().(runtimeclient.Object)
		setResourceVersion(updatedObj, resourceVersion)

		keyedLogger.V(1).Info("Updating the status")
		updatedObj, err = m.adapter.UpdateStatus(context.TODO(), m.client, updatedObj, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			keyedLogger.V(1).Info("ResourceVersion was updated by another process. Will refresh the resourceVersion and retry the update")
			refreshVersion = true
			return false, nil
		}
		if apierrors.IsNotFound(err) {
			keyedLogger.V(1).Info("ResourceVersion was deleted by another process. Will clear the resourceVersion and retry the update")
			resourceVersion = ""
			return false, nil
		}
		// Forbidden is likely to be a permanent failure and
		// likely the result of the containing namespace being
		// deleted.
		if apierrors.IsForbidden(err) {
			return false, err
		}
		if err != nil {
			keyedLogger.Error(err, "Failed to update the status")
			return false, nil
		}

		// Update was successful. All returns should be true even in
		// the event of an error since the next reconcile can also
		// refresh the resource version if necessary.

		// Update the version resource
		resourceVersion = getResourceVersion(updatedObj)
		setResourceVersion(obj, resourceVersion)

		return true, nil
	})
	if err != nil {
		return errors.Wrapf(err, "Failed to write the version map for %s %q to the API", adapterType, key)
	}
	return nil
}

func (m *VersionManager) getResourceVersionFromAPI(qualifiedName common.QualifiedName) (string, error) {
	m.logger.V(2).Info("Retrieving resourceVersion from the API", "version-qualified-name", qualifiedName)
	obj, err := m.adapter.Get(context.TODO(), m.client, qualifiedName.Namespace, qualifiedName.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return getResourceVersion(obj), nil
}

func getResourceVersion(obj runtimeclient.Object) string {
	return obj.GetResourceVersion()
}

func setResourceVersion(obj runtimeclient.Object, resourceVersion string) {
	obj.SetResourceVersion(resourceVersion)
}

func ownerReferenceForFederatedObject(resource VersionedResource) metav1.OwnerReference {
	gvk := resource.FederatedGVK()
	obj := resource.Object()
	return metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
	}
}

func updateClusterVersions(
	oldVersions []fedcorev1a1.ClusterObjectVersion,
	newVersions map[string]string,
	selectedClusters []string,
) []fedcorev1a1.ClusterObjectVersion {
	// Retain versions for selected clusters that were not changed
	selectedClusterSet := sets.NewString(selectedClusters...)
	for _, oldVersion := range oldVersions {
		if !selectedClusterSet.Has(oldVersion.ClusterName) {
			continue
		}
		if _, ok := newVersions[oldVersion.ClusterName]; !ok {
			newVersions[oldVersion.ClusterName] = oldVersion.Version
		}
	}

	return VersionMapToClusterVersions(newVersions)
}

func VersionMapToClusterVersions(versionMap map[string]string) []fedcorev1a1.ClusterObjectVersion {
	clusterVersions := []fedcorev1a1.ClusterObjectVersion{}
	for clusterName, version := range versionMap {
		// Lack of version indicates deletion
		if version == "" {
			continue
		}
		clusterVersions = append(clusterVersions, fedcorev1a1.ClusterObjectVersion{
			ClusterName: clusterName,
			Version:     version,
		})
	}
	propagatedversion.SortClusterVersions(clusterVersions)
	return clusterVersions
}
