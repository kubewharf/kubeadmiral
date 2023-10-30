package federatedhpa

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/bijection"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventsink"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/naming"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	FederatedHPAControllerName         = "federatedhpa-controller"
	PrefixedFederatedHPAControllerName = common.DefaultPrefix + FederatedHPAControllerName

	FederatedHPAMode            = "hpa-mode"
	FederatedHPAModeFederation  = "federation"
	FederatedHPAModeDistributed = "distributed"
	FederatedHPAModeDefault     = ""
)

type FederatedHPAController struct {
	name string

	informerManager                  informermanager.InformerManager
	fedObjectInformer                fedcorev1a1informers.FederatedObjectInformer
	propagationPolicyInformer        fedcorev1a1informers.PropagationPolicyInformer
	clusterPropagationPolicyInformer fedcorev1a1informers.ClusterPropagationPolicyInformer

	fedClient     fedclient.Interface
	dynamicClient dynamic.Interface

	worker               worker.ReconcileWorker[Resource]
	cacheSyncRateLimiter workqueue.RateLimiter

	scaleTargetRefMapping map[schema.GroupVersionKind]string
	workloadHPAMapping    *bijection.OneToManyRelation[Resource, Resource]
	ppWorkloadMapping     *bijection.OneToManyRelation[Resource, Resource]

	metrics       stats.Metrics
	logger        klog.Logger
	eventRecorder record.EventRecorder
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

		fedClient:     fedClient,
		dynamicClient: dynamicClient,

		cacheSyncRateLimiter: workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 10*time.Second),

		scaleTargetRefMapping: map[schema.GroupVersionKind]string{},
		workloadHPAMapping:    bijection.NewOneToManyRelation[Resource, Resource](),
		ppWorkloadMapping:     bijection.NewOneToManyRelation[Resource, Resource](),

		metrics:       metrics,
		logger:        logger.WithValues("controller", FederatedHPAControllerName),
		eventRecorder: eventsink.NewDefederatingRecorderMux(kubeClient, FederatedHPAControllerName, 6),
	}

	f.worker = worker.NewReconcileWorker[Resource](
		FederatedHPAControllerName,
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
		eventhandlers.NewTriggerOnAllChanges(f.enqueueFedHPAForFederatedObjects),
	); err != nil {
		return nil, err
	}
	if _, err := clusterFedObjectInformer.Informer().AddEventHandler(
		eventhandlers.NewTriggerOnAllChanges(f.enqueueFedHPAForFederatedObjects),
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
		if lastObserved == nil && latest != nil ||
			lastObserved != nil && latest != nil && isHPAFTCAnnoChanged(lastObserved, latest) {
			f.enqueueFederatedObjectsForFTC(latest)
		}
	}); err != nil {
		return nil, err
	}

	return f, nil
}

func (f *FederatedHPAController) enqueueFedHPAForFederatedObjects(fo metav1.Object) {
	key, err := fedObjectToSourceObjectResource(fo)
	if err != nil {
		f.logger.Error(err, "Failed to get source object resource from fed object")
		return
	}

	if f.isHPAType(fo) {
		f.worker.EnqueueWithDelay(key, 3*time.Second)
		return
	}

	if hpas, exist := f.workloadHPAMapping.LookupByT1(key); exist {
		for hpa := range hpas {
			f.worker.Enqueue(hpa)
		}
	}
}

