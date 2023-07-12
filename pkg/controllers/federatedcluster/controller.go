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
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/federatedclient"
	finalizerutils "github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const (
	ControllerName = "federated-cluster-controller"

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
	client     fedclient.Interface
	kubeClient kubeclient.Interface

	clusterLister   fedcorev1a1listers.FederatedClusterLister
	clusterSynced   cache.InformerSynced
	federatedClient federatedclient.FederatedClientFactory

	fedSystemNamespace       string
	clusterHealthCheckConfig *ClusterHealthCheckConfig
	clusterJoinTimeout       time.Duration

	eventRecorder record.EventRecorder
	metrics       stats.Metrics
	logger        klog.Logger

	worker              worker.ReconcileWorker
	statusCollectWorker worker.ReconcileWorker
}

func NewFederatedClusterController(
	client fedclient.Interface,
	kubeClient kubeclient.Interface,
	informer fedcorev1a1informers.FederatedClusterInformer,
	federatedClient federatedclient.FederatedClientFactory,
	metrics stats.Metrics,
	fedsystemNamespace string,
	restConfig *rest.Config,
	workerCount int,
	clusterJoinTimeout time.Duration,
) (*FederatedClusterController, error) {
	c := &FederatedClusterController{
		client:             client,
		kubeClient:         kubeClient,
		clusterLister:      informer.Lister(),
		clusterSynced:      informer.Informer().HasSynced,
		federatedClient:    federatedClient,
		fedSystemNamespace: fedsystemNamespace,
		clusterHealthCheckConfig: &ClusterHealthCheckConfig{
			Period: time.Minute,
		},
		clusterJoinTimeout: clusterJoinTimeout,
		metrics:            metrics,
		logger:             klog.LoggerWithValues(klog.Background(), "controller", ControllerName),
	}

	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(
		&corev1client.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")},
	)
	broadcaster.StartLogging(klog.V(4).Infof)
	c.eventRecorder = broadcaster.NewRecorder(
		genscheme.Scheme,
		corev1.EventSource{Component: ("federatedcluster-controller")},
	)

	c.worker = worker.NewReconcileWorker(
		c.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
		delayingdeliver.NewMetricTags("federatedcluster-worker", "FederatedCluster"),
	)

	c.statusCollectWorker = worker.NewReconcileWorker(
		c.collectClusterStatus,
		worker.RateLimiterOptions{
			InitialDelay: 50 * time.Millisecond,
		},
		workerCount,
		metrics,
		delayingdeliver.NewMetricTags("federatedcluster-status-collect-worker", "FederatedCluster"),
	)

	informer.Informer().
		AddEventHandler(util.NewTriggerOnGenerationAndMetadataChanges(c.worker.EnqueueObject,
			func(oldMeta, newMeta metav1.Object) bool {
				if !reflect.DeepEqual(oldMeta.GetAnnotations(), newMeta.GetAnnotations()) ||
					!reflect.DeepEqual(oldMeta.GetFinalizers(), newMeta.GetFinalizers()) {
					return true
				}
				return false
			}))

	return c, nil
}

func (c *FederatedClusterController) IsControllerReady() bool {
	return c.clusterSynced()
}

func (c *FederatedClusterController) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	c.logger.Info("Starting controller")
	defer c.logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync("federated-controller", ctx.Done(), c.clusterSynced) {
		return
	}

	c.worker.Run(ctx.Done())
	c.statusCollectWorker.Run(ctx.Done())

	// periodically enqueue all clusters to trigger status collection
	go wait.Until(c.enqueueAllJoinedClusters, c.clusterHealthCheckConfig.Period, ctx.Done())

	<-ctx.Done()
}

