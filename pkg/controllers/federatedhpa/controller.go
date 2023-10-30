package federatedhpa

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/bijection"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	FederatedHPAControllerName  = "federated-hpa"
	FederatedHPAMode            = "hpa-mode"
	FederatedHPAModeFederation  = "federation"
	FederatedHPAModeDistributed = "distributed"
	FederatedHPAModeDefault     = ""
)

type Resource struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

type FederatedHPAController struct {
	name string

	informerManager                  informermanager.InformerManager
	fedObjectInformer                fedcorev1a1informers.FederatedObjectInformer
	propagationPolicyInformer        fedcorev1a1informers.PropagationPolicyInformer
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer

	kubeClient    kubernetes.Interface
	fedClient     fedclient.Interface
	dynamicClient dynamic.Interface

	worker worker.ReconcileWorker[Resource]

	scaleTargetRefMapping map[schema.GroupVersionKind]string               // hpa 的 scaleTargetRef 的路径
	workloadHPAMapping    *bijection.OneToManyRelation[Resource, Resource] // workload 和 HPA 的1对多映射【多个hpa可以指向同一workload，尽管会冲突，但是无法拦截】
	ppWorkloadMapping     *bijection.OneToManyRelation[Resource, Resource] // PP 和 HPA有关联的workload 的1对多映射【多个workload可以被同一个PP管理】

	eventRecorder record.EventRecorder

	metrics stats.Metrics
	logger  klog.Logger
}

func NewFederatedHPAController(
	kubeClient kubernetes.Interface,
	fedClient fedclient.Interface,
	dynamicClient dynamic.Interface,
	informerManager informermanager.InformerManager,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	propagationPolicyInformer fedcorev1a1informers.PropagationPolicyInformer,
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer,
	metrics stats.Metrics,
	logger klog.Logger,
	workerCount int,
) (*FederatedHPAController, error) {
	f := &FederatedHPAController{
		name: FederatedHPAControllerName,

		informerManager:                  informerManager,
		fedObjectInformer:                fedObjectInformer,
		propagationPolicyInformer:        propagationPolicyInformer,
		clusterPropagationPolicyInformer: clusterPropagationPolicyInformer,

		kubeClient:    kubeClient,
		fedClient:     fedClient,
		dynamicClient: dynamicClient,

		metrics:       metrics,
		logger:        logger.WithValues("controller", FederatedHPAControllerName),
		eventRecorder: eventsink.NewDefederatingRecorderMux(kubeClient, FederatedHPAControllerName, 6),
	}

	f.worker = worker.NewReconcileWorker[Resource](
		"fed-hpa-controller",
		f.reconcile,
		worker.RateLimiterOptions{},
		workerCount,
		metrics,
	)

	predicate := func(old, cur metav1.Object) bool {
		oldPP, ok := old.(fedcorev1a1.GenericPropagationPolicy)
		if !ok {
			return false
		}
		newPP, ok := cur.(fedcorev1a1.GenericPropagationPolicy)
		if !ok {
			return false
		}
		return oldPP.GetSpec().SchedulingMode != newPP.GetSpec().SchedulingMode ||
			oldPP.GetSpec().DisableFollowerScheduling != newPP.GetSpec().DisableFollowerScheduling
	}

	if _, err := fedObjectInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnGenerationChanges(f.enqueueFedHPAForFederatedObjects),
	); err != nil {
		return nil, err
	}
	if _, err := clusterFedObjectInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnGenerationChanges(f.enqueueFedHPAForFederatedObjects),
	); err != nil {
		return nil, err
	}

	if _, err := propagationPolicyInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnChanges(predicate, f.enqueueFedHPAForPropagationPolicy),
	); err != nil {
		return nil, err
	}
	if _, err := clusterPropagationPolicyInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnChanges(predicate, f.enqueueFedHPAForPropagationPolicy),
	); err != nil {
		return nil, err
	}

	if err := informerManager.AddFTCUpdateHandler(func(lastObserved, latest *fedcorev1a1.FederatedTypeConfig) {
		f.handleFTCUpdate(latest)
	}); err != nil {
		return nil, err
	}

	return f, nil
}

func (f *FederatedHPAController) HasSynced() bool {
	return f.informerManager.HasSynced() && f.fedObjectInformer.Informer().HasSynced() &&
		f.clusterPropagationPolicyInformer.Informer().HasSynced() &&
		f.propagationPolicyInformer.Informer().HasSynced()
}

