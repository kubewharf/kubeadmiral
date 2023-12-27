package join

import (
	"context"
	"fmt"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	applymetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/util/templates"
)

var (
	joinLongDesc = templates.LongDesc(`
	Join a member cluster to Kubeadmiral control plane. 

	If the control plane of the member cluster is not ready or has already joined, this command will do nothing.
	`)

	joinExample = templates.Examples(`
		# Join cluster1 to Kubeadmiral by kubeconfig
		%[1]s join cluster1 --cluster-kubeconfig=<CLUSTER_KUBECONFIG_PATH>
		
		# Support to use '--api-endpoint' to overwrite the member cluster's api-endpoint
		%[1]s join cluster1 --cluster-kubeconfig=<CLUSTER_KUBECONFIG_PATH> --api-endpoint=<CLUSTER_API_ENDPOINT>

		# Support to use '--use-service-account' to determine whether create a new ServiceAccount when join
		%[1]s join cluster1 --cluster-kubeconfig=<CLUSTER_KUBECONFIG_PATH> --use-service-account=false
		
		# Support to use '--cluster-context' to specify the context of member cluster
		%[1]s join cluster1 --cluster-kubeconfig=<CLUSTER_KUBECONFIG_PATH> --cluster-context=<CLUSTER_CONTEXT>

	`)
)

// CommandJoinOption holds all command options for join
type CommandJoinOption struct {
	// Cluster is the name of member cluster
	Cluster string

	// Namespace is the kubeadmiral namespace where the resources will be created, corresponding to common.DefaultFedSystemNamespace
	Namespace string

	// ClusterContext is context name of member cluster in kubeconfig
	ClusterContext string

	// ClusterKubeConfig is the member cluster's kubeconfig path
	ClusterKubeConfig string

	// Host is the api-endpoint of the member cluster
	Host string

	// CAData is the CA of the member cluster
	CAData []byte

	// CertData is the cert of the member cluster
	CertData []byte

	// KeyData is the cert-key of the member cluster
	KeyData []byte

	// BearerToken is the the client authentication specified by user
	BearerToken string

	// UseServiceAccount is a flag to choose whether create a new ServiceAccount when join, corresponding to FederatedCluster.spec.useServiceAccount
	UseServiceAccount bool

	ClusterK8sClientSet *kubernetes.Clientset
	FedK8sClientSet     *kubernetes.Clientset
	FedClientSet        *fedclient.Clientset
}

// AddFlags adds flags for a specified FlagSet
func (o *CommandJoinOption) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&o.ClusterKubeConfig, "cluster-kubeconfig", "", "Path of the member cluster's kubeconfig.")
	flags.StringVar(&o.ClusterContext, "cluster-context", "", "Context name of member cluster in kubeconfig.")
	flags.StringVar(&o.Host, "api-endpoint", "", "api-endpoint of the member cluster.")
	flags.BoolVar(&o.UseServiceAccount, "use-service-account", true,
		"Whether creates a new ServiceAccount when join, corresponding to FederatedCluster.spec.useServiceAccount.    "+
			"If you set 'false', BearerToken should be in the kubeconfig.")
}

func NewCmdJoin(f util.Factory, parentCommand string) *cobra.Command {
	o := CommandJoinOption{}

	cmd := &cobra.Command{
		Use:                   "join <FCLUSTER_NAME> --cluster-kubeconfig <CLUSTER_KUBECONFIG_PATH>",
		Short:                 "join clusters to Kubeadmiral control plane",
		Long:                  joinLongDesc,
		Example:               fmt.Sprintf(joinExample, parentCommand),
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.ToOptions(f, args); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			if err := o.Join(); err != nil {
				return err
			}
			return nil
		},
	}

	flag := cmd.Flags()
	o.AddFlags(flag)
	cmd.MarkFlagRequired("cluster-kubeconfig")

	return cmd
}

// ToOptions converts from CLI inputs to runtime options
func (o *CommandJoinOption) ToOptions(f util.Factory, args []string) error {
	var err error = nil

	if len(args) != 1 {
		return fmt.Errorf("command line input format error")
	}

	o.Cluster = args[0]

	o.Namespace = common.DefaultFedSystemNamespace

	clusterFactory, err := util.NewClusterFactoryByKubeConfigNoPassword(o.ClusterKubeConfig, o.ClusterContext)
	if err != nil {
		return err
	}

	clusterRESTConfig, err := clusterFactory.ToRESTConfig()
	if err != nil {
		return err
	}
	o.ClusterK8sClientSet = kubernetes.NewForConfigOrDie(clusterRESTConfig)

	fedRESTConfig, err := f.ToRESTConfig()
	if err != nil {
		return err
	}
	o.FedK8sClientSet = kubernetes.NewForConfigOrDie(fedRESTConfig)
	o.FedClientSet = fedclient.NewForConfigOrDie(fedRESTConfig)

	if len(o.Host) == 0 {
		o.Host = clusterRESTConfig.Host
	}
	o.CAData = clusterRESTConfig.CAData
	o.CertData = clusterRESTConfig.CertData
	o.KeyData = clusterRESTConfig.KeyData
	o.BearerToken = clusterRESTConfig.BearerToken

	return nil
}

