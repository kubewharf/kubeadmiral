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

package scheduler

import (
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const (
	GlobalSchedulerName         = "global-scheduler"
	PrefixedGlobalSchedulerName = common.DefaultPrefix + "global-scheduler"

	// Marks that the annotated object must follow the placement of the followed object.
	// Value is in the form G/V/R/ns/name, e.g. `types.kubeadmiral.io/v1alpha1/federateddeployments/default/fed-dp-xxx`.
	FollowsObjectAnnotation = common.DefaultPrefix + "follows-object"

	SchedulingModeAnnotation     = common.DefaultPrefix + "scheduling-mode"
	StickyClusterAnnotation      = common.DefaultPrefix + "sticky-cluster"
	StickyClusterAnnotationTrue  = "true"
	StickyClusterAnnotationFalse = "false"
	TolerationsAnnotations       = common.DefaultPrefix + "tolerations"
	PlacementsAnnotations        = common.DefaultPrefix + "placements"
	ClusterSelectorAnnotations   = common.DefaultPrefix + "clusterSelector"
	AffinityAnnotations          = common.DefaultPrefix + "affinity"
	MaxClustersAnnotations       = common.DefaultPrefix + "maxClusters"

	DefaultSchedulingMode = fedcorev1a1.SchedulingModeDuplicate

	EventReasonScheduleFederatedObject   = "ScheduleFederatedObject"
	EventReasonInvalidFollowsObject      = "InvalidFollowsObject"
	EventReasonWebhookConfigurationError = "WebhookConfigurationError"
	EventReasonWebhookRegistered         = "WebhookRegistered"

	SchedulingTriggersAnnotation        = common.DefaultPrefix + "scheduling-triggers"
	SchedulingDeferredReasonsAnnotation = common.DefaultPrefix + "scheduling-deferred-reasons"
)