func (c *FederatedClusterController) reconcile(qualifiedName common.QualifiedName) (status worker.Result) {
	_ = c.metrics.Rate("federated-cluster-controller.throughput", 1)
	logger := c.logger.WithValues("control-loop", "reconcile", "object", qualifiedName.String())
	ctx := klog.NewContext(context.TODO(), logger)
	startTime := time.Now()

	logger.V(3).Info("Starting reconcile")
	defer c.metrics.Duration("federated-cluster-controller.latency", startTime)
	defer func() {
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	cluster, err := c.clusterLister.Get(qualifiedName.Name)
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
		logger.V(2).Info("Handle terminating cluster")
		err := handleTerminatingCluster(
			ctx,
			cluster,
			c.client,
			c.kubeClient,
			c.eventRecorder,
			c.fedSystemNamespace,
		)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			logger.Error(err, "Failed to handle terminating cluster")
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if cluster, err = ensureFinalizer(ctx, cluster, c.client); err != nil {
		if apierrors.IsConflict(err) {
			// Ignore IsConflict errors because we will retry on the next reconcile
			return worker.StatusConflict
		}
		logger.Error(err, "Failed to ensure cluster finalizer")
		return worker.StatusError
	}

	if joined, alreadyFailed := isClusterJoined(&cluster.Status); joined || alreadyFailed {
		return worker.StatusAllOK
	}

	// not joined yet and not failed, so we try to join
	logger.V(2).Info("Handle unjoined cluster")
	cluster, newCondition, newJoinPerformed, err := handleNotJoinedCluster(
		ctx,
		cluster,
		c.client,
		c.kubeClient,
		c.eventRecorder,
		c.fedSystemNamespace,
		c.clusterJoinTimeout,
	)

	needsUpdate := false
	if newCondition != nil {
		needsUpdate = true

		currentTime := metav1.Now()
		newCondition.Type = fedcorev1a1.ClusterJoined
		newCondition.LastProbeTime = currentTime

		// The condition's last transition time is updated to the current time only if
		// the status has changed.
		oldCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterJoined)
		if oldCondition != nil && oldCondition.Status == newCondition.Status {
			newCondition.LastTransitionTime = oldCondition.LastTransitionTime
		} else {
			newCondition.LastTransitionTime = currentTime
		}

		setClusterCondition(&cluster.Status, newCondition)
	}
	if newJoinPerformed != nil && *newJoinPerformed != cluster.Status.JoinPerformed {
		needsUpdate = true
		cluster.Status.JoinPerformed = *newJoinPerformed
	}

	if needsUpdate {
		var updateErr error
		if cluster, updateErr = c.client.CoreV1alpha1().FederatedClusters().UpdateStatus(
			ctx, cluster, metav1.UpdateOptions{},
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

	// trigger initial status collection if successfully joined
	if joined, alreadyFailed := isClusterJoined(&cluster.Status); joined && !alreadyFailed {
		c.statusCollectWorker.EnqueueObject(cluster)
	}

	return worker.StatusAllOK
}

func (c *FederatedClusterController) collectClusterStatus(qualifiedName common.QualifiedName) (status worker.Result) {
	logger := c.logger.WithValues("control-loop", "status-collect", "object", qualifiedName.String())
	ctx := klog.NewContext(context.TODO(), logger)
	startTime := time.Now()

	logger.V(3).Info("Start status collection")
	defer func() {
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished status collection")
	}()

	cluster, err := c.clusterLister.Get(qualifiedName.Name)
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
		if err := collectIndividualClusterStatus(ctx, cluster, c.client, c.federatedClient); err != nil {
			logger.Error(err, "Failed to collect cluster status")
			return worker.StatusError
		}
	}

	return worker.StatusAllOK
}

func ensureFinalizer(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	client fedclient.Interface,
) (*fedcorev1a1.FederatedCluster, error) {
	updated, err := finalizerutils.AddFinalizers(
		cluster,
		sets.NewString(FinalizerFederatedClusterController),
	)
	if err != nil {
		return nil, err
	}

	if updated {
		return client.CoreV1alpha1().
			FederatedClusters().
			Update(ctx, cluster, metav1.UpdateOptions{})
	}

	return cluster, nil
}

func handleTerminatingCluster(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	client fedclient.Interface,
	kubeClient kubeclient.Interface,
	eventRecorder record.EventRecorder,
	fedSystemNamespace string,
) error {
	finalizers := sets.New(cluster.GetFinalizers()...)
	if !finalizers.Has(FinalizerFederatedClusterController) {
		return nil
	}

	// we need to ensure that all other finalizers are removed before we start cleanup as other controllers may
	// still rely on the credentials to do their own cleanup
	if len(finalizers) > 1 {
		eventRecorder.Eventf(
			cluster,
			corev1.EventTypeNormal,
			EventReasonHandleTerminatingClusterBlocked,
			"waiting for other finalizers to be cleaned up",
		)

		return nil
	}

	// Only perform clean-up if we made any effectual changes to the cluster during join.
	if cluster.Status.JoinPerformed {
		clusterSecret, clusterKubeClient, err := getClusterClient(ctx, kubeClient, fedSystemNamespace, cluster)
		if err != nil {
			eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"Failed to get cluster client: %v",
				err,
			)

			return fmt.Errorf("failed to get cluster client: %w", err)
		}

		// 1. cleanup service account token from cluster secret if required

		if cluster.Spec.UseServiceAccountToken {
			var err error

			_, tokenKeyExists := clusterSecret.Data[common.ClusterServiceAccountTokenKey]
			_, caExists := clusterSecret.Data[common.ClusterServiceAccountCAKey]
			if tokenKeyExists || caExists {
				delete(clusterSecret.Data, ServiceAccountTokenKey)
				delete(clusterSecret.Data, ServiceAccountCAKey)

				_, err = kubeClient.CoreV1().
					Secrets(fedSystemNamespace).
					Update(ctx, clusterSecret, metav1.UpdateOptions{})
				if err != nil {
					return fmt.Errorf(
						"failed to remove service account info from cluster secret: %w",
						err,
					)
				}
			}
		}

		// 2. connect to cluster and perform cleanup

		err = clusterKubeClient.CoreV1().
			Namespaces().
			Delete(ctx, fedSystemNamespace, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"delete system namespace from cluster %q failed: %v, will retry later",
				cluster.Name,
				err,
			)
			return fmt.Errorf("failed to delete fed system namespace from cluster: %w", err)
		}
	}

	// we have already checked that we are the last finalizer so we can simply set finalizers to be empty
	cluster.SetFinalizers(nil)
	_, err := client.CoreV1alpha1().
		FederatedClusters().
		Update(ctx, cluster, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update cluster for finalizer removal: %w", err)
	}

	return nil
}

