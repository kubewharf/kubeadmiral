//go:build e2e

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

package e2e

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	// Remember to import the package for tests to be discovered by ginkgo
	_ "github.com/kubewharf/kubeadmiral/test/e2e/automigration"
	_ "github.com/kubewharf/kubeadmiral/test/e2e/federatedcluster"
	_ "github.com/kubewharf/kubeadmiral/test/e2e/resourcepropagation"
	_ "github.com/kubewharf/kubeadmiral/test/e2e/schedulingprofile"
	// _ "github.com/kubewharf/kubeadmiral/test/e2e/example"
)

func TestMain(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "KubeAdmiral E2E Tests")
}
