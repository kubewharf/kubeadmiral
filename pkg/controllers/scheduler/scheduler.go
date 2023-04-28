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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicclient "k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	fedcorev1a1listers "github.com/kubewharf/kubeadmiral/pkg/client/listers/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/core"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/delayingdeliver"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/pendingcontrollers"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/worker"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

type ClusterWeight struct {
	Cluster string
	Weight  int64
}

type Scheduler struct {
	typeConfig *fedcorev1a1.FederatedTypeConfig
	name       string

	fedClient     fedclient.Interface
	dynamicClient dynamicclient.Interface

	federatedObjectClient dynamicclient.NamespaceableResourceInterface
	federatedObjectLister cache.GenericLister
	federatedObjectSynced cache.InformerSynced

	propagationPolicyLister        fedcorev1a1listers.PropagationPolicyLister
	clusterPropagationPolicyLister fedcorev1a1listers.ClusterPropagationPolicyLister
	propagationPolicySynced        cache.InformerSynced
	clusterPropagationPolicySynced cache.InformerSynced

	clusterLister fedcorev1a1listers.FederatedClusterLister
	clusterSynced cache.InformerSynced

	schedulingProfileLister fedcorev1a1listers.SchedulingProfileLister
	schedulingProfileSynced cache.InformerSynced

	webhookConfigurationSynced cache.InformerSynced
	webhookPlugins             sync.Map

	worker        worker.ReconcileWorker
	eventRecorder record.EventRecorder

	algorithm core.ScheduleAlgorithm

	metrics stats.Metrics
	logger  klog.Logger
}

func NewScheduler(
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	kubeClient kubeclient.Interface,
	fedClient fedclient.Interface,
	dynamicClient dynamicclient.Interface,
	federatedObjectInformer informers.GenericInformer,
	propagationPolicyInformer fedcorev1a1informers.PropagationPolicyInformer,
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer,
	clusterInformer fedcorev1a1informers.FederatedClusterInformer,
	schedulingProfileInformer fedcorev1a1informers.SchedulingProfileInformer,
	webhookConfigurationInformer fedcorev1a1informers.SchedulerPluginWebhookConfigurationInformer,
	metrics stats.Metrics,
	workerCount int,
) (*Scheduler, error) {
	schedulerName := fmt.Sprintf("%s-scheduler", typeConfig.GetFederatedType().Name)

	s := &Scheduler{
		typeConfig:    typeConfig,
		name:          schedulerName,
		fedClient:     fedClient,
		dynamicClient: dynamicClient,
		metrics:       metrics,
		logger:        klog.LoggerWithName(klog.Background(), schedulerName),
	}

	s.worker = worker.NewReconcileWorker(
		s.reconcile,
		worker.WorkerTiming{},
		workerCount,
		metrics,
		delayingdeliver.NewMetricTags("scheduler-worker", s.typeConfig.GetFederatedType().Kind),
	)
	s.eventRecorder = eventsink.NewDefederatingRecorderMux(kubeClient, s.name, 6)

	apiResource := typeConfig.GetFederatedType()
	s.federatedObjectClient = dynamicClient.Resource(schemautil.APIResourceToGVR(&apiResource))

	s.federatedObjectLister = federatedObjectInformer.Lister()
	s.federatedObjectSynced = federatedObjectInformer.Informer().HasSynced
	federatedObjectInformer.Informer().AddEventHandler(util.NewTriggerOnAllChanges(s.worker.EnqueueObject))

	// only required if namespaced
	if s.typeConfig.GetNamespaced() {
		s.propagationPolicyLister = propagationPolicyInformer.Lister()
		s.propagationPolicySynced = propagationPolicyInformer.Informer().HasSynced
		propagationPolicyInformer.Informer().AddEventHandler(util.NewTriggerOnGenerationChanges(s.enqueueFederatedObjectsForPolicy))
	}

	s.clusterPropagationPolicyLister = clusterPropagationPolicyInformer.Lister()
	s.clusterPropagationPolicySynced = clusterPropagationPolicyInformer.Informer().HasSynced
	clusterPropagationPolicyInformer.Informer().AddEventHandler(util.NewTriggerOnGenerationChanges(s.enqueueFederatedObjectsForPolicy))

	s.clusterLister = clusterInformer.Lister()
	s.clusterSynced = clusterInformer.Informer().HasSynced
	clusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { s.enqueueFederatedObjectsForCluster(obj.(pkgruntime.Object)) },
		DeleteFunc: func(obj interface{}) { s.enqueueFederatedObjectsForCluster(obj.(pkgruntime.Object)) },
		UpdateFunc: func(oldUntyped, newUntyped interface{}) {
			oldCluster, newCluster := oldUntyped.(*fedcorev1a1.FederatedCluster), newUntyped.(*fedcorev1a1.FederatedCluster)
			if !equality.Semantic.DeepEqual(oldCluster.Labels, newCluster.Labels) ||
				!equality.Semantic.DeepEqual(oldCluster.Spec.Taints, newCluster.Spec.Taints) {
				s.enqueueFederatedObjectsForCluster(newCluster)
			}
		},
	})

	s.schedulingProfileLister = schedulingProfileInformer.Lister()
	s.schedulingProfileSynced = schedulingProfileInformer.Informer().HasSynced

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
			s.webhookPlugins.Delete(obj.(*fedcorev1a1.SchedulerPluginWebhookConfiguration).Name)
		},
	})

	s.algorithm = core.NewSchedulerAlgorithm()

	return s, nil
}

