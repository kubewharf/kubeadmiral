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

// The design and implementation of the scheduler is heavily inspired by kube-scheduler and karmada-scheduler. Kudos!

package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/core"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/annotation"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	SchedulerName = "scheduler"
)

type ClusterWeight struct {
	Cluster string
	Weight  int64
}

type Scheduler struct {
	fedClient     fedclient.Interface
	dynamicClient dynamic.Interface

	fedObjectInformer                fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer         fedcorev1a1informers.ClusterFederatedObjectInformer
	propagationPolicyInformer        fedcorev1a1informers.PropagationPolicyInformer
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer
	federatedClusterInformer         fedcorev1a1informers.FederatedClusterInformer
	schedulingProfileInformer        fedcorev1a1informers.SchedulingProfileInformer

	informerManager informermanager.InformerManager

	webhookPlugins             sync.Map
	webhookConfigurationSynced cache.InformerSynced

	worker        worker.ReconcileWorker[common.QualifiedName]
	eventRecorder record.EventRecorder

	algorithm core.ScheduleAlgorithm

	metrics stats.Metrics
	logger  klog.Logger
}

func (s *Scheduler) IsControllerReady() bool {
	return s.HasSynced()
}

func NewScheduler(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,
	dynamicClient dynamic.Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	propagationPolicyInformer fedcorev1a1informers.PropagationPolicyInformer,
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer,
	federatedClusterInformer fedcorev1a1informers.FederatedClusterInformer,
	schedulingProfileInformer fedcorev1a1informers.SchedulingProfileInformer,
	informerManager informermanager.InformerManager,
	webhookConfigurationInformer fedcorev1a1informers.SchedulerPluginWebhookConfigurationInformer,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*Scheduler, error) {
	s := &Scheduler{
		fedClient:                        fedClient,
		dynamicClient:                    dynamicClient,
		fedObjectInformer:                fedObjectInformer,
		clusterFedObjectInformer:         clusterFedObjectInformer,
		propagationPolicyInformer:        propagationPolicyInformer,
		clusterPropagationPolicyInformer: clusterPropagationPolicyInformer,
		federatedClusterInformer:         federatedClusterInformer,
		schedulingProfileInformer:        schedulingProfileInformer,
		informerManager:                  informerManager,
		webhookConfigurationSynced:       webhookConfigurationInformer.Informer().HasSynced,
		webhookPlugins:                   sync.Map{},
		metrics:                          metrics,
		logger:                           logger.WithValues("controller", SchedulerName),
	}

	s.eventRecorder = eventsink.NewDefederatingRecorderMux(kubeClient, SchedulerName, 6)
	s.worker = worker.NewReconcileWorker[common.QualifiedName](
		SchedulerName,
		nil,
		s.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	fedObjectInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChanges(
		common.NewQualifiedName,
		s.worker.Enqueue,
	))
	clusterFedObjectInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnAllChanges(
		common.NewQualifiedName,
		s.worker.Enqueue,
	))

	propagationPolicyInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnGenerationChanges(
		func(obj metav1.Object) metav1.Object { return obj },
		s.enqueueFederatedObjectsForPolicy,
	))
	clusterPropagationPolicyInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnGenerationChanges(
		func(obj metav1.Object) metav1.Object { return obj },
		s.enqueueFederatedObjectsForPolicy,
	))

	federatedClusterInformer.Informer().AddEventHandler(eventhandlers.NewTriggerOnChanges(
		func(oldCluster, curCluster *fedcorev1a1.FederatedCluster) bool {
			return !equality.Semantic.DeepEqual(oldCluster.Labels, curCluster.Labels) ||
				!equality.Semantic.DeepEqual(oldCluster.Spec.Taints, curCluster.Spec.Taints) ||
				!equality.Semantic.DeepEqual(oldCluster.Status.APIResourceTypes, curCluster.Status.APIResourceTypes)
		},
		func(cluster *fedcorev1a1.FederatedCluster) *fedcorev1a1.FederatedCluster {
			return cluster
		},
		s.enqueueFederatedObjectsForCluster,
	))

	s.webhookConfigurationSynced = webhookConfigurationInformer.Informer().HasSynced
	webhookConfigurationInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			s.cacheWebhookPlugin(obj.(*fedcorev1a1.SchedulerPluginWebhookConfiguration))
		},
		UpdateFunc: func(oldUntyped, newUntyped interface{}) {
			oldConfig := oldUntyped.(*fedcorev1a1.SchedulerPluginWebhookConfiguration)
			newConfig := newUntyped.(*fedcorev1a1.SchedulerPluginWebhookConfiguration)
			if oldConfig.Spec.URLPrefix != newConfig.Spec.URLPrefix ||
				oldConfig.Spec.HTTPTimeout != newConfig.Spec.HTTPTimeout ||
				!reflect.DeepEqual(oldConfig.Spec.TLSConfig, newConfig.Spec.TLSConfig) {
				s.cacheWebhookPlugin(newConfig)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if deleted, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				// This object might be stale but ok for our current usage.
				obj = deleted.Obj
				if obj == nil {
					return
				}
			}
			s.webhookPlugins.Delete(obj.(*fedcorev1a1.SchedulerPluginWebhookConfiguration).Name)
		},
	})

	informerManager.AddFTCUpdateHandler(func(lastObserved, latest *fedcorev1a1.FederatedTypeConfig) {
		if lastObserved == nil && latest != nil {
			s.enqueueFederatedObjectsForFTC(latest)
			return
		}
	})

	s.algorithm = core.NewSchedulerAlgorithm()

	return s, nil
}

