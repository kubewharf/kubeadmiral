/*
Copyright 2016 The Kubernetes Authors.

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

package clustername

type Name struct{ name string }

var Host = Name{name: ""}

func Member(name string) Name {
	if name == "" {
		panic("member cluster name must be nonempty")
	}

	return Name{name: name}
}

func (name Name) IsHost() bool {
	return name.name == ""
}

func (name Name) MetricValue() string {
	if name.IsHost() {
		return "host"
	}

	return name.name
}
