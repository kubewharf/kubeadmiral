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

package pendingcontrollers

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	annotationutil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
)

type PendingControllers [][]string

const (
	PendingControllersAnnotation = common.DefaultPrefix + "pending-controllers"
)

func GetPendingControllers(fedObject *unstructured.Unstructured) (PendingControllers, error) {
	value, exists := fedObject.GetAnnotations()[PendingControllersAnnotation]
	if !exists {
		return nil, fmt.Errorf("annotation %v does not exist", PendingControllersAnnotation)
	}

	var controllers PendingControllers
	if err := json.Unmarshal([]byte(value), &controllers); err != nil {
		return nil, fmt.Errorf("failed to unmarshal json: %w", err)
	}

	return NormalizeControllers(controllers), nil
}

// Returns a deep copy of controllers, with 0-length inner slices removed.
// The outer slice is guaranteed to be non-nil if empty for less ambiguity.
func NormalizeControllers(controllers PendingControllers) PendingControllers {
	output := make([][]string, 0, len(controllers))
	for _, inner := range controllers {
		if len(inner) == 0 {
			continue
		}

		newInner := make([]string, 0, len(inner))
		newInner = append(newInner, inner...)
		output = append(output, newInner)
	}

	return output
}

func SetPendingControllers(
	fedObject *unstructured.Unstructured,
	controllers PendingControllers,
) (updated bool, err error) {
	controllers = NormalizeControllers(controllers)
	annotationValue, err := json.Marshal(controllers)
	if err != nil {
		return false, fmt.Errorf("failed to marshal json: %w", err)
	}
	updated, err = annotationutil.AddAnnotation(fedObject, PendingControllersAnnotation, string(annotationValue))
	if err != nil {
		return updated, fmt.Errorf("failed to add annotation: %w", err)
	}
	return updated, err
}

func getDownstreamControllers(allControllers PendingControllers, current string) PendingControllers {
	for i, controllerGroup := range allControllers {
		for _, controller := range controllerGroup {
			if controller == current {
				return allControllers[i+1:]
			}
		}
	}
	return nil
}

func UpdatePendingControllers(
	fedObject *unstructured.Unstructured,
	toRemove string,
	shouldSetDownstream bool,
	allControllers PendingControllers,
) (updated bool, err error) {
	pendingControllers, err := GetPendingControllers(fedObject)
	if err != nil {
		return false, fmt.Errorf("failed to get remaining pending controllers: %w", err)
	}

	var currentPendingControllers []string
	var restPendingControllers PendingControllers
	if len(pendingControllers) > 0 {
		currentPendingControllers = pendingControllers[0]
		restPendingControllers = pendingControllers[1:]
	}

	for i, controller := range currentPendingControllers {
		if controller == toRemove {
			currentPendingControllers = append(currentPendingControllers[:i], currentPendingControllers[i+1:]...)
			break
		}
	}

	if shouldSetDownstream {
		restPendingControllers = getDownstreamControllers(allControllers, toRemove)
	}

	newPendingControllers := make(PendingControllers, 0, 1+len(restPendingControllers))
	newPendingControllers = append(newPendingControllers, currentPendingControllers)
	newPendingControllers = append(newPendingControllers, restPendingControllers...)
	return SetPendingControllers(fedObject, newPendingControllers)
}

func ControllerDependenciesFulfilled(fedObject *unstructured.Unstructured, controllerName string) (bool, error) {
	pendingControllers, err := GetPendingControllers(fedObject)
	if err != nil {
		return false, err
	}

	if len(pendingControllers) == 0 {
		return true, nil
	}

	currentControllerGroup := pendingControllers[0]

	for _, controller := range currentControllerGroup {
		if controller == controllerName {
			return true, nil
		}
	}

	return false, nil
}
