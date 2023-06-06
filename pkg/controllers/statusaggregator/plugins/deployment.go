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

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
)

type DeploymentPlugin struct{}

func NewDeploymentPlugin() *DeploymentPlugin {
	return &DeploymentPlugin{}
}

func (receiver *DeploymentPlugin) AggregateStatues(
	ctx context.Context,
	sourceObject, fedObject *unstructured.Unstructured,
	clusterObjs map[string]interface{},
) (*unstructured.Unstructured, bool, error) {
	logger := klog.FromContext(ctx).WithValues("status-aggregator-plugin", "deployments")

	needUpdate := false
	digests := []util.LatestReplicasetDigest{}

	sourceDeployment := &appsv1.Deployment{}
	if err := util.ConvertViaJson(sourceObject, sourceDeployment); err != nil {
		return nil, false, err
	}

	aggregatedStatus := &appsv1.DeploymentStatus{
		ObservedGeneration: sourceDeployment.Generation,
	}

	for clusterName, clusterObj := range clusterObjs {
		logger := logger.WithValues("cluster-name", clusterName)

		utd := clusterObj.(*unstructured.Unstructured)
		// For status of deployment
		var found bool
		status, found, err := unstructured.NestedMap(utd.Object, common.StatusField)
		if err != nil || !found {
			logger.Error(err, "Failed to get status of cluster object")
			return nil, false, err
		}

		if status == nil {
			continue
		}

		deployStatus := &appsv1.DeploymentStatus{}
		if err = util.ConvertViaJson(status, deployStatus); err != nil {
			logger.Error(err, "Failed to convert the status of cluster object")
			return nil, false, err
		}

		aggregatedStatus.Replicas += deployStatus.Replicas
		aggregatedStatus.UpdatedReplicas += deployStatus.UpdatedReplicas
		aggregatedStatus.ReadyReplicas += deployStatus.ReadyReplicas
		aggregatedStatus.AvailableReplicas += deployStatus.AvailableReplicas
		aggregatedStatus.UnavailableReplicas += deployStatus.UnavailableReplicas

		// ensure that the latestreplicaset annotations describe the current spec
		// (atomically consistent with the SourceGenerationAnnotation)
		if fmt.Sprintf(
			"%d",
			utd.GetGeneration(),
		) == utd.GetAnnotations()[util.LatestReplicasetObservedGenerationAnnotation] {
			digest, errs := util.LatestReplicasetDigestFromObject(clusterName, utd)

			if len(errs) == 0 {
				digests = append(digests, digest)
			} else {
				for _, err := range errs {
					logger.Error(err, "Failed to get latest replicaset digest from object")
				}
			}
		}
	}

	newStatus, err := util.GetUnstructuredStatus(aggregatedStatus)
	if err != nil {
		logger.Error(err, "Failed to convert aggregated status to unstructured")
		return nil, false, err
	}

	oldStatus, _, err := unstructured.NestedMap(sourceObject.Object, common.StatusField)
	if err != nil {
		logger.Error(err, "Failed to get old status of source object")
		return nil, false, err
	}

	// update status of source object if needed
	if !reflect.DeepEqual(newStatus, oldStatus) {
		if err := unstructured.SetNestedMap(sourceObject.Object, newStatus, common.StatusField); err != nil {
			logger.Error(err, "Failed to set the new status on source object")
			return nil, false, err
		}
		needUpdate = true
	}

	sort.Slice(digests, func(i, j int) bool {
		return digests[i].ClusterName < digests[j].ClusterName
	})

	rsDigestsAnnotationBytes, err := json.Marshal(digests)
	if err != nil {
		logger.Error(err, "Failed to marshal digests for deployment")
		return nil, false, err
	}

	rsDigestsAnnotation := string(rsDigestsAnnotationBytes)
	hasRSDigestsAnnotation, err := annotation.HasAnnotationKeyValue(
		sourceObject,
		util.LatestReplicasetDigestsAnnotation,
		rsDigestsAnnotation,
	)
	if err != nil {
		logger.Error(err, "Failed to ensure annotations for deployment")
		return nil, false, err
	}

	if hasRSDigestsAnnotation {
		return sourceObject, needUpdate, nil
	} else {
		needUpdate = true
	}

	_, err = annotation.AddAnnotation(sourceObject, util.LatestReplicasetDigestsAnnotation, rsDigestsAnnotation)
	if err != nil {
		logger.Error(err, "Failed to add annotations for deployment")
		return nil, false, err
	}

	return sourceObject, needUpdate, nil
}
