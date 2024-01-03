package unjoin

import (
	"fmt"
	"testing"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	"github.com/stretchr/testify/assert"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	utilpointer "k8s.io/utils/pointer"
)

func TestCommandJoinOption_ToOptions(t *testing.T) {
	testCases := []struct {
		name        string
		option      CommandUnjoinOption
		args        []string
		expectedErr error
	}{
		// error test
		{
			name:        "no fcluster name",
			option:      CommandUnjoinOption{},
			args:        []string{},
			expectedErr: fmt.Errorf("command line input format error"),
		},
	}

	defaultConfigFlags := genericclioptions.NewConfigFlags(true).WithDiscoveryBurst(300).WithDiscoveryQPS(50.0)
	defaultConfigFlags.KubeConfig = utilpointer.String("/home/jzd/.kube/kubeadmiral/kubeadmiral.config")
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert := assert.New(t)
			err := testCase.option.ToOptions(util.NewFactory(defaultConfigFlags), testCase.args)
			assert.Equal(testCase.expectedErr, err)
		})
	}
}

func TestCommandJoinOption_Validate(t *testing.T) {
	testCases := []struct {
		name        string
		option      CommandUnjoinOption
		args        []string
		expectedErr error
	}{
		// normal test
		{
			name:        "normal test",
			option:      CommandUnjoinOption{},
			args:        []string{"cluster-test-healthy"},
			expectedErr: nil,
		},
		// error test
		{
			name:        "cluster has not joined kubeadmiral federation",
			option:      CommandUnjoinOption{},
			args:        []string{"member-test-notjoined"},
			expectedErr: fmt.Errorf("cluster: %s has not joined kubeadmiral federation", "member-test-notjoined"),
		},
		{
			name:        "cluster has joined, but secret does not exist",
			option:      CommandUnjoinOption{},
			args:        []string{"member-test-no-secret"},
			expectedErr: fmt.Errorf("secret: %s/%s does not exist", "kube-admiral-system", "member-test-no-secret"),
		},
	}

	defaultConfigFlags := genericclioptions.NewConfigFlags(true).WithDiscoveryBurst(300).WithDiscoveryQPS(50.0)
	defaultConfigFlags.KubeConfig = utilpointer.String("/home/jzd/.kube/kubeadmiral/kubeadmiral.config")
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert := assert.New(t)
			_ = testCase.option.ToOptions(util.NewFactory(defaultConfigFlags), testCase.args)
			err := testCase.option.Validate()
			assert.Equal(testCase.expectedErr, err)
		})
	}
}
