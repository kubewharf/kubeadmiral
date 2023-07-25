# controller-gen does not respect {} as default value for a struct field
# issue: https://github.com/kubernetes-sigs/controller-tools/issues/622
yq eval -i '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.replicaRescheduling.default = {}' "$1"
yq eval -i '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.reschedulePolicy.properties.replicaRescheduling.default = {}' "$1"

# policies are always referenced from labels, the value of which has limited length
yq eval -i '.spec.versions[].schema.openAPIV3Schema.properties.metadata |=
  {"type":"object","properties":{"name":{"type":"string","maxLength":63}}}' "$1"
