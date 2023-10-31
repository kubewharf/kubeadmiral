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
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	utilreference "github.com/kubewharf/kubeadmiral/pkg/lifted/distribution/reference"
)

type Image struct {
	registry   string
	repository string
	tag        string
	digest     string
}

func (i *Image) OperateRegistry(operator, value string) error {
	newRegistry, err := operateImageComponent(i.registry, operator, value)
	if err != nil {
		return err
	}
	i.registry = newRegistry
	return nil
}

func (i *Image) OperateRepository(operator, value string) error {
	newRepository, err := operateImageComponent(i.repository, operator, value)
	if err != nil {
		return err
	}
	i.repository = newRepository
	return nil
}

func (i *Image) OperateTag(operator, value string) error {
	newTag, err := operateImageComponent(i.tag, operator, value)
	if err != nil {
		return err
	}

	if operator != OperatorRemove && !utilreference.AnchoredTagRegexp.MatchString(newTag) {
		return fmt.Errorf("invalid tag format after applying the overrider")
	}

	i.tag = newTag
	return nil
}

func (i *Image) OperateDigest(operator, value string) error {
	newDigest, err := operateImageComponent(i.digest, operator, value)
	if err != nil {
		return err
	}

	if operator != OperatorRemove && !utilreference.AnchoredDigestRegexp.MatchString(newDigest) {
		return fmt.Errorf("invalid digest format after applying the overrider")
	}

	i.digest = newDigest
	return nil
}

func operateImageComponent(oldValue, operator, newValue string) (string, error) {
	switch operator {
	case OperatorAdd:
		if newValue == "" {
			return "", fmt.Errorf("add operation needs value")
		}
		return newValue, nil
	case OperatorReplace:
		if newValue == "" {
			return "", fmt.Errorf("replace operation needs value")
		}
		if oldValue == "" {
			return "", fmt.Errorf("replace is not allowed to operate on empty value")
		}
		return newValue, nil
	case OperatorRemove:
		return "", nil
	}
	return "", fmt.Errorf("unsupported operator")
}

func (i *Image) String() string {
	fullName := i.repository

	if i.registry != "" {
		fullName = i.registry + "/" + i.repository
	}

	if i.tag != "" {
		fullName = fullName + ":" + i.tag
	}

	if i.digest != "" {
		fullName = fullName + "@" + i.digest
	}

	return fullName
}

// ParseImage returns an Image from the given string.
func ParseImage(fullName string) (*Image, error) {
	ref, err := reference.Parse(fullName)
	if err != nil {
		return nil, err
	}

	image := &Image{}
	if named, ok := ref.(reference.Named); ok {
		image.registry, image.repository = SplitRegistryAndRepository(named.Name())
	}

	if tagged, ok := ref.(reference.Tagged); ok {
		image.tag = tagged.Tag()
	}

	if digested, ok := ref.(reference.Digested); ok {
		image.digest = digested.Digest().String()
	}

	return image, nil
}

// SplitRegistryAndRepository refers to https://github.com/distribution/distribution/blob/v2.7.1/reference/normalize.go#L62
// The reason for using this function here is due to this discussion: https://stackoverflow.com/a/37867949
// If we use reference.Domain() and reference.Path() for parsing, the following problems will occur:
//   - "ealen/echo-server" is resolved to <registry: "ealen", repository : "echo-server">
//
// Using the follow function ensures that the above image is parsed into the following content:
//   - "ealen/echo-server" is resolved to <registry: "", repository : "ealen/echo-server">
func SplitRegistryAndRepository(name string) (registry, repository string) {
	i := strings.IndexRune(name, '/')
	if i == -1 || (!strings.ContainsAny(name[:i], ".:") && name[:i] != "localhost") {
		registry, repository = "", name
	} else {
		registry, repository = name[:i], name[i+1:]
	}
	return
}

func GetStringFromUnstructuredObj(input *unstructured.Unstructured, path string) (string, error) {
	fields := strings.Split(strings.Trim(path, pathSeparator), pathSeparator)
	var currentObj interface{} = input.Object

	for i, field := range fields {
		switch obj := currentObj.(type) {
		case map[string]interface{}:
			currentObj = obj[field]
		case []interface{}:
			index, err := strconv.ParseInt(field, 10, 32)
			if err != nil {
				return "", fmt.Errorf(
					"slice found at path %s, but the next path segment is not a number (found %q)",
					strings.Join(fields[:i], "/"),
					field,
				)
			}
			if index < 0 || int(index) >= len(obj) {
				return "", fmt.Errorf("index(%d) is negative or out of range", index)
			}
			currentObj = obj[index]
		default:
			return "", fmt.Errorf(
				"%s accessor error: unknown type %T, expected map[string]interface{} or []interface{}",
				strings.Join(fields[:i], "/"),
				obj,
			)
		}
	}

	value, ok := currentObj.(string)
	if !ok {
		return "", fmt.Errorf("%q does not point to a string field", path)
	}
	return value, nil
}
