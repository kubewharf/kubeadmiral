package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"

	// "k8s.io/client-go/dynamic/dynamicinformer"
	// "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	fedinformers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/managedlabel"
	schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
	"github.com/kubewharf/kubeadmiral/pkg/util/controllerbase"
)

var (
	kubeconfig         string
	fedSystemNamespace string
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "")
	flag.StringVar(&fedSystemNamespace, "fed-system-namespace", common.DefaultFedSystemNamespace, "")
}

func main() {
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Exit(err)
	}

	kubeclient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Exit(err)
	}
	dynamicclient, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Exit(err)
	}
	fedclient, err := fedclient.NewForConfig(config)
	if err != nil {
		klog.Exit(err)
	}

	// kubeinformerfactory := informers.NewSharedInformerFactory(kubeclient, 0)
	// dynamicinformerfactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicclient, 0)
	fedinformerfactory := fedinformers.NewSharedInformerFactory(fedclient, 0)

	singleClusterBase := controllerbase.NewFTCControllerBase(fedinformerfactory.Core().V1alpha1().FederatedTypeConfigs(), dynamicclient)
	multiClusterBase := controllerbase.NewMultiClusterFTCControllerBase(
		fedSystemNamespace,
		config,
		kubeclient,
		fedinformerfactory.Core().V1alpha1().FederatedTypeConfigs(),
		fedinformerfactory.Core().V1alpha1().FederatedClusters(),
	)

	enabledFTCs := []string{
		"deployments.apps",
		"secrets",
		"configmaps",
	}

	controller := NewTestController(singleClusterBase, multiClusterBase, enabledFTCs)

	ctx := context.Background()
	fedinformerfactory.Start(ctx.Done())
	singleClusterBase.Start(ctx)
	multiClusterBase.Start(ctx)
	go controller.Start(ctx)

	<-ctx.Done()
}

type ObjectMeta struct {
	GVR       schema.GroupVersionResource
	Name      string
	Namespace string
}

type TestController struct {
	singleClusterBase *controllerbase.FTCControllerBase
	multiClusterBase  *controllerbase.MultiClusterFTCControllerBase

	queue workqueue.Interface
}

func NewTestController(
	singleClusterBase *controllerbase.FTCControllerBase,
	multiClusterBase *controllerbase.MultiClusterFTCControllerBase,
	enabledFTCs []string,
) *TestController {
	controller := &TestController{
		singleClusterBase: singleClusterBase,
		multiClusterBase:  multiClusterBase,
		queue:             workqueue.NewRateLimitingQueue(workqueue.DefaultItemBasedRateLimiter()),
	}

	singleClusterBase.AddEventHandlerGenerator(func(ftc *v1alpha1.FederatedTypeConfig) cache.ResourceEventHandler {
		if !sets.New[string](enabledFTCs...).Has(ftc.Name) {
			return nil
		}

		klog.Infof("Generating single cluster event handler for FTC %s", ftc.Name)

		sourceType := ftc.GetSourceType()
		if sourceType == nil {
			klog.Errorf("Source type for FTC %s should not be nil", ftc.Name)
		}
		sourceGVR := schemautil.APIResourceToGVR(sourceType)

		return cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				uns := obj.(*unstructured.Unstructured)
				for _, finalizer := range uns.GetFinalizers() {
					if finalizer == federate.FinalizerFederateController {
						return true
					}
				}
				return false
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					uns := obj.(*unstructured.Unstructured)
					klog.Infof(
						"Received ADD event for source object - GVR: %s, Name: %s, Namespace: %s",
						sourceGVR.String(),
						uns.GetName(),
						uns.GetNamespace(),
					)

					controller.queue.Add(ObjectMeta{
						GVR:       sourceGVR,
						Name:      uns.GetName(),
						Namespace: uns.GetNamespace(),
					})
				},
				UpdateFunc: func(oldObj interface{}, newObj interface{}) {
					uns := newObj.(*unstructured.Unstructured)
					klog.Infof(
						"Received UPDATE event for source object - GVR: %s, Name: %s, Namespace: %s",
						sourceGVR.String(),
						uns.GetName(),
						uns.GetNamespace(),
					)

					controller.queue.Add(ObjectMeta{
						GVR:       sourceGVR,
						Name:      uns.GetName(),
						Namespace: uns.GetNamespace(),
					})
				},
				DeleteFunc: func(obj interface{}) {
					uns := obj.(*unstructured.Unstructured)
					klog.Infof(
						"Received DELETE event for source object - GVR: %s, Name: %s, Namespace: %s",
						sourceGVR.String(),
						uns.GetName(),
						uns.GetNamespace(),
					)

					controller.queue.Add(ObjectMeta{
						GVR:       sourceGVR,
						Name:      uns.GetName(),
						Namespace: uns.GetNamespace(),
					})
				},
			},
		}
	})

	multiClusterBase.AddEventHandlerGenerator(
		func(ftc *v1alpha1.FederatedTypeConfig, cluster *v1alpha1.FederatedCluster) cache.ResourceEventHandler {
			if !sets.New[string](enabledFTCs...).Has(ftc.Name) {
				return nil
			}

			klog.Infof("Generating multi cluster event handler for FTC %s, cluster %s", ftc.Name, cluster.Name)

			sourceType := ftc.GetSourceType()
			if sourceType == nil {
				klog.Errorf("Source type for FTC %s should not be nil", ftc.Name)
			}
			sourceGVR := schemautil.APIResourceToGVR(sourceType)

			return cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					return managedlabel.HasManagedLabel(obj.(*unstructured.Unstructured))
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						uns := obj.(*unstructured.Unstructured)
						klog.Infof(
							"Received ADD event for cluster object - Cluster: %s, GVR: %s, Name: %s, Namespace: %s",
							cluster.Name,
							sourceGVR.String(),
							uns.GetName(),
							uns.GetNamespace(),
						)

						controller.queue.Add(ObjectMeta{
							GVR:       sourceGVR,
							Name:      uns.GetName(),
							Namespace: uns.GetNamespace(),
						})
					},
					UpdateFunc: func(oldObj interface{}, newObj interface{}) {
						uns := newObj.(*unstructured.Unstructured)
						klog.Infof(
							"Received UPDATE event for cluster object - Cluster: %s, GVR: %s, Name: %s, Namespace: %s",
							cluster.Name,
							sourceGVR.String(),
							uns.GetName(),
							uns.GetNamespace(),
						)

						controller.queue.Add(ObjectMeta{
							GVR:       sourceGVR,
							Name:      uns.GetName(),
							Namespace: uns.GetNamespace(),
						})
					},
					DeleteFunc: func(obj interface{}) {
						uns := obj.(*unstructured.Unstructured)
						klog.Infof(
							"Received DELETE event for cluster object - Cluster: %s, GVR: %s, Name: %s, Namespace: %s",
							cluster.Name,
							sourceGVR.String(),
							uns.GetName(),
							uns.GetNamespace(),
						)

						controller.queue.Add(ObjectMeta{
							GVR:       sourceGVR,
							Name:      uns.GetName(),
							Namespace: uns.GetNamespace(),
						})
					},
				},
			}
		},
	)

	return controller
}