func (f *FederatedHPAController) IsControllerReady() bool {
	return f.HasSynced()
}

func (f *FederatedHPAController) Run(ctx context.Context) {
	ctx, logger := logging.InjectLogger(ctx, f.logger)

	logger.Info("Starting controller")
	defer logger.Info("Stopping controller")

	if !cache.WaitForNamedCacheSync(FederatedHPAControllerName, ctx.Done(), f.HasSynced) {
		logger.Error(nil, "Timed out waiting for cache sync")
		return
	}

	logger.Info("Caches are synced")

	f.worker.Run(ctx)
	<-ctx.Done()
}

func (f *FederatedHPAController) reconcile(ctx context.Context, key Resource) (status worker.Result) {
	f.metrics.Counter(metrics.FederateControllerThroughput, 1)
	ctx, logger := logging.InjectLoggerValues(ctx, "source-object", key.QualifiedName().String(), "gvk", key.gvk)

	startTime := time.Now()

	logger.V(3).Info("Start reconcile")
	defer func() {
		f.metrics.Duration(metrics.FederateHPAControllerLatency, startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	// 获取联邦层的 hpa
	ftc, exists := f.informerManager.GetResourceFTC(key.gvk)
	if !exists {
		// This could happen if:
		// 1) The InformerManager is not yet up-to-date.
		// 2) We received an event from a FederatedObject without a corresponding FTC.
		//
		// For case 1, when the InformerManager becomes up-to-date, all the source objects will be enqueued once anyway,
		// so it is safe to skip processing this time round. We do not have to process orphaned FederatedObjects.
		// For case 2, the federate controller does not have to process FederatedObjects without a corresponding FTC.
		return worker.StatusAllOK
	}

	sourceGVR := ftc.GetSourceTypeGVR()
	ctx, logger = logging.InjectLoggerValues(ctx, "ftc", ftc.Name, "gvr", sourceGVR)

	lister, hasSynced, exists := c.informerManager.GetResourceLister(key.gvk)
	if !exists {
		// Once again, this could happen if:
		// 1) The InformerManager is not yet up-to-date.
		// 2) We received an event from a FederatedObject without a corresponding FTC.
		//
		// See above comment for an explanation of the handling logic.
		return worker.StatusAllOK
	}
	if !hasSynced() {
		// If lister is not yet synced, simply reenqueue after a short delay.
		logger.V(3).Info("Lister for source type not yet synced, will reenqueue")
		return worker.Result{
			Success:      true,
			RequeueAfter: pointer.Duration(c.cacheSyncRateLimiter.When(key)),
		}
	}
	c.cacheSyncRateLimiter.Forget(key)

	sourceUns, err := lister.Get(key.QualifiedName().String())
	if err != nil && apierrors.IsNotFound(err) {
		logger.V(3).Info("No source object found, skip federating")
		return worker.StatusAllOK
	}
	if err != nil {
		logger.Error(err, "Failed to get source object from store")
		return worker.StatusError
	}
	sourceObject := sourceUns.(*unstructured.Unstructured).DeepCopy()

	fedObjectName := naming.GenerateFederatedObjectName(sourceObject.GetName(), ftc.Name)
	ctx, logger = logging.InjectLoggerValues(ctx, "federated-object", fedObjectName)

	fedObject, err := fedobjectadapters.GetFromLister(
		c.fedObjectInformer.Lister(),
		c.clusterFedObjectInformer.Lister(),
		sourceObject.GetNamespace(),
		fedObjectName,
	)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get federated object from store")
		return worker.StatusError
	}
	if apierrors.IsNotFound(err) {
		fedObject = nil
	} else {
		fedObject = fedObject.DeepCopyGenericFederatedObject()
	}

	hpa, err := f.getTargetResource(key)
	hpaFO, err := f.getFOFromResource(key)

	// 获取hpa指向的workload
	scaleTargetRef, err := f.getScaleTargetRef(hpa)
	// 查看hpa指向的workload是否改变
	newWrokload := scaleTargetRefToResource(scaleTargetRef, key.namespace)
	oldWorkload, exist := f.workloadHPAMapping.LookupByT2(key)
	if exist {
		f.workloadHPAMapping.DeleteT2(key)        // 老的workload存在则删除老的
		f.ppWorkloadMapping.DeleteT2(oldWorkload) // 同样删除老workload及其关联的PP
	}
	// 添加新的workload关系
	if err := f.workloadHPAMapping.Add(newWrokload, key); err != nil {
		return worker.StatusError
	}

	// 获取workload的 fo
	workloadFO, err := f.getFOFromResource(newWrokload)
	workloadExist := true
	if err != nil {
		if !errors.IsNotFound(err) {
			return worker.StatusError
		}
		workloadExist = false
	}
	// 从workload的fo上获取pp
	var pp fedcorev1a1.GenericPropagationPolicy
	if workloadExist {
		newPPResource := getPPFromFo(workloadFO)                // 当前workload关联的pp
		_, exist := f.ppWorkloadMapping.LookupByT2(newWrokload) //workload之前关联的PP
		if exist {                                              // 存在老的PP则清除，因为可能已经过时
			f.ppWorkloadMapping.DeleteT2(newWrokload)
		}
		if newPPResource != nil {
			// 存在新PP则写入新关系
			if err := f.ppWorkloadMapping.Add(newPPResource, newWrokload); err != nil {
				return worker.StatusError
			}

			pp, err = f.getPP(newPPResource) // 获取pp内容
			if err != nil {
				if errors.IsNotFound(err) {
					return worker.StatusAllOK
				}
				return worker.StatusError
			}
		}
	}

	switch hpa.GetLabels()[FederatedHPAMode] {
	case FederatedHPAModeFederation:
		if err := f.addPendingController(hpaFO, "fed-hpa-controller"); err != nil {
			return worker.StatusError
		}
		if !workloadExist || isDividedPP(pp) {
			if err := f.addFedHPAAnnotation(hpa, "fed-hpa-enabled", "true"); err != nil {
				return worker.StatusError
			}
			return worker.StatusAllOK
		}
		if err := f.removeFedHPAAnnotation(hpa, "fed-hpa-enabled"); err != nil {
			return worker.StatusError
		}
		return worker.StatusAllOK

	case FederatedHPAModeDistributed, FederatedHPAModeDefault:
		if err := f.removeFedHPAAnnotation(hpa, "fed-hpa-enabled"); err != nil {
			return worker.StatusError
		}
		if !workloadExist || !isDividedPP(pp) &&
			isFollowerEnabled(pp) &&
			isWorkloadRetained(workloadFO) &&
			doseHPAFollowTheWorkload(hpa, workloadFO) {
			if err := f.removeFedHPAAnnotation(hpa, "fed-hpa-enabled"); err != nil {
				return worker.StatusError
			}
			if err := f.removePendingController(hpaFO, "fed-hpa-controller"); err != nil {
				return worker.StatusError
			}
			return worker.StatusAllOK
		}
		if err := f.addPendingController(hpaFO, "fed-hpa-controller"); err != nil {
			return worker.StatusError
		}
		return worker.StatusAllOK
	}

	return worker.StatusAllOK
}

func (f *FederatedHPAController) enqueueFedHPAForFederatedObjects(fo metav1.Object) {
	key := ObjectToResource(fo)
	if f.isHPAType(fo) {
		f.worker.Enqueue(key)
		return
	}

	if hpas, exist := f.workloadHPAMapping.LookupByT1(key); exist {
		for hpa := range hpas {
			f.worker.Enqueue(hpa)
		}
	}
}

func (f *FederatedHPAController) enqueueFedHPAForPropagationPolicy(policy metav1.Object) {
	key := ObjectToResource(policy)

	if workloads, exist := f.ppWorkloadMapping.LookupByT1(key); exist {
		for workload := range workloads {
			if hpas, exist := f.workloadHPAMapping.LookupByT1(workload); exist {
				for hpa := range hpas {
					f.worker.Enqueue(hpa)
				}
			}
		}
	}
}

func (f *FederatedHPAController) handleFTCUpdate(ftc *fedcorev1a1.FederatedTypeConfig) {
	resourceGVK := schema.GroupVersionKind{
		Group:   ftc.Spec.SourceType.Group,
		Version: ftc.Spec.SourceType.Version,
		Kind:    ftc.Spec.SourceType.Kind,
	}

	if ftc == nil {
		delete(f.scaleTargetRefMapping, resourceGVK)
	} else {
		// todo: 解析scaleTargetRef
	}
}