func (f *FederatedHPAController) enqueueFedHPAForPropagationPolicy(policy metav1.Object) {
	key := policyObjectToResource(policy)

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

func (f *FederatedHPAController) enqueueFederatedObjectsForFTC(ftc *fedcorev1a1.FederatedTypeConfig) {
	logger := f.logger.WithValues("ftc", ftc.GetName())

	if scaleTargetRefPath, ok := ftc.GetAnnotations()[HPAScaleTargetRefPath]; ok {
		f.scaleTargetRefMapping[ftc.GetSourceTypeGVK()] = scaleTargetRefPath
	} else {
		delete(f.scaleTargetRefMapping, ftc.GetSourceTypeGVK())
		return
	}

	logger.V(2).Info("Enqueue federated objects for FTC")

	allObjects := []fedcorev1a1.GenericFederatedObject{}
	fedObjects, err := f.fedObjectInformer.Lister().List(labels.Everything())
	if err != nil {
		logger.Error(err, "Failed to enqueue FederatedObjects for policy")
		return
	}
	for _, obj := range fedObjects {
		allObjects = append(allObjects, obj)
	}

	for _, obj := range allObjects {
		templateMetadata, err := obj.GetSpec().GetTemplateMetadata()
		if err != nil {
			logger.Error(err, "Failed to get source GVK from FederatedObject, will not enqueue")
			continue
		}
		if templateMetadata.GroupVersionKind() == ftc.GetSourceTypeGVK() {
			sourceObjectResource, err := fedObjectToSourceObjectResource(obj)
			if err != nil {
				logger.Error(err, "Failed to get source Resource from FederatedObject, will not enqueue")
				continue
			}
			f.worker.Enqueue(sourceObjectResource)
		}
	}
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
	f.metrics.Counter(metrics.FederateHPAControllerThroughput, 1)
	ctx, logger := logging.InjectLoggerValues(ctx, "source-hpa-object", key.QualifiedName().String(), "gvk", key.gvk)

	startTime := time.Now()

	logger.V(3).Info("Start reconcile")
	defer func() {
		f.metrics.Duration(metrics.FederateHPAControllerLatency, startTime)
		logger.WithValues("duration", time.Since(startTime), "status", status.String()).V(3).Info("Finished reconcile")
	}()

	hpaFTC, exists := f.informerManager.GetResourceFTC(key.gvk)
	if !exists {
		// Waiting for func enqueueFederatedObjectsForFTC enqueue it again.
		return worker.StatusAllOK
	}

	hpaGVR := hpaFTC.GetSourceTypeGVR()
	ctx, logger = logging.InjectLoggerValues(ctx, "hpa-ftc", hpaFTC.Name, "hpa-gvr", hpaGVR)

	lister, hasSynced, exists := f.informerManager.GetResourceLister(key.gvk)
	if !exists {
		return worker.StatusAllOK
	}
	if !hasSynced() {
		// If lister is not yet synced, simply reenqueue after a short delay.
		logger.V(3).Info("Lister for source hpa type not yet synced, will reenqueue")
		return worker.Result{
			Success:      true,
			RequeueAfter: pointer.Duration(f.cacheSyncRateLimiter.When(key)),
		}
	}
	f.cacheSyncRateLimiter.Forget(key)

	hpaUns, err := lister.Get(key.QualifiedName().String())
	if err != nil {
		logger.Error(err, "Failed to get source hpa object from store")
		return worker.StatusAllOK
	}
	hpaObject := hpaUns.(*unstructured.Unstructured).DeepCopy()

	fedHPAObjectName := naming.GenerateFederatedObjectName(hpaObject.GetName(), hpaFTC.Name)
	ctx, logger = logging.InjectLoggerValues(ctx, "federated-hpa-object", fedHPAObjectName)

	fedHPAObject, err := fedobjectadapters.GetFromLister(
		f.fedObjectInformer.Lister(),
		nil,
		hpaObject.GetNamespace(),
		fedHPAObjectName,
	)
	if err != nil {
		logger.Error(err, "Failed to get federated hpa object from store")
		return worker.StatusError
	}

	if ok, err := pendingcontrollers.ControllerDependenciesFulfilled(fedHPAObject, PrefixedFederatedHPAControllerName); err != nil {
		logger.Error(err, "Failed to check controller dependencies")
		return worker.StatusError
	} else if !ok {
		return worker.StatusAllOK
	}

	scaleTargetRef := f.scaleTargetRefMapping[key.gvk]
	newWorkloadResource, err := scaleTargetRefToResource(hpaObject, scaleTargetRef)
	if err != nil {
		logger.Error(err, "Failed to get workload resource from hpa")
		return worker.StatusError
	}

	ctx, logger = logging.InjectLoggerValues(ctx, "workload-object", newWorkloadResource.QualifiedName(), "workload-gvk", newWorkloadResource.gvk)

	oldWorkloadResource, exist := f.workloadHPAMapping.LookupByT2(key)
	if exist {
		f.workloadHPAMapping.DeleteT2(key)
		f.ppWorkloadMapping.DeleteT2(oldWorkloadResource)
	}
	if err := f.workloadHPAMapping.Add(newWorkloadResource, key); err != nil {
		logger.Error(err, "Failed to add workload and hpa mapping")
		return worker.StatusError
	}
	workloadExist := true
	fedWorkload, err := f.getFedWorkLoadFromResource(newWorkloadResource)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to get fed workload from fed workload resource")
			return worker.StatusError
		}
		workloadExist = false
	}

	var pp fedcorev1a1.GenericPropagationPolicy
	if workloadExist {
		newPPResource := getPPResourceFromFedWorkload(fedWorkload)
		if newPPResource != nil {
			_, exist = f.ppWorkloadMapping.LookupByT2(newWorkloadResource)
			if exist {
				f.ppWorkloadMapping.DeleteT2(newWorkloadResource)
			}

			if err := f.ppWorkloadMapping.Add(*newPPResource, newWorkloadResource); err != nil {
				logger.Error(err, "Failed to add workload and pp mapping")
				return worker.StatusError
			}

			pp, err = f.getPPFromResource(newPPResource)
			if err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to get pp from pp resource")
				return worker.StatusError
			}
		}
	}

	switch hpaObject.GetLabels()[FederatedHPAMode] {
	case FederatedHPAModeFederation:
		if res := f.addFedHPAPendingController(ctx, fedHPAObject); res != worker.StatusAllOK {
			return worker.StatusError
		}

		if !workloadExist || isPPExist(pp) && isPPDivided(pp) {
			return f.addFedHPALabel(ctx, hpaObject, hpaGVR, HPAEnableKey, common.AnnotationValueTrue)
		} else {
			return f.removeFedHPALabel(ctx, hpaObject, hpaGVR, HPAEnableKey)
		}

	case FederatedHPAModeDistributed, FederatedHPAModeDefault:
		if res := f.removeFedHPALabel(ctx, hpaObject, hpaGVR, HPAEnableKey); res != worker.StatusAllOK {
			return worker.StatusError
		}

		if !workloadExist || isPPExist(pp) &&
			!isPPDivided(pp) &&
			isPPFollowerEnabled(pp) &&
			isWorkloadRetainReplicas(fedWorkload) &&
			isHPAFollowTheWorkload(ctx, hpaObject, fedWorkload) {
			return f.removePendingController(ctx, hpaFTC, fedHPAObject)
		} else {
			return f.addFedHPAPendingController(ctx, fedHPAObject)
		}
	}

	return worker.StatusAllOK
}

