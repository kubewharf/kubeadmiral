//go:build ignore

package podautoscaler

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	cacheddiscovery "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	autoscalinginformers "k8s.io/client-go/informers/autoscaling/v2"
	coreinformers "k8s.io/client-go/informers/core/v1"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/scale"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/controller/podautoscaler"
	poautosclerconfig "k8s.io/kubernetes/pkg/controller/podautoscaler/config"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

func main() {

}

func startHPAControllerWithMetricsClient(
	ctx context.Context,
	controllerContext *controllercontext.Context,
	metricsClient metrics.MetricsClient,
	hpaConfiguration *poautosclerconfig.HPAControllerConfiguration,
) (bool, error) {
	hpaClient := controllerContext.KubeClientset
	// Use a discovery client capable of being refreshed.
	discoveryClient := hpaClient.Discovery()
	cachedClient := cacheddiscovery.NewMemCacheClient(discoveryClient)
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedClient)
	go wait.Until(func() {
		restMapper.Reset()
	}, 30*time.Second, ctx.Done())

	hpaClientConfig := controllerContext.RestConfig

	// we don't use cached discovery because DiscoveryScaleKindResolver does its own caching,
	// so we want to re-fetch every time when we actually ask for it
	scaleKindResolver := scale.NewDiscoveryScaleKindResolver(hpaClient.Discovery())
	scaleClient, err := scale.NewForConfig(hpaClientConfig, restMapper, dynamic.LegacyAPIPathResolverFunc, scaleKindResolver)
	if err != nil {
		return false, err
	}
	podInformer, err := newAggregatedPodInformer(ctx, controllerContext)
	if err != nil {
		return false, err
	}
	hpaInformer, err := newHPAInformer()
	if err != nil {
		return false, err
	}

	go podautoscaler.NewHorizontalController(
		hpaClient.CoreV1(),
		scaleClient,
		hpaClient.AutoscalingV2(),
		restMapper,
		metricsClient,
		hpaInformer,
		podInformer,
		hpaConfiguration.HorizontalPodAutoscalerSyncPeriod.Duration,
		hpaConfiguration.HorizontalPodAutoscalerDownscaleStabilizationWindow.Duration,
		hpaConfiguration.HorizontalPodAutoscalerTolerance,
		hpaConfiguration.HorizontalPodAutoscalerCPUInitializationPeriod.Duration,
		hpaConfiguration.HorizontalPodAutoscalerInitialReadinessDelay.Duration,
	).Run(ctx, controllerContext.WorkerCount)
	return true, nil
}

func newHPAInformer() (autoscalinginformers.HorizontalPodAutoscalerInformer, error) {

}

func newAggregatedPodInformer(
	ctx context.Context,
	controllerContext *controllercontext.Context,
) (coreinformers.PodInformer, error) {
	informer := cache.NewSharedIndexInformer(
		podListerWatcher(ctx, controllerContext),
		&corev1.Pod{},
		0,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
	return &podInformer{informer: informer}, nil
}

func podListerWatcher(ctx context.Context, controllerContext *controllercontext.Context) cache.ListerWatcher {
	proxyCh := make(chan watch.Event)
	proxyWatcher := watch.NewProxyWatcher(proxyCh)

	handler := func(event watch.EventType, obj interface{}, cluster string) {
		select {
		case <-proxyWatcher.StopChan():
			return
		case <-ctx.Done():
			return
		default:
		}

		pod := obj.(*corev1.Pod)
		copyPod(pod, cluster)
		proxyCh <- watch.Event{
			Type:   event,
			Object: pod,
		}
	}

	controllerContext.FederatedInformerManager.AddPodEventHandler(&informermanager.ResourceEventHandlerWithClusterFuncs{
		AddFunc:    func(obj interface{}, cluster string) { handler(watch.Added, obj, cluster) },
		UpdateFunc: func(_, obj interface{}, cluster string) { handler(watch.Modified, obj, cluster) },
		DeleteFunc: func(obj interface{}, cluster string) { handler(watch.Deleted, obj, cluster) },
	})

	lw := &multiClustersListWatch{}
	lw.ListFunc = func(options metav1.ListOptions) (runtime.Object, error) {
		clusters, err := controllerContext.FederatedInformerManager.GetReadyClusters()
		if err != nil {
			return nil, err
		}
		podList := &corev1.PodList{Items: []corev1.Pod{}}
		resourceVersion := "0"
		for _, cluster := range clusters {
			lister, hasSync, exist := controllerContext.FederatedInformerManager.GetPodLister(cluster.Name)
			if !exist || !hasSync() {
				return nil, fmt.Errorf("cache not exists or not ready: %s", cluster.Name)
			}
			ret, err := lister.List(labels.Everything())
			if err != nil {
				return nil, err
			}
			for i := range ret {
				copyPod(ret[i], cluster.Name)
				// TODO: make it right
				if resourceVersion < ret[i].ResourceVersion {
					resourceVersion = ret[i].ResourceVersion
				}
				podList.Items = append(podList.Items, *ret[i])
			}
		}
		if r := lw.getResourceVersions(); resourceVersion > r {
			lw.setResourceVersions(resourceVersion)
		} else {
			resourceVersion = r
		}

		podList.ResourceVersion = resourceVersion
		return podList, nil
	}
	lw.WatchFunc = func(options metav1.ListOptions) (watch.Interface, error) {
		return proxyWatcher, nil
	}
	return lw
}

type multiClustersListWatch struct {
	cache.ListWatch

	names           sets.Set[string]
	lock            sync.RWMutex
	resourceVersion string
}

func (m *multiClustersListWatch) getResourceVersions() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.resourceVersion
}

func (m *multiClustersListWatch) setResourceVersions(r string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.resourceVersion = r
}

func (m *multiClustersListWatch) checkNameConflict(name string) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.names.Has(name)
}

func (m *multiClustersListWatch) insertName(name string) bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.names == nil {
		m.names = sets.New(name)
		return true
	}
	if m.names.Has(name) {
		return false
	}
	m.names.Insert(name)
	return true
}

func (m *multiClustersListWatch) deleteName(name string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.names.Delete(name)
}

func copyPod(obj *corev1.Pod, cluster string) {
	pod := obj.DeepCopy()
	label := pod.GetLabels()
	if label == nil {
		label = make(map[string]string)
	}
	label[common.DefaultPrefix+"cluster"] = hashNaming(cluster, 63)
	pod.SetLabels(label)

	anno := pod.GetAnnotations()
	if anno == nil {
		anno = make(map[string]string)
	}
	anno[common.DefaultPrefix+"raw-name"] = pod.Name
	anno[common.DefaultPrefix+"cluster"] = cluster
	pod.SetAnnotations(anno)
	*obj = *pod
}

func hashNaming(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	hash := fnv.New32()
	_, _ = hash.Write([]byte(name))
	nameHash := fmt.Sprint(hash.Sum32())
	return fmt.Sprintf("%s-%s", name[:maxLen-len(nameHash)-1], nameHash)
}

type podInformer struct {
	informer cache.SharedIndexInformer
}

func (p *podInformer) Informer() cache.SharedIndexInformer {
	return p.informer
}

func (p *podInformer) Lister() v1.PodLister {
	return v1.NewPodLister(p.informer.GetIndexer())
}