func (c *TestController) Start(ctx context.Context) {
	if !cache.WaitForCacheSync(ctx.Done(), c.singleClusterBase.IsSynced, c.multiClusterBase.IsSynced) {
		klog.Error("Failed to wait for cache sync")
		return
	}

	go wait.UntilWithContext(ctx, c.processQueueItem, 0)
	<-ctx.Done()
}

func (c *TestController) processQueueItem(ctx context.Context) {
	key, shutdown := c.queue.Get()
	if shutdown {
		return
	}
	defer c.queue.Done(key)

	objectMeta := key.(ObjectMeta)
	klog.Infof("Processing queue item - GVR: %s, Name: %s, Namespace: %s", objectMeta.GVR, objectMeta.Name, objectMeta.Namespace)

	var srcObj runtime.Object
	memberClusters := []string{}
	memberObjs := []runtime.Object{}
	var err error

	// 1. Get source object
	lister, hasSynced := c.singleClusterBase.GetResourceLister(objectMeta.GVR)
	if lister == nil {
		klog.Infof("Lister for GVR %S does not exist, will retry later", objectMeta.GVR.String())
		c.queue.Add(key)
		return
	}
	if !hasSynced() {
		klog.Infof("Lister for GVR %S not synced, will retry later", objectMeta.GVR.String())
		c.queue.Add(key)
		return
	}

	if len(objectMeta.Namespace) > 0 {
		srcObj, err = lister.ByNamespace(objectMeta.Namespace).Get(objectMeta.Name)
	} else {
		srcObj, err = lister.Get(objectMeta.Name)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Errorf("Error getting source object: %w", err)
		c.queue.Add(key)
		return
	}
	if apierrors.IsNotFound(err) {
		klog.Infof("No source object, will continue")
		return
	}

	// 2. Get member objects
	clusters, err := c.multiClusterBase.GetFederatedClusterLister().List(labels.Everything())
	if err != nil {
		klog.Errorf("Failed to list clusters: %s", err)
		c.queue.Add(key)
		return
	}

	for _, cluster := range clusters {
		lister, hasSynced := c.multiClusterBase.GetResourceLister(cluster.Name, objectMeta.GVR)
		if lister == nil {
			klog.Infof("Lister for GVR %s and cluster %s does not exist, will retry later", objectMeta.GVR.String(), cluster.Name)
			c.queue.Add(key)
			return
		}
		if !hasSynced() {
			klog.Infof("Lister for GVR %s and cluster %s not synced, will retry later", objectMeta.GVR.String(), cluster.Name)
			c.queue.Add(key)
			return
		}

		var memberObj runtime.Object
		if len(objectMeta.Namespace) > 0 {
			memberObj, err = lister.ByNamespace(objectMeta.Namespace).Get(objectMeta.Name)
		} else {
			memberObj, err = lister.Get(objectMeta.Name)
		}
		if err != nil && !apierrors.IsNotFound(err) {
			klog.Errorf("Error getting member object: %w", err)
			c.queue.Add(key)
			return
		}
		if apierrors.IsNotFound(err) {
			klog.Infof("No member object in cluster %s, will continue", cluster.Name)
			continue
		}

		memberClusters = append(memberClusters, cluster.Name)
		memberObjs = append(memberObjs, memberObj)
	}

	fmt.Println("Source object:")
	prettyPrint(srcObj.(*unstructured.Unstructured))
	fmt.Println("Member objects:")
	for i, memObj := range memberObjs {
		fmt.Printf("%s:\n", memberClusters[i])
		prettyPrint(memObj.(*unstructured.Unstructured))
	}
}

func prettyPrint(uns *unstructured.Unstructured) {
	unstructured.RemoveNestedField(uns.Object, "metadata", "managedFields")
	bytes, err := json.MarshalIndent(uns, "", " ")
	if err != nil {
		klog.Infof("Unable to print object: %s", err)
	} else {
		fmt.Println(string(bytes))
	}
}
