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

package naming

import (
	"fmt"
	"hash/fnv"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const (
	endpointSlicePrefix  = "imported"
	derivedServicePrefix = "derived"
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

// GenerateImportedEndpointSliceName generates an imported endpointSlice name from endpointSlice name in member clusters.
func GenerateImportedEndpointSliceName(endpointSliceName string, cluster string) string {
	importedEpsName := fmt.Sprintf("%s-%s-%s", endpointSlicePrefix, cluster, endpointSliceName)

	if len(importedEpsName) > common.MaxEndpointSliceNameLength {
		nameHash := fmt.Sprint(fnvHashFunc(importedEpsName))
		importedEpsName = fmt.Sprintf("%s-%s", importedEpsName[:common.MaxEndpointSliceNameLength-len(nameHash)-1], nameHash)
	}

	return importedEpsName
}

// GenerateDerivedSvcFedObjName generates a federated object name whose template is a derived service.
func GenerateDerivedSvcFedObjName(serviceName string) string {
	derivedSvcFedObjName := fmt.Sprintf("%s-%s-%s", derivedServicePrefix, serviceName, "services")

	if len(derivedSvcFedObjName) > common.MaxFederatedObjectNameLength {
		nameHash := fmt.Sprint(fnvHashFunc(derivedSvcFedObjName))
		derivedSvcFedObjName = fmt.Sprintf("%s-%s", derivedSvcFedObjName[:common.MaxFederatedObjectNameLength-len(nameHash)-1], nameHash)
	}

	return derivedSvcFedObjName
}

// GenerateSourceClusterValue generates the value of source cluster label.
func GenerateSourceClusterValue(clusterName string) string {
	if len(clusterName) > common.MaxLabelValueLength {
		nameHash := fmt.Sprint(fnvHashFunc(clusterName))
		clusterName = fmt.Sprintf("%s-%s", clusterName[:common.MaxLabelValueLength-len(nameHash)-1], nameHash)
	}

	return clusterName
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

	// squash any sequence of more than one '.'
	sanitizedName := []byte{}
	for i, ch := range transformedName {
		if i != 0 && transformedName[i-1] == '.' && transformedName[i] == '.' {
			transformed = true
			continue
		}
		sanitizedName = append(sanitizedName, ch)
	}

	return string(sanitizedName), transformed
}

func fnvHashFunc(key string) uint32 {
	hash := fnv.New32()
	hash.Write([]byte(key))
	return hash.Sum32()
}
