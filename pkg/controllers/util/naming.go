/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package util

import (
	"fmt"
	"hash/fnv"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

// The functions in this file are exposed as variables to allow them
// to be overridden for testing purposes. Simulated scale testing
// requires being able to change the namespace of target resources
// (NamespaceForCluster and QualifiedNameForCluster) and ensure that
// the namespace of a federated resource will always be the kubefed
// system namespace (NamespaceForResource).

func namespaceForCluster(clusterName, namespace string) string {
	return namespace
}

// NamespaceForCluster returns the namespace to use for the given cluster.
var NamespaceForCluster = namespaceForCluster

func namespaceForResource(resourceNamespace, fedNamespace string) string {
	return resourceNamespace
}

// NamespaceForResource returns either the kubefed namespace or
// resource namespace.
var NamespaceForResource = namespaceForResource

func qualifiedNameForCluster(clusterName string, qualifiedName common.QualifiedName) common.QualifiedName {
	return qualifiedName
}

// QualifiedNameForCluster returns the qualified name to use for the
// given cluster.
var QualifiedNameForCluster = qualifiedNameForCluster

// GenerateFederatedObjectName generates a federated object name from source object name and ftc name.
func GenerateFederatedObjectName(objectName, ftcName string) string {
	transformedName, transformed := transformObjectName(objectName)
	federatedName := fmt.Sprintf("%s-%s", transformedName, ftcName)
	if transformed {
		federatedName = fmt.Sprintf("%s-%d", federatedName, fnvHashFunc(objectName))
	}

	if len(federatedName) > common.MaxFederatedObjectNameLength {
		nameHash := fmt.Sprint(fnvHashFunc(federatedName))
		federatedName = fmt.Sprintf("%s-%s", federatedName[:common.MaxFederatedObjectNameLength-len(nameHash)-1], nameHash)
	}

	return federatedName
}

// transformObjectName will transform the object name as follows:
// - upper case letters are transformed into lower case letters
// - characters that cannot appear in a DNS subdomain as defined in RFC 1123 are replaced with dots
// For more information about the DNS subdomain name, please refer to
// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names.
func transformObjectName(objectName string) (string, bool) {
	transformed := false
	transformedName := []byte(objectName)

	const caseDiff byte = 'a' - 'A'

	for i, ch := range transformedName {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-' {
			continue
		}

		transformed = true
		if ch >= 'A' && ch <= 'Z' {
			// transform uppercase letters into lowercase
			transformedName[i] = caseDiff + ch
		} else {
			// transform any other illegal characters to dots
			transformedName[i] = '.'
		}
	}

	return string(transformedName), transformed
}

func fnvHashFunc(key string) uint32 {
	hash := fnv.New32()
	hash.Write([]byte(key))
	return hash.Sum32()
}