// Validate verifies whether the options are valid and whether the joining is valid.
func (o *CommandJoinOption) Validate() error {
	if !o.UseServiceAccount {
		if len(o.BearerToken) == 0 {
			return fmt.Errorf("if --use-service-account sets false, BearerToken should be in the kubeconfig")
		}
	} else if len(o.CertData) == 0 || len(o.KeyData) == 0 {
		return fmt.Errorf("if --use-service-account sets true, certificate and key should be in the kubeconfig")
	}

	if err := o.checkClusterReady(); err != nil {
		return err
	}

	if err := o.checkClusterJoined(); err != nil {
		return err
	}

	return nil
}

// check whether the member cluster's control plane nodes are ready
func (o *CommandJoinOption) checkClusterReady() error {
	nodes, err := o.ClusterK8sClientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if isControlPlane(&node) {
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
					return fmt.Errorf("control plane %s is not ready", node.Name)
				}
			}
		}
	}

	return nil
}

func isControlPlane(node *corev1.Node) bool {
	labels := node.Labels
	if labels != nil {
		_, hasControlPlaneLabel := labels["node-role.kubernetes.io/control-plane"]
		return hasControlPlaneLabel
	}
	return false
}

// check whether the member cluster has already joined a kubeadmiral federation
func (o *CommandJoinOption) checkClusterJoined() error {
	namespace, err := o.ClusterK8sClientSet.CoreV1().Namespaces().Get(context.TODO(), o.Namespace, metav1.GetOptions{})
	if err == nil {
		for key, value := range namespace.GetAnnotations() {
			if key == "kubeadmiral.io/federated-cluster-uid" {
				fedClusters, err := o.FedClientSet.CoreV1alpha1().FederatedClusters().List(context.TODO(), metav1.ListOptions{})
				if err != nil {
					return err
				}

				for _, fedCluster := range fedClusters.Items {
					if fedCluster.UID == types.UID(value) {
						return fmt.Errorf("the cluster has already joined your kubeadmiral federation (federated-cluster-uid: %v)", value)
					}
				}
				return fmt.Errorf("the cluster has joined another kubeadmiral federation (federated-cluster-uid: %v)", value)
			}
		}
	}

	return nil
}

func (o *CommandJoinOption) Join() error {
	if err := o.applySecret(); err != nil {
		return err
	}

	if err := o.createFederatedCluster(); err != nil {
		return err
	}

	return nil
}

// apply the Secret
func (o *CommandJoinOption) applySecret() error {
	kindString := "Secret"
	APIVersionString := "v1"
	secret := &applycorev1.SecretApplyConfiguration{
		TypeMetaApplyConfiguration: applymetav1.TypeMetaApplyConfiguration{
			Kind:       &kindString,
			APIVersion: &APIVersionString,
		},
		ObjectMetaApplyConfiguration: &applymetav1.ObjectMetaApplyConfiguration{
			Name:      &o.Cluster,
			Namespace: &o.Namespace,
		},
		Data: map[string][]byte{
			common.ClusterCertificateAuthorityKey: o.CAData,
			common.ClusterClientCertificateKey:    o.CertData,
			common.ClusterClientKeyKey:            o.KeyData,
			common.ClusterBootstrapTokenKey:       []byte(o.BearerToken),
		},
	}

	_, err := o.FedK8sClientSet.CoreV1().Secrets(o.Namespace).Apply(context.TODO(), secret, metav1.ApplyOptions{FieldManager: "kubectl-client-side-apply"})
	if err != nil {
		return err
	}

	fmt.Printf("Secret: %s/%s created\n", o.Namespace, o.Cluster)
	return nil
}

// create the FederatedCluster
func (o *CommandJoinOption) createFederatedCluster() error {
	federatedCluster := &fedcorev1a1.FederatedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Cluster,
			Namespace: o.Namespace,
		},
		Spec: fedcorev1a1.FederatedClusterSpec{
			APIEndpoint:            o.Host,
			UseServiceAccountToken: o.UseServiceAccount,
			SecretRef: fedcorev1a1.LocalSecretReference{
				Name: o.Cluster,
			},
		},
	}

	_, err := o.FedClientSet.CoreV1alpha1().FederatedClusters().Create(context.TODO(), federatedCluster, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	fmt.Printf("FederatedCluster: %s created\n", o.Cluster)
	return nil
}
