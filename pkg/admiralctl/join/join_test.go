package join

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	utilpointer "k8s.io/utils/pointer"
)

func TestCommandJoinOption_Preflight(t *testing.T) {
	testCases := []struct {
		name        string
		option      CommandJoinOption
		args        []string
		expectedErr error
	}{
		{
			name: "normal test, UseServiceAccount=true",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/config",
				ClusterContext:    "",
				UseServiceAccount: true,
			},
			args:        []string{"cluster"},
			expectedErr: nil,
		},
		{
			name: "normal test, UseServiceAccount=false",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/config",
				ClusterContext:    "token-context",
				UseServiceAccount: false,
			},
			args:        []string{"cluster"},
			expectedErr: nil,
		},
		// error test
		{
			name: "no fcluster name",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/config",
				ClusterContext:    "",
				UseServiceAccount: true,
			},
			args:        []string{},
			expectedErr: fmt.Errorf("command line input format error"),
		},
		{
			name: "UseServiceAccount=false, but no BearerToken",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/config",
				ClusterContext:    "",
				UseServiceAccount: false,
			},
			args:        []string{"cluster"},
			expectedErr: fmt.Errorf("if --use-service-account sets false, BearerToken should be in the kubeconfig"),
		},
		{
			name: "UseServiceAccount=true, but no cert and key",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/config",
				ClusterContext:    "no-cert-context",
				UseServiceAccount: true,
			},
			args:        []string{"cluster"},
			expectedErr: fmt.Errorf("if --use-service-account sets true, certificate and key should be in the kubeconfig"),
		},
		{
			name: "cluster's control plane not ready",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/kubeadmiral/member-test-notready.config",
				ClusterContext:    "",
				UseServiceAccount: true,
			},
			args:        []string{"cluster"},
			expectedErr: fmt.Errorf("control plane %s is not ready", "kubeadmiral-member-test-notready-control-plane"),
		},
		{
			name: "cluster already joined the federation",
			option: CommandJoinOption{
				ClusterKubeConfig: "/home/jzd/.kube/kubeadmiral/member-1.config",
				ClusterContext:    "",
				UseServiceAccount: true,
			},
			args:        []string{"cluster"},
			expectedErr: fmt.Errorf("the cluster has already joined your kubeadmiral federation (federated-cluster-uid: %v)", "e9e8be6f-5d61-4f97-bc67-f1fadf17542a"),
		},
	}

	defaultConfigFlags := genericclioptions.NewConfigFlags(true).WithDiscoveryBurst(300).WithDiscoveryQPS(50.0)
	defaultConfigFlags.KubeConfig = utilpointer.String("/home/jzd/.kube/kubeadmiral/kubeadmiral.config")
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert := assert.New(t)
			err := testCase.option.Preflight(util.NewFactory(defaultConfigFlags), testCase.args)
			assert.Equal(testCase.expectedErr, err)
		})
	}
}