func (s *Scheduler) HasSynced() bool {
	cachesSynced := []cache.InformerSynced{
		s.fedObjectInformer.Informer().HasSynced,
		s.clusterFedObjectInformer.Informer().HasSynced,
		s.propagationPolicyInformer.Informer().HasSynced,
		s.clusterPropagationPolicyInformer.Informer().HasSynced,
		s.federatedClusterInformer.Informer().HasSynced,
		s.informerManager.HasSynced,
		s.schedulingProfileInformer.Informer().HasSynced,
		s.webhookConfigurationSynced,
	}

	for _, synced := range cachesSynced {
		if !synced() {
			return false
		}
	}

	return true
}

func (s *Scheduler) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, s.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(SchedulerName, ctx.Done(), s.HasSynced) {
		logger.Error(nil, "Timed out waiting for cache sync")
		return
	}

	logger.Info("Caches are synced")

	s.worker.Run(ctx)
	<-ctx.Done()
}

func (s *Scheduler) reconcile(ctx context.Context, key common.QualifiedName) (status worker.Result) {
	_ = s.metrics.Rate("scheduler.throughput", 1)
	ctx, logger := logging.InjectLoggerValues(ctx, "key", key.String())

	startTime := time.Now()

	logger.V(3).Info("Start reconcile")
	defer func() {
		s.metrics.Duration(fmt.Sprintf("%s.latency", SchedulerName), startTime)
		logger.V(3).WithValues("duration", time.Since(startTime), "status", status.String()).Info("Finished reconcile")
	}()

	fedObject, err := fedobjectadapters.GetFromLister(
		s.fedObjectInformer.Lister(),
		s.clusterFedObjectInformer.Lister(),
		key.Namespace,
		key.Name,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get FederatedObject from store")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) || fedObject.GetDeletionTimestamp() != nil {
		logger.V(3).Info("Observed FederatedObject deletion")
		return worker.StatusAllOK
	}

	fedObject = fedObject.DeepCopyGenericFederatedObject()

	sourceGVK, err := fedObject.GetSpec().GetTemplateGVK()
	if err != nil {
		logger.Error(err, "Failed to get source GVK from FederatedObject")
		return worker.StatusError
	}
	ctx, logger = logging.InjectLoggerValues(ctx, "source-gvk", sourceGVK)

	ftc, exists := s.informerManager.GetResourceFTC(sourceGVK)
	if !exists {
		logger.V(3).Info("FTC for FederatedObject source type does not exist, will skip scheduling")
		return worker.StatusAllOK
	}
	ctx, logger = logging.InjectLoggerValues(ctx, "ftc", ftc.GetName())

	policy, clusters, schedulingProfile, earlyReturnResult := s.prepareToSchedule(ctx, fedObject, ftc)
	if earlyReturnResult != nil {
		return *earlyReturnResult
	}

	if policy != nil {
		ctx, logger = logging.InjectLoggerValues(ctx, "policy", common.NewQualifiedName(policy).String())
	}
	if schedulingProfile != nil {
		ctx, logger = logging.InjectLoggerValues(
			ctx,
			"schedulingProfile",
			common.NewQualifiedName(schedulingProfile).String(),
		)
	}

	result, earlyReturnWorkerResult := s.schedule(ctx, ftc, fedObject, policy, schedulingProfile, clusters)
	if earlyReturnWorkerResult != nil {
		return *earlyReturnWorkerResult
	}

	ctx, logger = logging.InjectLoggerValues(ctx, "result", result.String())
	logger.V(2).Info("Scheduling result obtained")

	auxInfo := &auxiliarySchedulingInformation{
		enableFollowerScheduling: false,
		unschedulableThreshold:   nil,
	}
	if policy != nil {
		spec := policy.GetSpec()

		auxInfo.enableFollowerScheduling = !spec.DisableFollowerScheduling
		ctx, logger = logging.InjectLoggerValues(ctx, "enableFollowerScheduling", auxInfo.enableFollowerScheduling)

		if autoMigration := spec.AutoMigration; autoMigration != nil {
			auxInfo.unschedulableThreshold = pointer.Duration(autoMigration.Trigger.PodUnschedulableDuration.Duration)
			ctx, logger = logging.InjectLoggerValues(
				ctx,
				"unschedulableThreshold",
				auxInfo.unschedulableThreshold.String(),
			)
		}
	}

	return s.persistSchedulingResult(ctx, ftc, fedObject, *result, auxInfo)
}

