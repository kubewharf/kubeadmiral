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
	"sync"
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

	resourceCollectors sync.Map
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
		logger:             klog.LoggerWithName(klog.Background(), ControllerName),
		resourceCollectors: sync.Map{},
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
		worker.WorkerTiming{},
		workerCount,
		metrics,
		delayingdeliver.NewMetricTags("federatedcluster-worker", "FederatedCluster"),
	)

	c.statusCollectWorker = worker.NewReconcileWorker(
		c.collectClusterStatus,
		worker.WorkerTiming{
			Interval:       50 * time.Millisecond,
			InitialBackoff: 50 * time.Millisecond,
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
	key := qualifiedName.String()
	keyedLogger := c.logger.WithValues("control-loop", "reconcile", "key", key)
	startTime := time.Now()

	keyedLogger.Info("Start reconcile")
	defer c.metrics.Duration("federated-cluster-controller.latency", startTime)
	defer func() {
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status.String()).Info("Finished reconcile")
	}()

	cluster, err := c.clusterLister.Get(qualifiedName.Name)
	if err != nil && apierrors.IsNotFound(err) {
		keyedLogger.Info("Observed cluster deletion")
		return worker.StatusAllOK
	}
	if err != nil {
		keyedLogger.Error(err, "Failed to get cluster from store")
		return worker.StatusError
	}
	cluster = cluster.DeepCopy()

	if cluster.GetDeletionTimestamp() != nil {
		keyedLogger.Info("Handle terminating cluster")
		err := c.handleTerminatingCluster(cluster)
		if err != nil {
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			keyedLogger.Error(err, "Failed to handle terminating cluster")
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	if cluster, err = ensureFinalizer(cluster, c.client); err != nil {
		if apierrors.IsConflict(err) {
			// Ignore IsConflict errors because we will retry on the next reconcile
			return worker.StatusConflict
		}
		keyedLogger.Error(err, "Failed to ensure cluster finalizer")
		return worker.StatusError
	}

	joined, alreadyFailed := isClusterJoined(&cluster.Status)
	if !joined && !alreadyFailed {
		// not joined yet and not failed, so we try to join
		keyedLogger.Info("Handle unjoined cluster")
		if cluster, err = handleNotJoinedCluster(
			cluster,
			c.client,
			c.kubeClient,
			c.eventRecorder,
			c.fedSystemNamespace,
			keyedLogger.V(6).WithValues("process", "cluster-join"),
			c.clusterJoinTimeout,
		); err != nil {
			keyedLogger.Error(err, "Failed to handle unjoined cluster")
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}

		// trigger initial status collection if successfully joined
		if joined, alreadyFailed := isClusterJoined(&cluster.Status); joined && !alreadyFailed {
			c.statusCollectWorker.EnqueueObject(cluster)
		}
	}

	return worker.StatusAllOK
}

func (c *FederatedClusterController) collectClusterStatus(qualifiedName common.QualifiedName) (status worker.Result) {
	key := qualifiedName.String()
	keyedLogger := c.logger.WithValues("control-loop", "status-collect", "key", key)
	startTime := time.Now()

	keyedLogger.Info("Start status collection")
	defer func() {
		keyedLogger.WithValues("duration", time.Since(startTime), "status", status.String()).Info("Finished status collection")
	}()

	cluster, err := c.clusterLister.Get(qualifiedName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get cluster from store")
		return worker.StatusError
	}
	if err != nil || cluster.GetDeletionTimestamp() != nil {
		keyedLogger.Info("Observed cluster deletion")
		c.deleteResourceCollectorForCluster(qualifiedName.Name)
		return worker.StatusAllOK
	}

	cluster = cluster.DeepCopy()
	if shouldCollectClusterStatus(cluster, c.clusterHealthCheckConfig.Period) {
		if err := c.collectIndividualClusterStatus(context.TODO(), cluster); err != nil {
			keyedLogger.Error(err, "Failed to collect cluster status")
			return worker.StatusError
		}
	}

	return worker.StatusAllOK
}

func ensureFinalizer(
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
			Update(context.TODO(), cluster, metav1.UpdateOptions{})
	}

	return cluster, nil
}

func (c *FederatedClusterController) handleTerminatingCluster(cluster *fedcorev1a1.FederatedCluster) error {
	// we also enqueue to statusCollectWorker in order to perform necessary cleanup
	c.statusCollectWorker.Enqueue(common.NewQualifiedName(cluster))

	finalizers := sets.New(cluster.GetFinalizers()...)
	if !finalizers.Has(FinalizerFederatedClusterController) {
		return nil
	}

	// we need to ensure that all other finalizers are removed before we start cleanup as other controllers may
	// still rely on the credentials to do their own cleanup
	if len(finalizers) > 1 {
		c.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeNormal,
			EventReasonHandleTerminatingClusterBlocked,
			"waiting for other finalizers to be cleaned up",
		)

		return nil
	}

	requireCleanup := true
	// there are two cases where cleanup is not required:
	// 1. when the cluster has no ClusterJoin condition (the cluster did not even make it past the first join step)
	// 2. when the cluster is Unjoinable (it was already part of another federation)
	joinedCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterJoined)
	if joinedCondition == nil {
		requireCleanup = false
	} else if joinedCondition.Reason != nil && *joinedCondition.Reason == ClusterUnjoinableReason {
		requireCleanup = false
	}

	if requireCleanup {
		clusterSecretName := cluster.Spec.SecretRef.Name
		if clusterSecretName == "" {
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"cluster %q secret is not set",
				cluster.Name,
			)

			return fmt.Errorf("cluster secret is not set")
		}

		clusterSecret, err := c.kubeClient.CoreV1().
			Secrets(c.fedSystemNamespace).
			Get(context.TODO(), clusterSecretName, metav1.GetOptions{})
		if err != nil {
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"cluster %q secret %q not found",
				cluster.Name,
				clusterSecretName,
			)
			return fmt.Errorf("failed to get cluster secret: %w", err)
		}

		// 1. cleanup service account token from cluster secret if required

		if cluster.Spec.UseServiceAccountToken {
			var err error

			_, tokenKeyExists := clusterSecret.Data[common.ClusterServiceAccountTokenKey]
			_, caExists := clusterSecret.Data[common.ClusterServiceAccountCAKey]
			if tokenKeyExists || caExists {
				delete(clusterSecret.Data, ServiceAccountTokenKey)
				delete(clusterSecret.Data, ServiceAccountCAKey)

				clusterSecret, err = c.kubeClient.CoreV1().
					Secrets(c.fedSystemNamespace).
					Update(context.TODO(), clusterSecret, metav1.UpdateOptions{})
				if err != nil {
					return fmt.Errorf(
						"failed to remove service account info from cluster secret: %w",
						err,
					)
				}
			}
		}

		// 2. connect to cluster and perform cleanup

		restConfig := &rest.Config{Host: cluster.Spec.APIEndpoint}

		if err := util.PopulateAuthDetailsFromSecret(restConfig, cluster.Spec.Insecure, clusterSecret, false); err != nil {
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning,
				EventReasonHandleTerminatingClusterFailed,
				"cluster %q secret %q is malformed: %v",
				cluster.Name,
				clusterSecret.Name,
				err.Error(),
			)
			return fmt.Errorf("cluster secret malformed: %w", err)
		}

		clusterKubeClient, err := kubeclient.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("failed to create cluster kube clientset: %w", err)
		}

		err = clusterKubeClient.CoreV1().
			Namespaces().
			Delete(context.TODO(), c.fedSystemNamespace, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			c.eventRecorder.Eventf(
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
	_, err := c.client.CoreV1alpha1().
		FederatedClusters().
		Update(context.TODO(), cluster, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update cluster for finalizer removal: %w", err)
	}

	return nil
}

func (c *FederatedClusterController) enqueueAllJoinedClusters() {
	clusters, err := c.clusterLister.List(labels.Everything())
	if err != nil {
		c.logger.Error(err, "Failed to enqueue all clusters")
	}

	joinedClusters := map[string]*fedcorev1a1.FederatedCluster{}
	for _, cluster := range clusters {
		if util.IsClusterJoined(&cluster.Status) {
			joinedClusters[cluster.Name] = cluster
		}
	}

	for _, cluster := range joinedClusters {
		c.statusCollectWorker.EnqueueObject(cluster)
	}
}
