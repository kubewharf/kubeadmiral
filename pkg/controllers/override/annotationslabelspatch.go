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

package override

import (
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func parseStringMapOverriders(
	fedObject fedcorev1a1.GenericFederatedObject,
	overriders []fedcorev1a1.StringMapOverrider,
	target string,
) (fedcorev1a1.OverridePatches, error) {
	if len(overriders) == 0 {
		return fedcorev1a1.OverridePatches{}, nil
	}

	sourceObj, err := fedObject.GetSpec().GetTemplateAsUnstructured()
	if err != nil {
		return nil, fmt.Errorf("failed to get sourceObj from fedObj: %w", err)
	}

	// get labels or annotations of sourceObj
	mapValue := getLabelsOrAnnotationsFromObject(sourceObj, target)

	// apply StringMapOverriders to mapValue
	for index := range overriders {
		mapValue, err = applyStringMapOverrider(mapValue, &overriders[index])
		if err != nil {
			return nil, fmt.Errorf("failed to apply %s overrider: %w", target, err)
		}
	}

	// marshal mapValue into jsonBytes
	jsonBytes, err := json.Marshal(mapValue)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s value(%v): %w", target, mapValue, err)
	}

	return fedcorev1a1.OverridePatches{
		{
			Op:    OperatorReplace,
			Path:  fmt.Sprintf("/metadata/%s", target),
			Value: apiextensionsv1.JSON{Raw: jsonBytes},
		},
	}, nil
}

func getLabelsOrAnnotationsFromObject(rawObj *unstructured.Unstructured, target string) map[string]string {
	if target == LabelsTarget {
		return rawObj.GetLabels()
	}
	return rawObj.GetAnnotations()
}

// applyStringMapOverrider
func applyStringMapOverrider(mapValue map[string]string, stringMapOverrider *fedcorev1a1.StringMapOverrider) (map[string]string, error) {
	if mapValue == nil {
		mapValue = make(map[string]string)
	}
	overriderMap := stringMapOverrider.Value

	operator := stringMapOverrider.Operator
	if operator == "" {
		operator = OperatorOverwrite
	}

	if operator == OperatorAddIfAbsent && len(stringMapOverrider.Value) == 0 {
		return nil, fmt.Errorf("%s operation needs value", OperatorAddIfAbsent)
	}

	for key, value := range overriderMap {
		switch operator {
		case OperatorAddIfAbsent:
			// addIfAbsent should not operate on existing key-value pairs
			if _, ok := mapValue[key]; ok {
				return nil, fmt.Errorf("%s is not allowed to operate on exist key:%s", OperatorAddIfAbsent, key)
			}
			mapValue[key] = value
		case OperatorOverwrite:
			// this operation only overwrites the keys that already exist in the map
			if _, ok := mapValue[key]; ok {
				mapValue[key] = value
			}
		case OperatorDelete:
			delete(mapValue, key)
		}
	}
	return mapValue, nil
}