func (s *Scheduler) prepareToSchedule(
	ctx context.Context,
	fedObject fedcorev1a1.GenericFederatedObject,
	ftc *fedcorev1a1.FederatedTypeConfig,
) (
	fedcorev1a1.GenericPropagationPolicy,
	[]*fedcorev1a1.FederatedCluster,
	*fedcorev1a1.SchedulingProfile,
	*worker.Result,
) {
	logger := klog.FromContext(ctx)

	// check pending controllers

	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(fedObject, PrefixedGlobalSchedulerName); err != nil {
		logger.Error(err, "Failed to check controller dependencies")
		return nil, nil, nil, &worker.StatusError
	} else if !ok {
		logger.V(3).Info("Controller dependencies not fulfilled")
		return nil, nil, nil, &worker.StatusAllOK
	}

	// check whether to skip scheduling

	allClusters, err := s.federatedClusterInformer.Lister().List(labels.Everything())
	if err != nil {
		logger.Error(err, "Failed to get clusters from store")
		return nil, nil, nil, &worker.StatusError
	}
	clusters := make([]*fedcorev1a1.FederatedCluster, 0)
	for _, cluster := range allClusters {
		if clusterutil.IsClusterJoined(&cluster.Status) {
			clusters = append(clusters, cluster)
		}
	}

	var policy fedcorev1a1.GenericPropagationPolicy
	var schedulingProfile *fedcorev1a1.SchedulingProfile

	policyKey, hasSchedulingPolicy := GetMatchedPolicyKey(fedObject)

	if hasSchedulingPolicy {
		ctx, logger = logging.InjectLoggerValues(ctx, "policy", policyKey.String())

		if policy, err = s.policyFromStore(policyKey); err != nil {
			logger.Error(err, "Failed to find matched policy")
			if apierrors.IsNotFound(err) {
				// do not retry since the object will be reenqueued after the policy is subsequently created
				// emit an event to warn users that the assigned propagation policy does not exist
				s.eventRecorder.Eventf(
					fedObject,
					corev1.EventTypeWarning,
					EventReasonScheduleFederatedObject,
					"PropagationPolicy %s not found",
					policyKey.String(),
				)
				return nil, nil, nil, &worker.StatusAllOK
			}
			return nil, nil, nil, &worker.StatusError
		}

		profileName := policy.GetSpec().SchedulingProfile
		if len(profileName) > 0 {
			ctx, logger = logging.InjectLoggerValues(ctx, "profile", profileName)
			schedulingProfile, err = s.schedulingProfileInformer.Lister().Get(profileName)
			if err != nil {
				logger.Error(err, "Failed to get scheduling profile")
				s.eventRecorder.Eventf(
					fedObject,
					corev1.EventTypeWarning,
					EventReasonScheduleFederatedObject,
					"Failed to schedule object: %v",
					fmt.Errorf("failed to get scheduling profile %s: %w", profileName, err),
				)

				if apierrors.IsNotFound(err) {
					return nil, nil, nil, &worker.StatusAllOK
				}

				return nil, nil, nil, &worker.StatusError
			}
		}
	}

	triggerHash, err := s.computeSchedulingTriggerHash(ftc, fedObject, policy, clusters)
	if err != nil {
		logger.Error(err, "Failed to compute scheduling trigger hash")
		return nil, nil, nil, &worker.StatusError
	}

	triggersChanged, err := annotation.AddAnnotation(fedObject, SchedulingTriggerHashAnnotation, triggerHash)
	if err != nil {
		logger.Error(err, "Failed to update scheduling trigger hash")
		return nil, nil, nil, &worker.StatusError
	}

	shouldSkipScheduling := false
	if !triggersChanged {
		// scheduling triggers have not changed, skip scheduling
		shouldSkipScheduling = true
		logger.V(3).Info("Scheduling triggers not changed, skip scheduling")
	} else if len(fedObject.GetAnnotations()[common.NoSchedulingAnnotation]) > 0 {
		// skip scheduling if no-scheduling annotation is found
		shouldSkipScheduling = true
		logger.V(3).Info("no-scheduling annotation found, skip scheduling")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeNormal,
			EventReasonScheduleFederatedObject,
			"no-scheduling annotation found, skip scheduling",
		)
	}

	if shouldSkipScheduling {
		if updated, err := s.updatePendingControllers(ftc, fedObject, false); err != nil {
			logger.Error(err, "Failed to update pending controllers")
			return nil, nil, nil, &worker.StatusError
		} else if updated {
			if _, err := fedobjectadapters.Update(
				ctx,
				s.fedClient.CoreV1alpha1(),
				fedObject,
				metav1.UpdateOptions{},
			); err != nil {
				logger.Error(err, "Failed to update pending controllers")
				if apierrors.IsConflict(err) {
					return nil, nil, nil, &worker.StatusConflict
				}
				return nil, nil, nil, &worker.StatusError
			}
		}

		return nil, nil, nil, &worker.StatusAllOK
	}

	return policy, clusters, schedulingProfile, nil
}

