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

package nsautoprop

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	corev1informers "k8s.io/client-go/informers/core/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/adoption"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/orphaning"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	NamespaceAutoPropagationControllerName         = "nsautoprop-controller"
	PrefixedNamespaceAutoPropagationControllerName = common.DefaultPrefix + NamespaceAutoPropagationControllerName
	EventReasonNamespaceAutoPropagation            = "NamespaceAutoPropagation"
)

var namespaceGVK = corev1.SchemeGroupVersion.WithKind("Namespace")

/*
NamespacesAutoPropagationController automatically propagates namespaces to all
clusters without requiring a ClusterPropagationPolicy for scheduling.
It
  - sets placement of federated namespaces to all clusters
  - ensures preexisting namespaces in member clusters are adopted
  - ensures adopted namespaces in member clusters are not deleted when the
    federated namespace is deleted

Note that since both NamespaceAutoPropagationController and global-scheduler sets the placements,
if both are enabled, they will conflict with each other and reconcile indefinitely.
*/
type Controller struct {
	fedClient fedclient.Interface

	informerManager          informermanager.InformerManager
	namespaceInformer        corev1informers.NamespaceInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer
	clusterInformer          fedcorev1a1informers.FederatedClusterInformer

	worker        worker.ReconcileWorker[common.QualifiedName]
	eventRecorder record.EventRecorder

	excludeRegexp      *regexp.Regexp
	fedSystemNamespace string

	logger  klog.Logger
	metrics stats.Metrics
}

func (c *Controller) IsControllerReady() bool {
	return c.HasSynced()
}