func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Info("Starting controller")
	defer s.logger.Info("Stopping controller")

	cachesSynced := []cache.InformerSynced{
		s.federatedObjectSynced,
		s.clusterPropagationPolicySynced,
		s.clusterSynced,
		s.schedulingProfileSynced,
		s.webhookConfigurationSynced,
	}
	if s.typeConfig.GetNamespaced() {
		cachesSynced = append(cachesSynced, s.propagationPolicySynced)
	}

	if !cache.WaitForNamedCacheSync(s.name, ctx.Done(), cachesSynced...) {
		return
	}

	s.worker.Run(ctx.Done())
	<-ctx.Done()
}

func (s *Scheduler) reconcile(qualifiedName common.QualifiedName) (status worker.Result) {
	_ = s.metrics.Rate("scheduler.throughput", 1)
	keyedLogger := s.logger.WithValues("origin", "reconcile", "object", qualifiedName.String())
	ctx := klog.NewContext(context.TODO(), keyedLogger)
	startTime := time.Now()

	keyedLogger.V(3).Info("Start reconcile")
	defer func() {
		s.metrics.Duration(fmt.Sprintf("%s.latency", s.name), startTime)
		keyedLogger.V(3).WithValues("duration", time.Since(startTime), "status", status.String()).Info("Finished reconcile")
	}()

	fedObject, err := s.federatedObjectFromStore(qualifiedName)
	if err != nil && !apierrors.IsNotFound(err) {
		keyedLogger.Error(err, "Failed to get object from store")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) || fedObject.GetDeletionTimestamp() != nil {
		keyedLogger.V(3).Info("Observed object deletion")
		return worker.StatusAllOK
	}

	fedObject = fedObject.DeepCopy()

	policy, clusters, schedulingProfile, earlyReturnResult := s.prepareToSchedule(ctx, fedObject)
	if earlyReturnResult != nil {
		return *earlyReturnResult
	}

	if policy != nil {
		keyedLogger = keyedLogger.WithValues("policy", common.NewQualifiedName(policy).String())
	}
	if schedulingProfile != nil {
		keyedLogger = keyedLogger.WithValues("schedulingProfile", common.NewQualifiedName(schedulingProfile).String())
	}

	ctx = klog.NewContext(ctx, keyedLogger)
	result, earlyReturnWorkerResult := s.schedule(ctx, fedObject, policy, schedulingProfile, clusters)
	if earlyReturnWorkerResult != nil {
		return *earlyReturnWorkerResult
	}

	keyedLogger = keyedLogger.WithValues("result", result.String())
	keyedLogger.V(2).Info("Scheduling result obtained")

	auxInfo := &auxiliarySchedulingInformation{
		enableFollowerScheduling: false,
		unschedulableThreshold:   nil,
	}
	if policy != nil {
		spec := policy.GetSpec()

		auxInfo.enableFollowerScheduling = !spec.DisableFollowerScheduling
		keyedLogger = keyedLogger.WithValues("enableFollowerScheduling", auxInfo.enableFollowerScheduling)

		if autoMigration := spec.AutoMigration; autoMigration != nil {
			auxInfo.unschedulableThreshold = pointer.Duration(autoMigration.Trigger.PodUnschedulableDuration.Duration)
			keyedLogger = keyedLogger.WithValues("unschedulableThreshold", auxInfo.unschedulableThreshold.String())
		}
	}

	ctx = klog.NewContext(ctx, keyedLogger)
	return s.persistSchedulingResult(ctx, fedObject, *result, auxInfo)
}

