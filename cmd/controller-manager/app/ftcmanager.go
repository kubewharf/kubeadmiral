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

package app

import (
	"github.com/kubewharf/kubeadmiral/pkg/controllermanager"
)

const (
	FederateControllerName      = "federate"
	GlobalSchedulerName         = "scheduler"
	AutoMigrationControllerName = "automigration"
)

var knownFTCSubControllers = map[string]controllermanager.FTCSubControllerInitFuncs{
	GlobalSchedulerName: {
		StartFunc:     startGlobalScheduler,
		IsEnabledFunc: isGlobalSchedulerEnabled,
	},
	FederateControllerName: {
		StartFunc:     startFederateController,
		IsEnabledFunc: isFederateControllerEnabled,
	},
	AutoMigrationControllerName: {
		StartFunc:     startAutoMigrationController,
		IsEnabledFunc: isAutoMigrationControllerEnabled,
	},
}
