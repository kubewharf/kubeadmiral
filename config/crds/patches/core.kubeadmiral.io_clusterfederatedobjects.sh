#!/usr/bin/env bash

yq eval -i '.metadata.name = "clusterfederatedobjects.core.kubeadmiral.io"' "$1"

yq eval -i '.spec.names.kind = "ClusterFederatedObject"' "$1"
yq eval -i '.spec.names.listKind = "ClusterFederatedObjectList"' "$1"
yq eval -i '.spec.names.plural = "clusterfederatedobjects"' "$1"
yq eval -i '.spec.names.singular = "clusterfederatedobject"' "$1"
yq eval -i '.spec.names.shortNames = ["cfo"]' "$1"

yq eval -i '.spec.scope = "Cluster"' "$1"