func (f *FederatedHPAController) getFedWorkLoadFromResource(workload Resource) (fedcorev1a1.GenericFederatedObject, error) {
	workloadFTC, exists := f.informerManager.GetResourceFTC(workload.gvk)
	if !exists {
		return nil, errors.New(fmt.Sprintf("failed to get workload %v ftc", workloadFTC))
	}

	fedObjectName := naming.GenerateFederatedObjectName(workload.name, workloadFTC.Name)

	// get fed hpa object
	fedObject, err := fedobjectadapters.GetFromLister(
		f.fedObjectInformer.Lister(),
		nil,
		workload.namespace,
		fedObjectName,
	)
	if err != nil {
		return nil, err
	}

	return fedObject, nil
}

func (f *FederatedHPAController) getPPFromResource(resource *Resource) (fedcorev1a1.GenericPropagationPolicy, error) {
	if resource.gvk.Kind == PropagationPolicyKind {
		pp, err := f.propagationPolicyInformer.Lister().PropagationPolicies(resource.namespace).Get(resource.name)
		if err != nil {
			return nil, err
		}
		return pp, nil
	}

	cpp, err := f.clusterPropagationPolicyInformer.Lister().Get(resource.name)
	if err != nil {
		return nil, err
	}
	return cpp, nil
}
