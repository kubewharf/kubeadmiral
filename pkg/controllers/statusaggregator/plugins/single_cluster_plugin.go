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
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

// NewSingleClusterPlugin aggregates status for resources that are only dispatched to a single cluster, such as Job, StatefulSet.
func NewSingleClusterPlugin() *SingleClusterPlugin {
	return &SingleClusterPlugin{}
}

type SingleClusterPlugin struct{}

func (receiver *SingleClusterPlugin) AggregateStatues(
	sourceObject, fedObject *unstructured.Unstructured,
	clusterObjs map[string]interface{},
) (*unstructured.Unstructured, bool, error) {
	needUpdate := false

	if len(clusterObjs) == 0 {
		// no member objects to sync from
		return sourceObject, false, nil
	}

	if len(clusterObjs) > 1 {
		klog.Warningf(
			"fedObject %s associated with %d cluster objects, only 1 is supported by default status aggregator plugin.",
			sourceObject.GetName(),
			len(clusterObjs),
		)
		return sourceObject, false, nil
	}

	// We only return the first clusterObjs's status.
	var clusterName string
	var clusterObj *unstructured.Unstructured
	for k, v := range clusterObjs {
		clusterName, clusterObj = k, v.(*unstructured.Unstructured)
	}

	newStatus, found, err := unstructured.NestedMap(clusterObj.Object, common.StatusField)
	if err != nil {
		klog.Errorf(
			"Failed to get status of cluster resource object %q for cluster %q",
			sourceObject.GetName(),
			clusterName,
		)
		return nil, false, err
	}
	if !found || newStatus == nil {
		// no status to update
		return sourceObject, false, nil
	}

	oldStatus, _, err := unstructured.NestedMap(sourceObject.Object, common.StatusField)
	if err != nil {
		klog.Errorf("Failed to get old status of cluster resource object %q with err: %s", sourceObject.GetName(), err)
		return nil, false, err
	}

	// update status of source object if needed
	if !reflect.DeepEqual(newStatus, oldStatus) {
		if err := unstructured.SetNestedMap(sourceObject.Object, newStatus, common.StatusField); err != nil {
			klog.Errorf(
				"Failed to set the new status of cluster resource object %q with err %s",
				sourceObject.GetName(),
				err,
			)
			return nil, false, err
		}

		needUpdate = true
	}

	return sourceObject, needUpdate, nil
}