func (s *Scheduler) prepareToSchedule(
	ctx context.Context,
	fedObject *unstructured.Unstructured,
) (
	fedcorev1a1.GenericPropagationPolicy,
	[]*fedcorev1a1.FederatedCluster,
	*fedcorev1a1.SchedulingProfile,
	*worker.Result,
) {
	keyedLogger := klog.FromContext(ctx)

	// check pending controllers

	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(fedObject, PrefixedGlobalSchedulerName); err != nil {
		keyedLogger.Error(err, "Failed to check controller dependencies")
		return nil, nil, nil, &worker.StatusError
	} else if !ok {
		keyedLogger.V(3).Info("Controller dependencies not fulfilled")
		return nil, nil, nil, &worker.StatusAllOK
	}

	// check whether to skip scheduling

	allClusters, err := s.clusterLister.List(labels.Everything())
	if err != nil {
		keyedLogger.Error(err, "Failed to get clusters from store")
		return nil, nil, nil, &worker.StatusError
	}
	clusters := make([]*fedcorev1a1.FederatedCluster, 0)
	for _, cluster := range allClusters {
		if util.IsClusterJoined(&cluster.Status) {
			clusters = append(clusters, cluster)
		}
	}

	var policy fedcorev1a1.GenericPropagationPolicy
	var schedulingProfile *fedcorev1a1.SchedulingProfile

	policyKey, hasSchedulingPolicy := MatchedPolicyKey(fedObject, s.typeConfig.GetNamespaced())

	if hasSchedulingPolicy {
		keyedLogger = keyedLogger.WithValues("policy", policyKey.String())

		if policy, err = s.policyFromStore(policyKey); err != nil {
			keyedLogger.Error(err, "Failed to find matched policy")
			if apierrors.IsNotFound(err) {
				// do not retry since the object will be reenqueued after the policy is subsequently created
				// emit an event to warn users that the assigned propagation policy does not exist
				s.eventRecorder.Eventf(
					fedObject,
					corev1.EventTypeWarning,
					EventReasonScheduleFederatedObject,
					"object propagation policy %s not found",
					policyKey.String(),
				)
				return nil, nil, nil, &worker.Result{Success: false, RequeueAfter: nil}
			}
			return nil, nil, nil, &worker.StatusError
		}

		profileName := policy.GetSpec().SchedulingProfile
		if len(profileName) > 0 {
			keyedLogger = keyedLogger.WithValues("profile", profileName)
			schedulingProfile, err = s.schedulingProfileLister.Get(profileName)
			if err != nil {
				keyedLogger.Error(err, "Failed to get scheduling profile")
				s.eventRecorder.Eventf(
					fedObject,
					corev1.EventTypeWarning,
					EventReasonScheduleFederatedObject,
					"failed to schedule object: %v",
					fmt.Errorf("failed to get scheduling profile %s: %w", profileName, err),
				)

				if apierrors.IsNotFound(err) {
					return nil, nil, nil, &worker.StatusAllOK
				}

				return nil, nil, nil, &worker.StatusError
			}
		}
	}

	triggerHash, err := s.computeSchedulingTriggerHash(fedObject, policy, clusters)
	if err != nil {
		keyedLogger.Error(err, "Failed to compute scheduling trigger hash")
		return nil, nil, nil, &worker.StatusError
	}

	triggersChanged, err := annotationutil.AddAnnotation(fedObject, SchedulingTriggerHashAnnotation, triggerHash)
	if err != nil {
		keyedLogger.Error(err, "Failed to update scheduling trigger hash")
		return nil, nil, nil, &worker.StatusError
	}

	shouldSkipScheduling := false
	if !triggersChanged {
		// scheduling triggers have not changed, skip scheduling
		shouldSkipScheduling = true
		keyedLogger.V(3).Info("Scheduling triggers not changed, skip scheduling")
	} else if len(fedObject.GetAnnotations()[common.NoSchedulingAnnotation]) > 0 {
		// skip scheduling if no-scheduling annotation is found
		shouldSkipScheduling = true
		keyedLogger.V(3).Info("No-scheduling annotation found, skip scheduling")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeNormal,
			EventReasonScheduleFederatedObject,
			"no-scheduling annotation found, skip scheduling",
		)
	}

	if shouldSkipScheduling {
		if updated, err := s.updatePendingControllers(fedObject, false); err != nil {
			keyedLogger.Error(err, "Failed to update pending controllers")
			return nil, nil, nil, &worker.StatusError
		} else if updated {
			if _, err := s.federatedObjectClient.Namespace(fedObject.GetNamespace()).Update(
				ctx, fedObject, metav1.UpdateOptions{},
			); err != nil {
				keyedLogger.Error(err, "Failed to update pending controllers")
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
	fedObject *unstructured.Unstructured,
	policy fedcorev1a1.GenericPropagationPolicy,
	schedulingProfile *fedcorev1a1.SchedulingProfile,
	clusters []*fedcorev1a1.FederatedCluster,
) (*core.ScheduleResult, *worker.Result) {
	keyedLogger := klog.FromContext(ctx)

	if policy == nil {
		// deschedule the federated object if there is no policy attached
		keyedLogger.V(2).Info("No policy specified, scheduling to no clusters")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeNormal,
			EventReasonScheduleFederatedObject,
			"no scheduling policy specified, will schedule object to no clusters",
		)

		return &core.ScheduleResult{
			SuggestedClusters: make(map[string]*int64),
		}, nil
	}

	// schedule according to matched policy
	keyedLogger.V(2).Info("Matched policy found, start scheduling")
	s.eventRecorder.Eventf(
		fedObject,
		corev1.EventTypeNormal,
		EventReasonScheduleFederatedObject,
		"scheduling policy %s specified, scheduling object",
		common.NewQualifiedName(policy).String(),
	)

	schedulingUnit, err := schedulingUnitForFedObject(s.typeConfig, fedObject, policy)
	if err != nil {
		keyedLogger.Error(err, "Failed to get scheduling unit")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to get scheduling unit: %w", err),
		)
		return nil, &worker.StatusError
	}

	framework, err := s.createFramework(schedulingProfile, s.buildFrameworkHandle())
	if err != nil {
		keyedLogger.Error(err, "Failed to construct scheduling profile")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to construct scheduling profile: %w", err),
		)

		return nil, &worker.StatusError
	}

	ctx = klog.NewContext(ctx, keyedLogger)
	result, err := s.algorithm.Schedule(ctx, framework, *schedulingUnit, clusters)
	if err != nil {
		keyedLogger.Error(err, "Failed to compute scheduling result")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to compute scheduling result: %w", err),
		)
		return nil, &worker.StatusError
	}

	return &result, nil
}

