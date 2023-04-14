yq eval -i '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.replicaRescheduling.default = {}' "$1"
yq eval -i '.spec.versions[].schema.openAPIV3Schema.properties.metadata |=
  {"type":"object","properties":{"name":{"type":"string","maxLength":63}}}' "$1"
