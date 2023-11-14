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
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	podutil "github.com/kubewharf/kubeadmiral/pkg/util/pod"
)

const (
	pathSeparator = "/"

	Registry   = "Registry"
	Repository = "Repository"
	Tag        = "Tag"
	Digest     = "Digest"
)

func parseImageOverriders(
	fedObject fedcorev1a1.GenericFederatedObject,
	imageOverriders []fedcorev1a1.ImageOverrider,
) (fedcorev1a1.OverridePatches, error) {
	// patchMap(<imagePath, imageValue>) is used to store the newest image value of each image path
	patchMap := make(map[string]string)

	// parse patches and store the new image values in patchMap
	for i := range imageOverriders {
		imageOverrider := &imageOverriders[i]
		if imageOverrider.ImagePath != "" {
			if err := parsePatchesFromImagePath(fedObject, imageOverrider, patchMap); err != nil {
				return nil, err
			}
		} else {
			if err := parsePatchesFromWorkload(fedObject, imageOverrider, patchMap); err != nil {
				return nil, err
			}
		}
	}

	// convert patchMap to patchList and return
	patches := make(fedcorev1a1.OverridePatches, len(patchMap))
	index := 0
	for path, value := range patchMap {
		patches[index] = fedcorev1a1.OverridePatch{
			Op:   OperatorReplace,
			Path: path,
			// OverridePatch's value is apiextensionsv1.JSON type, which raw should be wrapped with "".
			Value: apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf("\"%s\"", value))},
		}
		index++
	}

	// sort by path to avoid unnecessary updates due to disorder
	sort.Slice(patches, func(i, j int) bool {
		return patches[i].Path < patches[j].Path
	})

	return patches, nil
}

// parsePatchesFromImagePath applies overrider to image value on the specified path,
// and generates a patch which is stored in patchMap
func parsePatchesFromImagePath(
	fedObject fedcorev1a1.GenericFederatedObject,
	imageOverrider *fedcorev1a1.ImageOverrider,
	patchMap map[string]string,
) error {
	imagePath := imageOverrider.ImagePath
	if !strings.HasPrefix(imagePath, pathSeparator) {
		return fmt.Errorf("image path should be start with %s", pathSeparator)
	}
	imageValue := patchMap[imagePath]

	// get image value
	if imageValue == "" {
		// get source obj
		sourceObj, err := fedObject.GetSpec().GetTemplateAsUnstructured()
		if err != nil {
			return fmt.Errorf("failed to get sourceObj from fedObj: %w", err)
		}

		imageValue, err = GetStringFromUnstructuredObj(sourceObj, imageOverrider.ImagePath)
		if err != nil {
			return fmt.Errorf("failed to parse image value from unstructured obj: %w", err)
		}
	}

	// apply image override
	newImageValue, err := applyImageOverrider(imageValue, imageOverrider)
	if err != nil {
		return fmt.Errorf("failed to apply image override: %w", err)
	}
	patchMap[imagePath] = newImageValue

	return nil
}

// parsePatchesFromWorkload applies overrider to image value on the default path of workload,
// and generates a patch which is stored in patchMap
func parsePatchesFromWorkload(
	fedObject fedcorev1a1.GenericFederatedObject,
	imageOverrider *fedcorev1a1.ImageOverrider,
	patchMap map[string]string,
) error {
	// get pod spec from fedObj
	gvk, err := getGVKFromFederatedObject(fedObject)
	if err != nil {
		return err
	}
	podSpec, err := podutil.GetResourcePodSpec(fedObject, gvk)
	if err != nil {
		return fmt.Errorf("failed to get podSpec from sourceObj: %w", err)
	}

	// convert user-specified containerNames into set
	specifiedContainers := sets.New[string]()
	for _, name := range imageOverrider.ContainerNames {
		if name != "" {
			specifiedContainers.Insert(name)
		}
	}

	containerKinds := []string{InitContainers, Containers}
	// process the init containers and containers
	for i, containers := range [][]corev1.Container{podSpec.InitContainers, podSpec.Containers} {
		for containerIndex, container := range containers {
			if len(imageOverrider.ContainerNames) == 0 || specifiedContainers.Has(container.Name) {
				imagePath, err := generateTargetPathForPodSpec(gvk, containerKinds[i], ImageTarget, containerIndex)
				if err != nil {
					return err
				}

				imageValue := container.Image
				if val, ok := patchMap[imagePath]; ok {
					imageValue = val
				}

				newImageValue, err := applyImageOverrider(imageValue, imageOverrider)
				if err != nil {
					return fmt.Errorf("failed to apply image overrider: %w", err)
				}
				patchMap[imagePath] = newImageValue
			}
		}
	}

	return nil
}

// applyImageOverrider applies overriders to oldImage to generate a new value
func applyImageOverrider(oldImage string, imageOverrider *fedcorev1a1.ImageOverrider) (string, error) {
	// parse oldImage value into different parts for easier operation
	newImage, err := ParseImage(oldImage)
	if err != nil {
		return "", fmt.Errorf("parse image failed(%s), error: %w", oldImage, err)
	}

	// apply operations in image override
	for _, operation := range imageOverrider.Operations {
		operator, value, component := operation.Operator, operation.Value, operation.ImageComponent

		// if omitted, defaults to "overwrite"
		if operator == "" {
			operator = OperatorOverwrite
		}

		switch component {
		case Registry:
			if err := newImage.OperateRegistry(operator, value); err != nil {
				return "", err
			}
		case Repository:
			if err := newImage.OperateRepository(operator, value); err != nil {
				return "", err
			}
		case Tag:
			if err := newImage.OperateTag(operator, value); err != nil {
				return "", err
			}
		case Digest:
			if err := newImage.OperateDigest(operator, value); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported image component(%s)", component)
		}
	}

	return newImage.String(), nil
}
