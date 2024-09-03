/*
Copyright 2024 The KubeAdmiral Authors.

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
	"sort"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	podutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
)

func parseEnvOverriders(
	helper *helpData,
	overriders []fedcorev1a1.EnvOverrider,
) (fedcorev1a1.OverridePatches, error) {
	if len(overriders) == 0 {
		return fedcorev1a1.OverridePatches{}, nil
	}

	// patchMap(<targetPath, []target]>) is used to store the newest env values of each command path
	patchMap := make(map[string][]corev1.EnvVar)

	podSpec, err := podutil.GetResourcePodSpecFromUnstructuredObj(helper.sourceObj, helper.gvk)
	if err != nil {
		return nil, fmt.Errorf("failed to get podSpec from sourceObj: %w", err)
	}

	// parse patches and store the new env values
	for index := range overriders {
		envOverrider := &overriders[index]
		containerKind, containerIndex, container := lookForMatchedContainer(podSpec, envOverrider.ContainerName)
		// if there is no matched container, this overrider is skipped
		if container == nil {
			continue
		}

		targetPath, err := generateTargetPathForPodSpec(helper.gvk, containerKind, EnvTarget, containerIndex)
		if err != nil {
			return nil, err
		}

		var currentValue []corev1.EnvVar
		if _, ok := patchMap[targetPath]; ok {
			currentValue = patchMap[targetPath]
		} else {
			currentValue = container.Env
		}

		// apply env overrider
		if patchMap[targetPath], err = applyEnvOverrider(currentValue, envOverrider); err != nil {
			return nil, fmt.Errorf("failed to apply env override: %w", err)
		}
	}

	// convert patchMap to patchList and return
	return convertEnvPatchMapToPatches(patchMap)
}

// applyEnvOverrider applies overrider to old list to generate new list
func applyEnvOverrider(value []corev1.EnvVar, envOverrider *fedcorev1a1.EnvOverrider) ([]corev1.EnvVar, error) {
	operator := envOverrider.Operator
	if operator == "" {
		operator = OperatorOverwrite
	}

	if operator == OperatorAddIfAbsent && len(envOverrider.Value) == 0 {
		return nil, fmt.Errorf("%s operation needs value", OperatorAddIfAbsent)
	}

	mapValue := map[string][]int{}
	for index, oldEnv := range value {
		if _, ok := mapValue[oldEnv.Name]; !ok {
			mapValue[oldEnv.Name] = []int{index}
		} else {
			mapValue[oldEnv.Name] = append(mapValue[oldEnv.Name], index)
		}
	}

	for _, env := range envOverrider.Value {
		switch operator {
		case OperatorAddIfAbsent:
			// addIfAbsent should not operate on existing key-value pairs
			if _, ok := mapValue[env.Name]; ok {
				return nil, fmt.Errorf("%s is not allowed to operate on exist key:%s", OperatorAddIfAbsent, env.Name)
			}
			value = append(value, env)
			mapValue[env.Name] = append(mapValue[env.Name], len(value)-1)
		case OperatorOverwrite:
			// this operation only overwrites the keys that already exist in the map
			if indexes, ok := mapValue[env.Name]; ok {
				for _, index := range indexes {
					value[index] = env
				}
			}
		case OperatorDelete:
			if indexes, ok := mapValue[env.Name]; ok {
				var tmpValue []corev1.EnvVar
				start, lastIndex := 0, 0
				for _, index := range indexes {
					for i := start; i < index; i++ {
						tmpValue = append(tmpValue, value[i])
					}
					start = index + 1
					lastIndex = index
				}
				if lastIndex != len(value)-1 {
					tmpValue = append(tmpValue, value[lastIndex+1:]...)
				}
				value = tmpValue

				mapValue = map[string][]int{}
				for index, oldEnv := range value {
					if _, ok := mapValue[oldEnv.Name]; !ok {
						mapValue[oldEnv.Name] = []int{index}
					} else {
						mapValue[oldEnv.Name] = append(mapValue[oldEnv.Name], index)
					}
				}
			}
		default:
			return nil, fmt.Errorf("unsupported operator:%s", operator)
		}
	}

	return value, nil
}

func convertEnvPatchMapToPatches(patchMap map[string][]corev1.EnvVar) (fedcorev1a1.OverridePatches, error) {
	patches := make(fedcorev1a1.OverridePatches, len(patchMap))
	index := 0
	for path, value := range patchMap {
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal env value(%v): %w", value, err)
		}
		patches[index] = fedcorev1a1.OverridePatch{
			Op:    OperatorReplace,
			Path:  path,
			Value: apiextensionsv1.JSON{Raw: jsonBytes},
		}
		index++
	}

	// sort by path to avoid unnecessary updates due to disorder
	sort.Slice(patches, func(i, j int) bool {
		return patches[i].Path < patches[j].Path
	})
	return patches, nil
}