func (s *Scheduler) schedule(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	policy fedcorev1a1.GenericPropagationPolicy,
	schedulingProfile *fedcorev1a1.SchedulingProfile,
	clusters []*fedcorev1a1.FederatedCluster,
) (*core.ScheduleResult, *worker.Result) {
	logger := klog.FromContext(ctx)

	if policy == nil {
		// deschedule the federated object if there is no policy attached
		logger.V(2).Info("No policy specified, scheduling to no clusters")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeNormal,
			EventReasonScheduleFederatedObject,
			"No scheduling policy specified, will schedule object to no clusters",
		)

		return &core.ScheduleResult{SuggestedClusters: make(map[string]*int64)}, nil
	}

	// schedule according to matched policy
	logger.V(2).Info("Matched policy found, start scheduling")
	s.eventRecorder.Eventf(
		fedObject,
		corev1.EventTypeNormal,
		EventReasonScheduleFederatedObject,
		"Scheduling policy %s specified, scheduling object",
		common.NewQualifiedName(policy).String(),
	)

	schedulingUnit, err := schedulingUnitForFedObject(ftc, fedObject, policy)
	if err != nil {
		logger.Error(err, "Failed to get scheduling unit")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"Failed to schedule object: %v",
			fmt.Errorf("failed to get scheduling unit: %w", err),
		)
		return nil, &worker.StatusError
	}

	framework, err := s.createFramework(schedulingProfile, s.buildFrameworkHandle())
	if err != nil {
		logger.Error(err, "Failed to construct scheduling profile")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"Failed to schedule object: %v",
			fmt.Errorf("failed to construct scheduling profile: %w", err),
		)

		return nil, &worker.StatusError
	}

	ctx = klog.NewContext(ctx, logger)
	result, err := s.algorithm.Schedule(ctx, framework, *schedulingUnit, clusters)
	if err != nil {
		logger.Error(err, "Failed to compute scheduling result")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"Failed to schedule object: %v",
			fmt.Errorf("failed to compute scheduling result: %w", err),
		)
		return nil, &worker.StatusError
	}

	return &result, nil
}

