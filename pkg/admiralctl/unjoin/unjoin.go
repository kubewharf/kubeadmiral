package unjoin

import (
	"context"
	"fmt"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/util/templates"
)

var (
	unjoinLongDesc = templates.LongDesc(`
	Unjoin a FederatedCluster from Kubeadmiral federation. 

	If the the federated cluster has not joined the federation, this command will do nothing.
	`)

	unjoinExample = templates.Examples(`
		# Unjoin cluster1 from Kubeadmiral federation
		%[1]s unjoin cluster1
	`)
)

// CommandUnjoinOption holds all command options for unjoin
type CommandUnjoinOption struct {
	// Cluster is the name of member cluster
	Cluster string

	FedK8sClientSet *kubernetes.Clientset
	FedClientSet    *fedclient.Clientset
}

func NewCmdJoin(f util.Factory, parentCommand string) *cobra.Command {
	o := CommandUnjoinOption{}

	cmd := &cobra.Command{
		Use:                   "unjoin <FCLUSTER_NAME>",
		Short:                 "unjoin the FederatedCluster from Kubeadmiral federation",
		Long:                  unjoinLongDesc,
		Example:               fmt.Sprintf(unjoinExample, parentCommand),
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.ToOptions(f, args); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			return nil
		},
	}

	return cmd
}

// ToOptions converts from CLI inputs to runtime options
func (o *CommandUnjoinOption) ToOptions(f util.Factory, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("command line input format error")
	}

	o.Cluster = args[0]

	fedRESTConfig, err := f.ToRESTConfig()
	if err != nil {
		return err
	}
	o.FedK8sClientSet = kubernetes.NewForConfigOrDie(fedRESTConfig)
	o.FedClientSet = fedclient.NewForConfigOrDie(fedRESTConfig)

	return nil
}

// Validate verifies whether the options are valid and whether the unjoining is valid.
func (o *CommandUnjoinOption) Validate() error {
	if err := o.checkClusterJoined(); err != nil {
		return err
	}
	return nil
}

// check whether the cluster has joined kubeadmiral federation
func (o *CommandUnjoinOption) checkClusterJoined() error {
	fedClusters, err := o.FedClientSet.CoreV1alpha1().FederatedClusters().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, fedCluster := range fedClusters.Items {
		if fedCluster.Name == o.Cluster {
			return nil
		}
	}

	return fmt.Errorf("the cluster has not joined the kubeadmiral federation")
}