func getClusterClient(
	ctx context.Context,
	hostClient kubeclient.Interface,
	fedSystemNamespace string,
	cluster *fedcorev1a1.FederatedCluster,
) (*corev1.Secret, kubeclient.Interface, error) {
	restConfig := &rest.Config{Host: cluster.Spec.APIEndpoint}

	clusterSecretName := cluster.Spec.SecretRef.Name
	if clusterSecretName == "" {
		return nil, nil, fmt.Errorf("cluster secret is not set")
	}

	clusterSecret, err := hostClient.CoreV1().Secrets(fedSystemNamespace).Get(ctx, clusterSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster secret: %w", err)
	}

	if err := util.PopulateAuthDetailsFromSecret(restConfig, cluster.Spec.Insecure, clusterSecret, false); err != nil {
		return nil, nil, fmt.Errorf("cluster secret malformed: %w", err)
	}

	clusterClient, err := kubeclient.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cluster kube clientset: %w", err)
	}

	return clusterSecret, clusterClient, nil
}

func (c *FederatedClusterController) enqueueAllJoinedClusters() {
	clusters, err := c.clusterLister.List(labels.Everything())
	if err != nil {
		c.logger.Error(err, "Failed to enqueue all clusters")
	}

	for _, cluster := range clusters {
		if util.IsClusterJoined(&cluster.Status) {
			c.statusCollectWorker.EnqueueObject(cluster)
		}
	}
}