func (s *Scheduler) persistSchedulingResult(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	result core.ScheduleResult,
	auxInfo *auxiliarySchedulingInformation,
) worker.Result {
	logger := klog.FromContext(ctx)

	schedulingResultsChanged, err := s.applySchedulingResult(ftc, fedObject, result, auxInfo)
	if err != nil {
		logger.Error(err, "Failed to apply scheduling result")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to apply scheduling result: %w", err),
		)
		return worker.StatusError
	}
	_, err = s.updatePendingControllers(ftc, fedObject, schedulingResultsChanged)
	if err != nil {
		logger.Error(err, "Failed to update pending controllers")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed update pending controllers: %w", err),
		)
		return worker.StatusError
	}

	// We always update the federated object because the fact that scheduling even occurred minimally implies that the
	// scheduling trigger hash must have changed.
	logger.V(1).Info("Updating federated object")
	if _, err := fedobjectadapters.Update(
		ctx,
		s.fedClient.CoreV1alpha1(),
		fedObject,
		metav1.UpdateOptions{},
	); err != nil {
		logger.Error(err, "Failed to update federated object")
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to update federated object: %w", err),
		)
		return worker.StatusError
	}

	logger.V(1).Info("Updated federated object")
	s.eventRecorder.Eventf(
		fedObject,
		corev1.EventTypeNormal,
		EventReasonScheduleFederatedObject,
		"scheduling success: %s",
		result.String(),
	)

	return worker.StatusAllOK
}

// updatePendingControllers removes the scheduler from the object's pending controller annotation. If wasModified is
// true (the scheduling result was not modified), it will additionally set the downstream processors to notify them to
// reconcile the changes made by the scheduler.
func (s *Scheduler) updatePendingControllers(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	wasModified bool,
) (bool, error) {
	return pendingcontrollers.UpdatePendingControllers(
		fedObject,
		PrefixedGlobalSchedulerName,
		wasModified,
		ftc.GetControllers(),
	)
}

type auxiliarySchedulingInformation struct {
	enableFollowerScheduling bool
	unschedulableThreshold   *time.Duration
}

// applySchedulingResult updates the federated object with the scheduling result and the enableFollowerScheduling
// annotation, it returns a bool indicating if the scheduling result has changed.
func (s *Scheduler) applySchedulingResult(
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
	result core.ScheduleResult,
	auxInfo *auxiliarySchedulingInformation,
) (bool, error) {
	objectModified := false
	clusterSet := result.ClusterSet()

	// 1. Set placements

	placementUpdated := fedObject.GetSpec().SetControllerPlacement(PrefixedGlobalSchedulerName, sets.List(clusterSet))
	objectModified = objectModified || placementUpdated

	// 2. Set replicas overrides

	desiredOverrides := map[string]int64{}
	for clusterName, replicaCount := range result.SuggestedClusters {
		if replicaCount != nil {
			desiredOverrides[clusterName] = *replicaCount
		}
	}
	overridesUpdated, err := UpdateReplicasOverride(ftc, fedObject, desiredOverrides)
	if err != nil {
		return false, err
	}
	objectModified = objectModified || overridesUpdated

	// 3. Set annotations

	annotations := fedObject.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 2)
	}
	annotationsModified := false

	enableFollowerSchedulingAnnotationValue := common.AnnotationValueTrue
	if !auxInfo.enableFollowerScheduling {
		enableFollowerSchedulingAnnotationValue = common.AnnotationValueFalse
	}
	if annotations[common.EnableFollowerSchedulingAnnotation] != enableFollowerSchedulingAnnotationValue {
		annotations[common.EnableFollowerSchedulingAnnotation] = enableFollowerSchedulingAnnotationValue
		annotationsModified = true
	}

	if auxInfo.unschedulableThreshold == nil {
		if _, ok := annotations[common.PodUnschedulableThresholdAnnotation]; ok {
			delete(annotations, common.PodUnschedulableThresholdAnnotation)
			annotationsModified = true
		}
	} else {
		unschedulableThresholdAnnotationValue := auxInfo.unschedulableThreshold.String()
		if annotations[common.PodUnschedulableThresholdAnnotation] != unschedulableThresholdAnnotationValue {
			annotations[common.PodUnschedulableThresholdAnnotation] = unschedulableThresholdAnnotationValue
			annotationsModified = true
		}
	}

	if annotationsModified {
		fedObject.SetAnnotations(annotations)
		objectModified = true
	}

	return objectModified, nil
}

