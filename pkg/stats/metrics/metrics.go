/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package metrics

const (
	ClusterJoinedDuration         = "cluster_joined_duration"
	ClusterReadyState             = "cluster_ready_state"
	ClusterOfflineState           = "cluster_offline_state"
	ClusterJoinedState            = "cluster_joined_state"
	ClusterMemoryAllocatableBytes = "cluster_memory_allocatable_bytes"
	ClusterCPUAllocatableNumber   = "cluster_cpu_allocatable_number"
	ClusterMemoryAvailableBytes   = "cluster_memory_available_bytes"
	ClusterCPUAvailableNumber     = "cluster_cpu_available_number"
	ClusterSchedulableNodesTotal  = "cluster_schedulable_nodes_total"
	ClusterSyncStatusDuration     = "cluster_sync_status_duration"
	ClusterDeletionState          = "cluster_deletion_state"

	ScheduleAttemptsTotal                    = "schedule_attempts_total"
	SchedulingAttemptDuration                = "scheduling_attempt_duration"
	QueueIncomingFederatedObjectTotal        = "queue_incoming_federated_object_total"
	SchedulerLatency                         = "scheduler_latency"
	SchedulerThroughput                      = "scheduler_throughput"
	SchedulerFrameworkExtensionPointDuration = "scheduler_framework_extension_point_duration"
	SchedulerPluginExecutionDuration         = "scheduler_plugin_execution_duration"

	FederatedClusterControllerLatency              = "federated_cluster_controller_latency"
	FederatedClusterControllerThroughput           = "federated_cluster_controller_throughput"
	FederatedClusterControllerCollectStatusLatency = "federated_cluster_controller_collect_status_latency"
	OverridePolicyControllerLatency                = "override_policy_controller_latency"
	OverridePolicyControllerThroughput             = "override_policy_controller_throughput"
	PolicyRCCountControllerThroughput              = "policyrc_count_controller_throughput"
	PolicyRCCountLatency                           = "policyrc_count_latency"
	PolicyRCPersistLatency                         = "policyrc_persist_latency"
	PolicyRCPersistThroughput                      = "policyrc_persist_throughput"
	FollowerControllerLatency                      = "follower_controller_latency"
	FollowerControllerThroughput                   = "follower_controller_throughput"
	FollowersTotal                                 = "followers_total"
	LeadersTotal                                   = "leaders_total"
	StatusCollectionDuration                       = "status_collection_duration"
	StatusCollectionErrorTotal                     = "status_collection_error_total"
	MemberOperationError                           = "member_operation_error"
	DispatchOperationErrorTotal                    = "dispatch_operation_error_total"
	DispatchOperationDuration                      = "dispatch_operation_duration"
	NamespaceAutoPropagationControllerLatency      = "namespace_auto_propagation_controller_latency"
	NamespaceAutoPropagationControllerThroughput   = "namespace_auto_propagation_controller_throughput"
	NamespacePropagateFailedTotal                  = "namespace_propagate_failed_total"
	StatusLatency                                  = "status_latency"
	StatusThroughput                               = "status_throughput"
	SyncLatency                                    = "sync_latency"
	SyncThroughput                                 = "sync_throughput"
	StatusAggregatorLatency                        = "status_aggregator_latency"
	StatusAggregatorThroughput                     = "status_aggregator_throughput"
	AutoMigrationLatency                           = "auto_migration_latency"
	AutoMigrationThroughput                        = "auto_migration_throughput"
	FederateControllerLatency                      = "federate_controller_latency"
	FederateControllerThroughput                   = "federate_controller_throughput"
	FederateHPAControllerLatency                   = "federate_hpa_controller_latency"
	FederateHPAControllerThroughput                = "federate_hpa_controller_throughput"
)
