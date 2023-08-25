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

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func IsResourcePropagated(sourceObject *unstructured.Unstructured, fedObject fedcorev1a1.GenericFederatedObject) (bool, error) {
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

	syncAllOk := true
	for _, cluster := range fedObject.GetStatus().Clusters {
		if cluster.Status != fedcorev1a1.ClusterPropagationOK {
			syncAllOk = false
			break
		}
	}

	return syncAllOk, nil
}

func IsResourceTemplateSyncedToMemberClusters(fedObject fedcorev1a1.GenericFederatedObject) (bool, error) {
	if fedObject == nil {
		return false, nil
	}

	// check if the latest resource template has been synced to member clusters
	return fedObject.GetStatus().SyncedGeneration == fedObject.GetGeneration(), nil
}

func IsFederatedTemplateUpToDate(sourceObject *unstructured.Unstructured, fedObject fedcorev1a1.GenericFederatedObject) (bool, error) {
	templateMap, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
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

	if reflect.DeepEqual(prunedSourceObj, templateMap.Object) {
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
