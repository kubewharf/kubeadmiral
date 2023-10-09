/*
Copyright 2016 The Kubernetes Authors.

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

package federatedcluster

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	genscheme "github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	FederatedClusterControllerName = "federated-cluster-controller"

	FinalizerFederatedClusterController = common.DefaultPrefix + "federated-cluster-controller"

	EventReasonHandleTerminatingClusterFailed  = "HandleTerminatingClusterFailed"
	EventReasonHandleTerminatingClusterBlocked = "HandleTerminatingClusterBlocked"
)

// ClusterHealthCheckConfig defines the configurable parameters for cluster health check
type ClusterHealthCheckConfig struct {
	Period time.Duration
}

// FederatedClusterController reconciles a FederatedCluster object
type FederatedClusterController struct {
	clusterInformer          fedcorev1a1informers.FederatedClusterInformer
	federatedInformerManager informermanager.FederatedInformerManager

	kubeClient kubeclient.Interface
	fedClient  fedclient.Interface

	fedSystemNamespace            string
	clusterHealthCheckConfig      *ClusterHealthCheckConfig
	clusterJoinTimeout            time.Duration
	resourceAggregationNodeFilter []labels.Selector

	worker              worker.ReconcileWorker[common.QualifiedName]
	statusCollectWorker worker.ReconcileWorker[common.QualifiedName]
	eventRecorder       record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger
}

func NewFederatedClusterController(
	kubeClient kubeclient.Interface,
	fedClient fedclient.Interface,
	clusterInformer fedcorev1a1informers.FederatedClusterInformer,
	federatedInformerManager informermanager.FederatedInformerManager,
	metrics stats.Metrics,
	logger klog.Logger,
	clusterJoinTimeout time.Duration,
	workerCount int,
	fedSystemNamespace string,
	resourceAggregationNodeFilter []labels.Selector,
) (*FederatedClusterController, error) {
	c := &FederatedClusterController{
		clusterInformer:          clusterInformer,
		federatedInformerManager: federatedInformerManager,
		kubeClient:               kubeClient,
		fedClient:                fedClient,
		fedSystemNamespace:       fedSystemNamespace,
		clusterHealthCheckConfig: &ClusterHealthCheckConfig{
			// TODO: make health check period configurable
			Period: time.Second * 30,
		},
		clusterJoinTimeout:            clusterJoinTimeout,
		resourceAggregationNodeFilter: resourceAggregationNodeFilter,
		metrics:                       metrics,
		logger:                        logger.WithValues("controller", FederatedClusterControllerName),
	}

	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(
		&corev1client.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")},
	)
	broadcaster.StartLogging(klog.V(6).Infof)
	c.eventRecorder = broadcaster.NewRecorder(
		genscheme.Scheme,
		corev1.EventSource{Component: ("federatedcluster-controller")},
	)

	c.worker = worker.NewReconcileWorker[common.QualifiedName](
		FederatedClusterControllerName,
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	c.statusCollectWorker = worker.NewReconcileWorker[common.QualifiedName](
		FederatedClusterControllerName,
		c.collectClusterStatus,
		worker.RateLimiterOptions{
			InitialDelay: 50 * time.Millisecond,
		},
		workerCount,
		metrics,
	)

	clusterInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnChanges(
		func(old metav1.Object, cur metav1.Object) bool {
			if old.GetGeneration() != cur.GetGeneration() {
				return true
			}
			if !equality.Semantic.DeepEqual(old.GetAnnotations(), cur.GetAnnotations) {
				return true
			}
			if !equality.Semantic.DeepEqual(old.GetFinalizers(), cur.GetFinalizers()) {
				return true
			}
			return false
		},
		func(cluster metav1.Object) {
			c.worker.Enqueue(common.NewQualifiedName(cluster))
		},
	))

	return c, nil
}

func (c *FederatedClusterController) HasSynced() bool {
	return c.clusterInformer.Informer().HasSynced() && c.federatedInformerManager.HasSynced()
}

func (c *FederatedClusterController) IsControllerReady() bool {
	return c.HasSynced()
}

func (c *FederatedClusterController) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, c.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync("federated-controller", ctx.Done(), c.HasSynced) {
		logger.Error(nil, "Timed out waiting for cache sync")
		return
	}

	logger.Info("Caches are synced")

	c.worker.Run(ctx)
	c.statusCollectWorker.Run(ctx)

	// Periodically enqueue all clusters to trigger status collection.
	go wait.Until(c.enqueueAllJoinedClusters, c.clusterHealthCheckConfig.Period, ctx.Done())

	<-ctx.Done()
}

func (c *FederatedClusterController) reconcile(
	ctx context.Context,
	key common.QualifiedName,
) (status worker.Result) {
	c.metrics.Counter(metrics.FederatedClusterControllerThroughput, 1)
	ctx, logger := logging.InjectLoggerValues(ctx, "cluster", key.String())

	startTime := time.Now()

	logger.V(3).Info("Starting reconcile")
	defer func() {
		c.metrics.Duration(metrics.FederatedClusterControllerLatency, startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	cluster, err := c.clusterInformer.Lister().Get(key.Name)
	if err != nil && apierrors.IsNotFound(err) {
		logger.V(3).Info("Observed cluster deletion")
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get cluster from store")
		return worker.StatusError
	}

	cluster = cluster.DeepCopy()

	if cluster.GetDeletionTimestamp() != nil {
		c.metrics.Store(metrics.ClusterDeletionState, 1,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "status", Value: "deleting"})
		c.metrics.Store(metrics.ClusterDeletionState, 0,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "status", Value: "deleted"})
		logger.V(2).Info("Handle terminating cluster")
		if err := c.handleTerminatingCluster(ctx, cluster); err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			logger.Error(err, "Failed to handle terminating cluster")
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if cluster, err = c.ensureFinalizer(ctx, cluster); err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		logger.Error(err, "Failed to ensure cluster finalizer")
		return worker.StatusError
	}

	if joined, alreadyFailed := isClusterJoined(&cluster.Status); joined || alreadyFailed {
		return worker.StatusAllOK
	}

	// Not joined yet and not failed, so we try to join
	logger.V(2).Info("Handle unjoined cluster")
	cluster, newCondition, newJoinPerformed, err := c.handleNotJoinedCluster(ctx, cluster)

	needsUpdate := false
	if newCondition != nil {
		needsUpdate = true

		currentTime := metav1.Now()
		newCondition.Type = fedcorev1a1.ClusterJoined
		newCondition.LastProbeTime = currentTime
		newCondition.LastTransitionTime = currentTime

		setClusterCondition(&cluster.Status, newCondition)
	}
	if newJoinPerformed != nil && *newJoinPerformed != cluster.Status.JoinPerformed {
		needsUpdate = true
		cluster.Status.JoinPerformed = *newJoinPerformed
	}

	if needsUpdate {
		var updateErr error
		if cluster, updateErr = c.fedClient.CoreV1alpha1().FederatedClusters().UpdateStatus(
			ctx,
			cluster,
			metav1.UpdateOptions{},
		); updateErr != nil {
			logger.Error(updateErr, "Failed to update cluster status")
			err = updateErr
		}
	}

	if err != nil {
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	// Trigger initial status collection if successfully joined
	if joined, alreadyFailed := isClusterJoined(&cluster.Status); joined && !alreadyFailed {
		c.statusCollectWorker.Enqueue(common.NewQualifiedName(cluster))
	}

	return worker.StatusAllOK
}

func (c *FederatedClusterController) collectClusterStatus(
	ctx context.Context,
	key common.QualifiedName,
) (status worker.Result) {
	ctx, logger := logging.InjectLoggerValues(ctx, "cluster", key.String(), "worker", "status-collect")

	startTime := time.Now()

	logger.V(3).Info("Starting to collect cluster status")
	defer func() {
		c.metrics.Duration(metrics.FederatedClusterControllerCollectStatusLatency, startTime)
		logger.WithValues(
			"duration",
			time.Since(startTime),
			"status",
			status.String(),
		).V(3).Info("Finished collecting cluster status")
	}()

	cluster, err := c.clusterInformer.Lister().Get(key.Name)
	if err != nil && apierrors.IsNotFound(err) {
		logger.V(3).Info("Observed cluster deletion")
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get cluster from store")
		return worker.StatusError
	}

	cluster = cluster.DeepCopy()

	if shouldCollectClusterStatus(cluster, c.clusterHealthCheckConfig.Period) {
		retryAfter, err := c.collectIndividualClusterStatus(ctx, cluster)
		if err != nil {
			logger.Error(err, "Failed to collect cluster status")
			return worker.StatusError
		}
		if retryAfter > 0 {
			return worker.Result{
				Success:      true,
				RequeueAfter: &retryAfter,
				Backoff:      false,
			}
		}
	}

	return worker.StatusAllOK
}

func (c *FederatedClusterController) ensureFinalizer(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
) (*fedcorev1a1.FederatedCluster, error) {
	updated, err := finalizers.AddFinalizers(cluster, sets.NewString(FinalizerFederatedClusterController))
	if err != nil {
		return nil, err
	}

	if updated {
		return c.fedClient.CoreV1alpha1().FederatedClusters().Update(ctx, cluster, metav1.UpdateOptions{})
	}

	return cluster, nil
}

func (c *FederatedClusterController) handleTerminatingCluster(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
) error {
	finalizers := sets.New(cluster.GetFinalizers()...)
	if !finalizers.Has(FinalizerFederatedClusterController) {
		return nil
	}

	// We need to ensure that all other finalizers are removed before we start cleanup as other controllers may still
	// rely on the service account credentials to do their own cleanup.
	if len(finalizers) > 1 {
		c.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeNormal,
			EventReasonHandleTerminatingClusterBlocked,
			"Waiting for other finalizers to be cleaned up",
		)
		return nil
	}

	// Only perform clean-up if we made any effectual changes to the cluster during join.
	if cluster.Status.JoinPerformed {
		clusterSecret, clusterKubeClient, err := c.getClusterClient(ctx, cluster)
		if err != nil {
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"Failed to get cluster client: %v",
				err,
			)
			return fmt.Errorf("failed to get cluster client: %w", err)
		}

		// 1. cleanup service account token from cluster secret if required.

		if cluster.Spec.UseServiceAccountToken {
			var err error

			_, tokenKeyExists := clusterSecret.Data[common.ClusterServiceAccountTokenKey]
			if tokenKeyExists {
				delete(clusterSecret.Data, common.ClusterServiceAccountTokenKey)

				if _, err = c.kubeClient.CoreV1().Secrets(c.fedSystemNamespace).Update(
					ctx,
					clusterSecret,
					metav1.UpdateOptions{},
				); err != nil {
					return fmt.Errorf("failed to remove service account info from cluster secret: %w", err)
				}
			}
		}

		// 2. connect to cluster and perform cleanup

		err = clusterKubeClient.CoreV1().Namespaces().Delete(
			ctx,
			c.fedSystemNamespace,
			metav1.DeleteOptions{},
		)
		if err != nil && !apierrors.IsNotFound(err) {
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"Delete system namespace from cluster %q failed: %v, will retry later",
				cluster.Name,
				err,
			)
			return fmt.Errorf("failed to delete fed system namespace from cluster: %w", err)
		}
	}

	// We have already checked that we are the last finalizer so we can simply set finalizers to be empty.
	cluster.SetFinalizers(nil)
	if _, err := c.fedClient.CoreV1alpha1().FederatedClusters().Update(
		ctx,
		cluster,
		metav1.UpdateOptions{},
	); err != nil {
		return fmt.Errorf("failed to update cluster for finalizer removal: %w", err)
	}

	c.metrics.Store(metrics.ClusterDeletionState, 0,
		stats.Tag{Name: "cluster_name", Value: cluster.Name},
		stats.Tag{Name: "status", Value: "deleting"})
	c.metrics.Store(metrics.ClusterDeletionState, 1,
		stats.Tag{Name: "cluster_name", Value: cluster.Name},
		stats.Tag{Name: "status", Value: "deleted"})
	return nil
}

func (c *FederatedClusterController) getClusterClient(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
) (*corev1.Secret, kubeclient.Interface, error) {
	restConfig := &rest.Config{Host: cluster.Spec.APIEndpoint}

	clusterSecretName := cluster.Spec.SecretRef.Name
	if clusterSecretName == "" {
		return nil, nil, fmt.Errorf("cluster secret is not set")
	}

	clusterSecret, err := c.kubeClient.CoreV1().Secrets(c.fedSystemNamespace).Get(
		ctx,
		clusterSecretName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster secret: %w", err)
	}

	if err := clusterutil.PopulateAuthDetailsFromSecret(restConfig, cluster.Spec.Insecure, clusterSecret, false); err != nil {
		return nil, nil, fmt.Errorf("cluster secret malformed: %w", err)
	}

	clusterClient, err := kubeclient.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cluster kube clientset: %w", err)
	}

	return clusterSecret, clusterClient, nil
}

func (c *FederatedClusterController) enqueueAllJoinedClusters() {
	clusters, err := c.clusterInformer.Lister().List(labels.Everything())
	if err != nil {
		c.logger.Error(err, "Failed to enqueue all clusters")
	}

	for _, cluster := range clusters {
		if clusterutil.IsClusterJoined(&cluster.Status) {
			c.statusCollectWorker.Enqueue(common.NewQualifiedName(cluster))
		}
	}
}
