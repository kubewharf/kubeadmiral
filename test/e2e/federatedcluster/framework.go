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

package federatedcluster

import (
	"time"

	"github.com/onsi/ginkgo/v2"

	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
)

var (
	federatedClusterTestLabels = ginkgo.Label("federated-cluster", framework.TestLabelSlow, framework.TestLabelNeedCreateCluster)

	// cluster join e2e test timeouts
	clusterJoinTimeout        = 30 * time.Second
	clusterJoinTimeoutTimeout = 10 * time.Minute

	// cluster delete e2e test timeouts
	clusterReadyTimeout        = 1 * time.Minute
	resourcePropagationTimeout = 1 * time.Minute
	clusterDeleteTimeout       = 2 * time.Minute

	// cluster status e2e test timeouts
	clusterStatusCollectTimeout = 5 * time.Second
	clusterStatusUpdateInterval = time.Minute
)
