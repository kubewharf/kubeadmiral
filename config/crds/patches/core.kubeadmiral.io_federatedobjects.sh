#!/usr/bin/env bash

yq eval -i '.metadata.name = "federatedobjects.core.kubeadmiral.io"' "$1"

yq eval -i '.spec.names.kind = "FederatedObject"' "$1"
yq eval -i '.spec.names.listKind = "FederatedObjectList"' "$1"
yq eval -i '.spec.names.plural = "federatedobjects"' "$1"
yq eval -i '.spec.names.singular = "federatedobject"' "$1"
yq eval -i '.spec.names.shortNames = ["fo"]' "$1"

yq eval -i '.spec.scope = "Namespaced"' "$1"