func (s *Scheduler) enqueueFederatedObjectsForPolicy(policy metav1.Object) {
	policyAccessor, ok := policy.(fedcorev1a1.GenericPropagationPolicy)
	if !ok {
		s.logger.Error(
			fmt.Errorf("policy is not a valid type (%T)", policy),
			"Failed to enqueue federated object for policy",
		)
		return
	}

	policyKey := common.NewQualifiedName(policyAccessor)
	logger := s.logger.WithValues("policy", policyKey.String())
	logger.V(2).Info("Enqueue FederatedObjects and ClusterFederatedObjects for policy")

	allObjects := []metav1.Object{}

	if len(policyKey.Namespace) > 0 {
		// If the policy is namespaced, we only need to scan FederatedObjects in the same namespace.
		fedObjects, err := s.fedObjectInformer.Lister().FederatedObjects(policyKey.Namespace).List(labels.Everything())
		if err != nil {
			s.logger.Error(err, "Failed to enqueue FederatedObjects for policy")
			return
		}
		for _, obj := range fedObjects {
			allObjects = append(allObjects, obj)
		}
	} else {
		// If the policy is cluster-scoped, we need to scan all FederatedObjects and ClusterFederatedObjects
		fedObjects, err := s.fedObjectInformer.Lister().List(labels.Everything())
		if err != nil {
			s.logger.Error(err, "Failed to enqueue FederatedObjects for policy")
			return
		}
		for _, obj := range fedObjects {
			allObjects = append(allObjects, obj)
		}

		clusterFedObjects, err := s.clusterFedObjectInformer.Lister().List(labels.Everything())
		if err != nil {
			s.logger.Error(err, "Failed to enqueue ClusterFederatedObjects for policy")
			return
		}
		for _, obj := range clusterFedObjects {
			allObjects = append(allObjects, obj)
		}
	}

	for _, obj := range allObjects {
		if policyKey, found := GetMatchedPolicyKey(obj); !found {
			continue
		} else if policyKey.Name == policyAccessor.GetName() && policyKey.Namespace == policyAccessor.GetNamespace() {
			s.worker.EnqueueObject(obj)
		}
	}
}

func (s *Scheduler) enqueueFederatedObjectsForCluster(cluster *fedcorev1a1.FederatedCluster) {
	logger := s.logger.WithValues("cluster", cluster.GetName())

	if !clusterutil.IsClusterJoined(&cluster.Status) {
		s.logger.WithValues("cluster", cluster.Name).
			V(3).
			Info("Skip enqueue federated objects for cluster, cluster not joined")
		return
	}

	logger.V(2).Info("Enqueue federated objects for cluster")

	fedObjects, err := s.fedObjectInformer.Lister().List(labels.Everything())
	if err != nil {
		s.logger.Error(err, "Failed to enquue FederatedObjects for policy")
		return
	}
	for _, obj := range fedObjects {
		s.worker.EnqueueObject(obj)
	}
	clusterFedObjects, err := s.clusterFedObjectInformer.Lister().List(labels.Everything())
	if err != nil {
		s.logger.Error(err, "Failed to enquue ClusterFederatedObjects for policy")
		return
	}
	for _, obj := range clusterFedObjects {
		s.worker.EnqueueObject(obj)
	}
}

func (s *Scheduler) enqueueFederatedObjectsForFTC(ftc *fedcorev1a1.FederatedTypeConfig) {
	logger := s.logger.WithValues("ftc", ftc.GetName())

	logger.V(2).Info("Enqueue federated objects for FTC")

	allObjects := []fedcorev1a1.GenericFederatedObject{}
	fedObjects, err := s.fedObjectInformer.Lister().List(labels.Everything())
	if err != nil {
		s.logger.Error(err, "Failed to enquue FederatedObjects for policy")
		return
	}
	for _, obj := range fedObjects {
		allObjects = append(allObjects, obj)
	}
	clusterFedObjects, err := s.clusterFedObjectInformer.Lister().List(labels.Everything())
	if err != nil {
		s.logger.Error(err, "Failed to enquue ClusterFederatedObjects for policy")
		return
	}
	for _, obj := range clusterFedObjects {
		allObjects = append(allObjects, obj)
	}

	for _, obj := range allObjects {
		sourceGVK, err := obj.GetSpec().GetTemplateGVK()
		if err != nil {
			s.logger.Error(err, "Failed to get source GVK from FederatedObject, will not enqueue")
			continue
		}
		if sourceGVK == ftc.GetSourceTypeGVK() {
			s.worker.EnqueueObject(obj)
		}
	}
}

// policyFromStore uses the given qualified name to retrieve a policy from the scheduler's policy listers.
func (s *Scheduler) policyFromStore(qualifiedName common.QualifiedName) (fedcorev1a1.GenericPropagationPolicy, error) {
	if len(qualifiedName.Namespace) > 0 {
		return s.propagationPolicyInformer.Lister().PropagationPolicies(qualifiedName.Namespace).Get(qualifiedName.Name)
	}
	return s.clusterPropagationPolicyInformer.Lister().Get(qualifiedName.Name)
}
