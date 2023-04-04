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

// The design and implementation of the scheduler is heavily inspired by kube-scheduler and karmada-scheduler. Kudos!

package scheduler

import (
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
	"k8s.io/client-go/dynamic"
)

func (s *Scheduler) buildFrameworkHandle() framework.Handle {
	return &handle{
		dynamicClient: s.dynamicClient,
	}
}

type handle struct {
	dynamicClient dynamic.Interface
}

func (f *handle) DynamicClient() dynamic.Interface {
	return f.dynamicClient
}
