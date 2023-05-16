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

package federatedtypeconfig

import (
	"bytes"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const fedObjectSchemaYaml = `
openAPIV3Schema:
	type: object
	properties:
		apiVersion:
			type: string
		kind:
			type: string
		metadata:
			type: object
		spec:
			type: object
			x-kubernetes-preserve-unknown-fields: true
		status:
			type: object
			properties:
				clusters:
					type: array
					items:
						type: object
						properties:
							generation:
								type: integer
							name:
								type: string
							status:
								type: string
						required: [name]
				conditions:
					type: array
					items:
						type: object
						properties:
							lastTransitionTime:
								type: string
								format: date-time
							lastUpdateTime:
								type: string
								format: date-time
							reason:
								type: string
							status:
								type: string
							type:
								type: string
						required: [type, status]
	required: [spec]
	x-kubernetes-preserve-unknown-fields: true
`

const statusObjectSchemaYaml = `
openAPIV3Schema:
	type: object
	x-kubernetes-preserve-unknown-fields: true
`

var fedObjectSchema, statusObjectSchema apiextensionsv1.CustomResourceValidation

func init() {
	if err := yaml.NewYAMLOrJSONDecoder(
		bytes.NewReader([]byte(strings.Replace(fedObjectSchemaYaml, "\t", "  ", -1))),
		len(fedObjectSchemaYaml),
	).Decode(&fedObjectSchema); err != nil {
		panic(err)
	}

	if err := yaml.NewYAMLOrJSONDecoder(
		bytes.NewReader([]byte(strings.Replace(statusObjectSchemaYaml, "\t", "  ", -1))),
		len(statusObjectSchemaYaml),
	).Decode(&statusObjectSchema); err != nil {
		panic(err)
	}
}