func (s *Scheduler) persistSchedulingResult(
	ctx context.Context,
	fedObject *unstructured.Unstructured,
	result core.ScheduleResult,
	auxInfo *auxiliarySchedulingInformation,
) worker.Result {
	keyedLogger := klog.FromContext(ctx)

	schedulingResultsChanged, err := s.applySchedulingResult(fedObject, result, auxInfo)
	if err != nil {
		keyedLogger.Error(err, "Failed to apply scheduling result")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to apply scheduling result: %w", err),
		)
		return worker.StatusError
	}
	pendingControllersChanged, err := s.updatePendingControllers(fedObject, schedulingResultsChanged)
	if err != nil {
		keyedLogger.Error(err, "Failed to update pending controllers")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed update pending controllers: %w", err),
		)
		return worker.StatusError
	}

	needsUpdate := schedulingResultsChanged || pendingControllersChanged
	if !needsUpdate {
		return worker.StatusAllOK
	}

	keyedLogger.V(1).Info("Updating federated object")
	if _, err := s.federatedObjectClient.Namespace(fedObject.GetNamespace()).Update(
		ctx,
		fedObject,
		metav1.UpdateOptions{},
	); err != nil {
		keyedLogger.Error(err, "Failed to update federated object")
		s.eventRecorder.Eventf(
			fedObject,
			corev1.EventTypeWarning,
			EventReasonScheduleFederatedObject,
			"failed to schedule object: %v",
			fmt.Errorf("failed to update federated object: %w", err),
		)
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	keyedLogger.V(1).Info("Updated federated object")
	s.eventRecorder.Eventf(
		fedObject,
		corev1.EventTypeNormal,
		EventReasonScheduleFederatedObject,
		"scheduling success: %s",
		result.String(),
	)

	return worker.StatusAllOK
}

