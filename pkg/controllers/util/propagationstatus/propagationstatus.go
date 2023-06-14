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

package propagationstatus

import (
	"fmt"
	"reflect"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

func IsResourcePropagated(sourceObject, fedObject *unstructured.Unstructured) (bool, error) {
	if sourceObject == nil {
		return false, fmt.Errorf("source object can't be nil")
	}

	if fedObject == nil {
		return false, nil
	}

	updated, err := IsFederatedTemplateUpToDate(sourceObject, fedObject)
	if err != nil || !updated {
		return updated, err
	}

	synced, err := IsResourceTemplateSyncedToMemberClusters(fedObject)
	if err != nil || !synced {
		return synced, err
	}

	resource := &fedtypesv1a1.GenericObjectWithStatus{}
	err = util.UnstructuredToInterface(fedObject, resource)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshall to generic resource: %w", err)
	}

	if resource.Status == nil {
		return false, nil
	}

	syncAllOk := true
	for _, cluster := range resource.Status.Clusters {
		if cluster.Status != fedtypesv1a1.ClusterPropagationOK {
			syncAllOk = false
			break
		}
	}

	return syncAllOk, nil
}

func IsResourceTemplateSyncedToMemberClusters(fedObject *unstructured.Unstructured) (bool, error) {
	if fedObject == nil {
		return false, nil
	}

	observedGeneration, found, err := unstructured.NestedInt64(fedObject.Object, "status", "syncedGeneration")
	if err != nil || !found {
		return false, err
	}

	// check if the latest resource template has been synced to member clusters
	return observedGeneration == fedObject.GetGeneration(), nil
}

func IsFederatedTemplateUpToDate(sourceObject, fedObject *unstructured.Unstructured) (bool, error) {
	templateMap, exists, err := unstructured.NestedMap(fedObject.Object, "spec", "template")
	if err != nil || !exists {
		return false, err
	}

	fedAnnotations := fedObject.GetAnnotations()
	if len(fedAnnotations) == 0 {
		return false, nil
	}

	if !areStringMapsSynced(
		sourceObject.GetAnnotations(),
		fedAnnotations,
		fedAnnotations[common.ObservedAnnotationKeysAnnotation],
	) {
		return false, nil
	}

	if !areStringMapsSynced(
		sourceObject.GetLabels(),
		fedObject.GetLabels(),
		fedAnnotations[common.ObservedLabelKeysAnnotation],
	) {
		return false, nil
	}

	prunedSourceObj, err := pruneSourceObj(
		sourceObject,
		[]byte(fedAnnotations[common.TemplateGeneratorMergePatchAnnotation]),
	)
	if err != nil {
		return false, err
	}

	if reflect.DeepEqual(prunedSourceObj, templateMap) {
		return true, nil
	}

	return false, nil
}

func areStringMapsSynced(
	sourceMap, federatedMap map[string]string,
	observedKeys string,
) bool {
	fedKeys, ignoredKeys := parseObservedKeys(observedKeys)
	allKeys := fedKeys.Union(ignoredKeys)

	if !allKeys.Equal(sets.KeySet(sourceMap)) {
		return false
	}

	for key := range fedKeys {
		if sourceMap[key] != federatedMap[key] {
			return false
		}
	}

	return true
}

func parseObservedKeys(keys string) (fed, nonFed sets.Set[string]) {
	keySegments := strings.Split(keys, "|")
	if len(keySegments) != 2 {
		return nil, nil
	}

	if len(keySegments[0]) != 0 {
		fed = sets.New(strings.Split(keySegments[0], ",")...)
	}

	if len(keySegments[1]) != 0 {
		nonFed = sets.New(strings.Split(keySegments[1], ",")...)
	}
	return
}

func pruneSourceObj(sourceObj *unstructured.Unstructured, mergePatch []byte) (map[string]interface{}, error) {
	sourceJSON, err := sourceObj.MarshalJSON()
	if err != nil {
		return nil, err
	}

	prunedSourceJSON, err := jsonpatch.MergePatch(
		sourceJSON,
		mergePatch,
	)
	if err != nil {
		return nil, err
	}

	var ret map[string]any
	// `encoding/json` decodes all json numbers (including integers) to float64.
	// However, `client-go` informers decode integers to int64.
	// We follow `client-go`'s decoding to ensure reflect.DeepEqual can report equality correctly.
	if err := utiljson.Unmarshal(prunedSourceJSON, &ret); err != nil {
		return nil, err
	}
	return ret, nil
}
