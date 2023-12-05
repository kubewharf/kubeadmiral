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
	"sort"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	podutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
)

// parseEntrypointOverriders parse OverridePatches from command/args overriders
func parseEntrypointOverriders(
	fedObject fedcorev1a1.GenericFederatedObject,
	overriders []fedcorev1a1.EntrypointOverrider,
	target string,
) (fedcorev1a1.OverridePatches, error) {
	if len(overriders) == 0 {
		return fedcorev1a1.OverridePatches{}, nil
	}

	// patchMap(<targetPath, []target]>) is used to store the newest command/args values of each command path
	patchMap := make(map[string][]string)

	// get gvk from fedObj
	gvk, err := getGVKFromFederatedObject(fedObject)
	if err != nil {
		return nil, err
	}
	podSpec, err := podutil.GetResourcePodSpec(fedObject, gvk)
	if err != nil {
		return nil, fmt.Errorf("failed to get podSpec from sourceObj: %w", err)
	}

	// parse patches and store the new command/args values in patchMap
	for index := range overriders {
		entrypointOverrider := &overriders[index]
		containerKind, containerIndex, container := lookForMatchedContainer(podSpec, entrypointOverrider.ContainerName)
		// if there is no matched container, this overrider is skipped
		if container == nil {
			continue
		}

		targetPath, err := generateTargetPathForPodSpec(gvk, containerKind, target, containerIndex)
		if err != nil {
			return nil, err
		}

		var currentValue []string
		if _, ok := patchMap[targetPath]; ok {
			currentValue = patchMap[targetPath]
		} else {
			currentValue = getCommandOrArgsFromContainer(container, target)
		}

		// apply entrypoint overrider
		if patchMap[targetPath], err = applyEntrypointOverrider(currentValue, entrypointOverrider); err != nil {
			return nil, fmt.Errorf("failed to apply %s override: %w", target, err)
		}
	}

	// convert patchMap to patchList and return
	return convertPatchMapToPatches(patchMap, target)
}

// lookForMatchedContainer find matchedContainer from init containers or containers
func lookForMatchedContainer(
	podSpec *corev1.PodSpec,
	containerName string,
) (containerKind string, containerIndex int, container *corev1.Container) {
	containerKinds := []string{InitContainers, Containers}
	for i, containers := range [][]corev1.Container{podSpec.InitContainers, podSpec.Containers} {
		for index, item := range containers {
			if containerName == item.Name {
				containerKind = containerKinds[i]
				containerIndex = index
				container = &containers[index]
				return
			}
		}
	}
	return
}

func getCommandOrArgsFromContainer(container *corev1.Container, target string) []string {
	if target == CommandTarget {
		return container.Command
	}
	return container.Args
}

// applyEntrypointOverrider applies overrider to old list to generate new list
func applyEntrypointOverrider(oldValue []string, entrypointOverrider *fedcorev1a1.EntrypointOverrider) ([]string, error) {
	operator := entrypointOverrider.Operator
	var newValue []string
	switch operator {
	case OperatorAppend:
		if len(entrypointOverrider.Value) == 0 {
			return nil, fmt.Errorf("%s operation needs non-empty list", OperatorAppend)
		}
		newValue = append(oldValue, entrypointOverrider.Value...)
	case OperatorOverwrite:
		newValue = entrypointOverrider.Value
	case OperatorDelete:
		entrypointOverriderSet := sets.NewString(entrypointOverrider.Value...)
		for _, value := range oldValue {
			if !entrypointOverriderSet.Has(value) {
				newValue = append(newValue, value)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported operator:%s", operator)
	}
	return newValue, nil
}

// convertPatchMapToPatches converts patchMap to patchList
func convertPatchMapToPatches(patchMap map[string][]string, target string) (fedcorev1a1.OverridePatches, error) {
	patches := make(fedcorev1a1.OverridePatches, len(patchMap))
	index := 0
	for path, value := range patchMap {
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal %s value(%v): %w", target, value, err)
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