// federatedObjectFromStore uses the given qualified name to retrieve a federated object from the scheduler's lister, it will help to
// resolve the object's scope and namespace based on the scheduler's type config.
func (s *Scheduler) federatedObjectFromStore(qualifiedName common.QualifiedName) (*unstructured.Unstructured, error) {
	var obj pkgruntime.Object
	var err error

	if s.typeConfig.GetNamespaced() {
		obj, err = s.federatedObjectLister.ByNamespace(qualifiedName.Namespace).Get(qualifiedName.Name)
	} else {
		obj, err = s.federatedObjectLister.Get(qualifiedName.Name)
	}

	return obj.(*unstructured.Unstructured), err
}

// policyFromStore uses the given qualified name to retrieve a policy from the scheduler's policy listers.
func (s *Scheduler) policyFromStore(qualifiedName common.QualifiedName) (fedcorev1a1.GenericPropagationPolicy, error) {
	if len(qualifiedName.Namespace) > 0 {
		return s.propagationPolicyLister.PropagationPolicies(qualifiedName.Namespace).Get(qualifiedName.Name)
	}
	return s.clusterPropagationPolicyLister.Get(qualifiedName.Name)
}

// updatePendingControllers removes the scheduler from the object's pending controller annotation. If wasModified is true (the scheduling
// result was not modified), it will additionally set the downstream processors to notify them to reconcile the changes made by the
// scheduler.
func (s *Scheduler) updatePendingControllers(fedObject *unstructured.Unstructured, wasModified bool) (bool, error) {
	return pendingcontrollers.UpdatePendingControllers(
		fedObject,
		PrefixedGlobalSchedulerName,
		wasModified,
		s.typeConfig.GetControllers(),
	)
}

type auxiliarySchedulingInformation struct {
	enableFollowerScheduling bool
	unschedulableThreshold   *time.Duration
}

// applySchedulingResult updates the federated object with the scheduling result and the enableFollowerScheduling annotation, it returns a
// bool indicating if the scheduling result has changed.
func (s *Scheduler) applySchedulingResult(
	fedObject *unstructured.Unstructured,
	result core.ScheduleResult,
	auxInfo *auxiliarySchedulingInformation,
) (bool, error) {
	objectModified := false
	clusterSet := result.ClusterSet()

	// set placements
	placementUpdated, err := util.SetPlacementClusterNames(fedObject, PrefixedGlobalSchedulerName, clusterSet)
	if err != nil {
		return false, err
	}
	objectModified = objectModified || placementUpdated

	// set replicas overrides
	desiredOverrides := map[string]int64{}
	for clusterName, replicaCount := range result.SuggestedClusters {
		if replicaCount != nil {
			desiredOverrides[clusterName] = *replicaCount
		}
	}
	overridesUpdated, err := UpdateReplicasOverride(s.typeConfig, fedObject, desiredOverrides)
	if err != nil {
		return false, err
	}
	objectModified = objectModified || overridesUpdated

	// set annotations
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