func NewNamespaceAutoPropagationController(
	kubeClient kubeclient.Interface,
	informerManager informermanager.InformerManager,
	fedClient fedclient.Interface,
	clusterInformer fedcorev1a1informers.FederatedClusterInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	namespaceInformer corev1informers.NamespaceInformer,
	nsExcludeRegexp *regexp.Regexp,
	fedSystemNamespace string,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*Controller, error) {
	c := &Controller{
		fedClient: fedClient,

		informerManager:          informerManager,
		clusterFedObjectInformer: clusterFedObjectInformer,
		clusterInformer:          clusterInformer,
		namespaceInformer:        namespaceInformer,

		excludeRegexp:      nsExcludeRegexp,
		fedSystemNamespace: fedSystemNamespace,

		eventRecorder: eventsink.NewDefederatingRecorderMux(kubeClient, NamespaceAutoPropagationControllerName, 4),
		metrics:       metrics,
		logger:        logger.WithValues("controller", NamespaceAutoPropagationControllerName),
	}

	c.worker = worker.NewReconcileWorker[common.QualifiedName](
		NamespaceAutoPropagationControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	if _, err := c.clusterFedObjectInformer.Informer().AddEventHandlerWithResyncPeriod(
		eventhandlers.NewTriggerOnAllChanges(
			func(obj *fedcorev1a1.ClusterFederatedObject) {
				srcMeta, err := obj.Spec.GetTemplateAsUnstructured()
				if err != nil {
					c.logger.Error(
						err,
						"Failed to get source object's metadata from ClusterFederatedObject",
						"object",
						common.NewQualifiedName(obj),
					)
					return
				}
				if srcMeta.GetKind() != common.NamespaceKind || !c.shouldBeAutoPropagated(srcMeta) {
					return
				}
				c.worker.Enqueue(common.QualifiedName{Name: obj.GetName()})
			},
		), 0); err != nil {
		return nil, err
	}

	reconcileAll := func() {
		typeConfig, exists := c.informerManager.GetResourceFTC(namespaceGVK)
		if !exists {
			c.logger.Error(nil, "Namespace ftc does not exist")
			return
		}

		allNamespaces, err := c.namespaceInformer.Lister().List(labels.Everything())
		if err != nil {
			c.logger.Error(err, "Failed to list all namespaces")
			return
		}

		for _, ns := range allNamespaces {
			c.worker.Enqueue(common.QualifiedName{Name: naming.GenerateFederatedObjectName(ns.Name, typeConfig.Name)})
		}
	}

	if _, err := c.clusterInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			reconcileAll()
		},
		DeleteFunc: func(obj interface{}) {
			reconcileAll()
		},
	}, 0); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Controller) reconcile(ctx context.Context, qualifiedName common.QualifiedName) worker.Result {
	ctx, keyedLogger := logging.InjectLoggerValues(ctx, "federated-name", qualifiedName.String())

	c.metrics.Counter(metrics.NamespaceAutoPropagationControllerThroughput, 1)
	keyedLogger.V(3).Info("Starting to reconcile")
	startTime := time.Now()
	defer func() {
		c.metrics.Duration(metrics.NamespaceAutoPropagationControllerLatency, startTime)
		keyedLogger.WithValues("duration", time.Since(startTime)).V(3).Info("Finished reconciling")
	}()

	fedNamespace, err := c.clusterFedObjectInformer.Lister().Get(qualifiedName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get federated namespace")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) || fedNamespace.GetDeletionTimestamp() != nil {
		return worker.StatusAllOK
	}
	fedNamespace = fedNamespace.DeepCopy()

	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(
		fedNamespace,
		PrefixedNamespaceAutoPropagationControllerName,
	); err != nil {
		keyedLogger.Error(err, "Failed to get pending controllers")
		return worker.StatusError
	} else if !ok {
		return worker.StatusAllOK
	}

	typeConfig, exists := c.informerManager.GetResourceFTC(namespaceGVK)
	if !exists {
		keyedLogger.Error(nil, "Namespace ftc does not exist")
		return worker.StatusError
	}

	srcMeta, err := fedNamespace.Spec.GetTemplateAsUnstructured()
	if err != nil {
		keyedLogger.Error(err, "Failed to get source object's metadata from ClusterFederatedObject")
		return worker.StatusError
	}

	if !c.shouldBeAutoPropagated(srcMeta) {
		updated, err := pendingcontrollers.UpdatePendingControllers(
			fedNamespace,
			PrefixedNamespaceAutoPropagationControllerName,
			false,
			typeConfig.GetControllers(),
		)
		if err != nil {
			keyedLogger.Error(err, "Failed to set pending controllers")
			return worker.StatusError
		}

		if updated {
			_, err = c.fedClient.CoreV1alpha1().
				ClusterFederatedObjects().
				Update(ctx, fedNamespace, metav1.UpdateOptions{})
			if err != nil {
				if apierrors.IsConflict(err) {
					return worker.StatusConflict
				}
				keyedLogger.Error(err, "Failed to update cluster federated object")
				return worker.StatusError
			}
		}
		return worker.StatusAllOK
	}

	c.recordNamespacePropagationFailedMetric(fedNamespace)

	needsUpdate := false

	// Set placement to propagate to all clusters
	clusters, err := c.clusterInformer.Lister().List(labels.Everything())
	if err != nil {
		keyedLogger.Error(err, "Failed to list federated clusters")
		return worker.StatusError
	}

	clusterNames := make([]string, 0, len(clusters))
	for _, clusterName := range clusters {
		clusterNames = append(clusterNames, clusterName.Name)
	}

	isDirty := fedNamespace.Spec.SetControllerPlacement(PrefixedNamespaceAutoPropagationControllerName, clusterNames)

	needsUpdate = needsUpdate || isDirty

	// Set internal versions of the annotations so they do not get overridden by federate controller

	// Ensure we adopt pre-existing namespaces in member clusters
	isDirty, err = c.ensureAnnotation(
		fedNamespace,
		adoption.ConflictResolutionInternalAnnotation,
		string(adoption.ConflictResolutionAdopt),
	)
	if err != nil {
		keyedLogger.Error(err, "Failed to ensure annotation")
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	// Ensure we don't delete adopted member namespaces when the federated namespace is deleted
	isDirty, err = c.ensureAnnotation(
		fedNamespace,
		orphaning.OrphanManagedResourcesInternalAnnotation,
		string(orphaning.OrphanManagedResourcesAdopted),
	)
	if err != nil {
		keyedLogger.Error(err, "Failed to ensure annotation")
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	// Update the pending controllers to unblock downstream controllers
	isDirty, err = pendingcontrollers.UpdatePendingControllers(
		fedNamespace,
		PrefixedNamespaceAutoPropagationControllerName,
		needsUpdate,
		typeConfig.GetControllers(),
	)
	if err != nil {
		keyedLogger.Error(err, "Failed to update pending controllers")
		return worker.StatusError
	}
	needsUpdate = needsUpdate || isDirty

	if !needsUpdate {
		return worker.StatusAllOK
	}

	_, err = c.fedClient.CoreV1alpha1().ClusterFederatedObjects().Update(ctx, fedNamespace, metav1.UpdateOptions{})
	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		c.eventRecorder.Eventf(fedNamespace, corev1.EventTypeWarning, EventReasonNamespaceAutoPropagation,
			"failed to update %s for auto propagation, err: %v",
			fedNamespace.GetName(), err)
		return worker.StatusError
	}

	c.eventRecorder.Eventf(fedNamespace, corev1.EventTypeNormal, EventReasonNamespaceAutoPropagation,
		"updated %s for auto propagation", fedNamespace.GetName())

	return worker.StatusAllOK
}

func (c *Controller) shouldBeAutoPropagated(fedNamespace *unstructured.Unstructured) bool {
	name := fedNamespace.GetName()

	if strings.HasPrefix(name, "kube-") {
		// don't propagate system namespaces
		return false
	}

	if name == c.fedSystemNamespace {
		// don't propagate our own namespace
		return false
	}

	if c.excludeRegexp != nil && c.excludeRegexp.MatchString(name) {
		// don't propagate namespaces matched by exclusion regex
		return false
	}

	if fedNamespace.GetAnnotations()[NoAutoPropagationAnnotation] == common.AnnotationValueTrue {
		return false
	}

	return true
}

func (c *Controller) ensureAnnotation(
	fedNamespace *fedcorev1a1.ClusterFederatedObject,
	key, value string,
) (bool, error) {
	needsUpdate, err := annotationutil.AddAnnotation(fedNamespace, key, value)
	if err != nil {
		return false, fmt.Errorf(
			"failed to add %s annotation to %s, err: %w",
			key, fedNamespace.GetName(), err)
	}

	return needsUpdate, nil
}

func (c *Controller) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	go c.namespaceInformer.Informer().Run(ctx.Done())

	if !cache.WaitForNamedCacheSync(NamespaceAutoPropagationControllerName, ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for caches to sync")
		return
	}

	logger.Info("Caches are synced")
	c.worker.Run(ctx)
	<-ctx.Done()
}

func (c *Controller) HasSynced() bool {
	return c.clusterInformer.Informer().HasSynced() &&
		c.clusterFedObjectInformer.Informer().HasSynced() &&
		c.namespaceInformer.Informer().HasSynced() &&
		c.informerManager.HasSynced()
}

func (c *Controller) recordNamespacePropagationFailedMetric(fedNamespace *fedcorev1a1.ClusterFederatedObject) {
	errorClusterCount := 0

	for _, clusterStatus := range fedNamespace.Status.Clusters {
		if clusterStatus.Status != fedcorev1a1.ClusterPropagationOK && clusterStatus.Status != fedcorev1a1.WaitingForRemoval {
			errorClusterCount++
		}
	}

	if errorClusterCount != 0 {
		c.metrics.Store(metrics.NamespacePropagateFailedTotal, errorClusterCount, stats.Tag{Name: "namespace", Value: fedNamespace.Name})
	}
}
