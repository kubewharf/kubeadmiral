package util

import (
	"context"

	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	clusterutil "github.com/kubewharf/kubeadmiral/pkg/util/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

type Factory interface {
	cmdutil.Factory

	NewClusterFactoryByClusterName(clusterName string) (cmdutil.Factory, error)
}

var _ Factory = &factoryImpl{}

// factoryImpl is the implementation of Factory
type factoryImpl struct {
	cmdutil.Factory

	// kubeConfigFlags holds all the flags specified by user.
	// These flags will be inherited by the member cluster's client.
	kubeConfigFlags *genericclioptions.ConfigFlags
}

// NewClusterFactoryByClusterName create a new ClusterFactory by ClusterName
func (f *factoryImpl) NewClusterFactoryByClusterName(clusterName string) (cmdutil.Factory, error) {
	restConfig, err := f.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	fedClientset, err := fedclient.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	cluster, err := fedClientset.CoreV1alpha1().FederatedClusters().Get(
		context.TODO(),
		clusterName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, err
	}

	kubeClientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	config, err := clusterutil.BuildClusterConfig(cluster, kubeClientset, restConfig, common.DefaultFedSystemNamespace)
	if err != nil {
		return nil, err
	}

	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	kubeConfigFlags.APIServer = &config.Host
	kubeConfigFlags.BearerToken = &config.BearerToken
	kubeConfigFlags.KeyFile = &config.KeyFile
	kubeConfigFlags.CAFile = &config.TLSClientConfig.CAFile
	kubeConfigFlags.CertFile = &config.TLSClientConfig.CertFile
	kubeConfigFlags.Insecure = &config.Insecure

	return cmdutil.NewFactory(kubeConfigFlags), nil
}

// NewFactory creates a new factory
func NewFactory(kubeConfigFlags *genericclioptions.ConfigFlags) Factory {
	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(kubeConfigFlags)
	f := &factoryImpl{
		kubeConfigFlags: kubeConfigFlags,
		Factory:         cmdutil.NewFactory(matchVersionKubeConfigFlags),
	}
	return f
}

// NewClusterFactoryByKubeConfig create a new ClusterFactory by KubeConfig
func NewClusterFactoryByKubeConfig(clusterKubeConfig, clusterContext string) (cmdutil.Factory, error) {
	configFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	configFlags.KubeConfig = &clusterKubeConfig
	configFlags.Context = &clusterContext
	return cmdutil.NewFactory(configFlags), nil
}
