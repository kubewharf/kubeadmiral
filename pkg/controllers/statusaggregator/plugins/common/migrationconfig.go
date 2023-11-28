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

package common

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type MigrationConfigPlugin struct{}

func NewMigrationConfigPlugin() *MigrationConfigPlugin {
	return &MigrationConfigPlugin{}
}

// TODO: Create a new interface that is different from the original `AggregateStatuses`
func (receiver *MigrationConfigPlugin) AggregateStatuses(
	ctx context.Context,
	sourceObject *unstructured.Unstructured,
	fedObject fedcorev1a1.GenericFederatedObject,
	clusterObjs map[string]interface{},
	clusterObjsUpToDate bool,
) (*unstructured.Unstructured, bool, error) {
	needUpdate := false

	fedObjMigrationConfig, fedAnnotationExists := fedObject.GetAnnotations()[common.AppliedMigrationConfigurationAnnotation]
	if sourceObject.GetAnnotations() == nil {
		if fedAnnotationExists {
			newAnnotation := make(map[string]string, 1)
			newAnnotation[common.AppliedMigrationConfigurationAnnotation] = fedObjMigrationConfig
			sourceObject.SetAnnotations(newAnnotation)
			needUpdate = true
		}
		return sourceObject, needUpdate, nil
	}
	sourceObjMigrationConfig, sourceAnnotationExists := sourceObject.GetAnnotations()[common.AppliedMigrationConfigurationAnnotation]

	// update annotations of source object if needed
	if !fedAnnotationExists && sourceAnnotationExists {
		updatedAnnotation := sourceObject.GetAnnotations()
		delete(updatedAnnotation, common.AppliedMigrationConfigurationAnnotation)
		sourceObject.SetAnnotations(updatedAnnotation)
		needUpdate = true
	}

	if fedAnnotationExists && !reflect.DeepEqual(sourceObjMigrationConfig, fedObjMigrationConfig) {
		updatedAnnotation := sourceObject.GetAnnotations()
		updatedAnnotation[common.AppliedMigrationConfigurationAnnotation] = fedObjMigrationConfig
		sourceObject.SetAnnotations(updatedAnnotation)
		needUpdate = true
	}

	return sourceObject, needUpdate, nil
}
