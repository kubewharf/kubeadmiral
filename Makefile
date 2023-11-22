SHELL := /bin/bash

# go build arguments
BUILD_FLAGS ?=
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GOPROXY ?= $(shell go env GOPROXY)
TARGET_NAME ?= all
DEBUG_TARGET_NAME ?= $(TARGET_NAME)_debug

# image information
REGISTRY ?= ghcr.io/kubewharf
TAG ?= latest
REGION ?=

ifeq (${REGION}, cn)
GOPROXY := https://goproxy.cn,direct
endif

.PHONY: all
all: build

# Production build
#
# Args:
#   BUILD_PLATFORMS: platforms to build. e.g.: linux/amd64,darwin/amd64. The default value is the host platform.
#
# Example:
#   # compile all binaries with the host platform
#   make build
#   # compile all binaries with specified platforms
#   make build BUILD_PLATFORMS=linux/amd64,darwin/amd64
#   # compile kubeadmiral-controller-manager with the host platform
#   make build TARGET_NAME=kubeadmiral-controller-manager
#   # compile kubeadmiral-controller-manager with specified platforms
#   make build TARGET_NAME=kubeadmiral-controller-manager BUILD_PLATFORMS=linux/amd64,darwin/amd64
.PHONY: build
build:
	BUILD_FLAGS="$(BUILD_FLAGS)" TARGET_NAME="$(TARGET_NAME)" GOPROXY="$(GOPROXY)" bash hack/make-rules/build.sh

# Debug build
.PHONY: build-debug
build-debug: BUILD_FLAGS+=-race
build-debug: TARGET_NAME:=$(DEBUG_TARGET_NAME)
build-debug: build

# Start a local kubeadmiral cluster for developers.
#
# It will directly start the kubeadmiral control-plane cluster(excluding the kubeadmiral-controller-manager) and three member-clusters.
# Users can run the kubeadmiral-controller-manager component through binary for easy debugging using make dev-run.
.PHONY: dev-up
dev-up:
	bash hack/make-rules/dev-up.sh
	make build-debug

# Clean up the clusters created by dev-up.
.PHONY: dev-clean
dev-clean:
	bash hack/make-rules/dev-clean.sh

# Run the kubeadmiral-controller-manager component with sane defaults for development.
.PHONY: dev-run
dev-run:
	./output/bin/$(GOOS)/$(GOARCH)/$(DEBUG_TARGET_NAME) \
		--enable-leader-elect=false \
		--worker-count=5 \
		--kubeconfig=${HOME}/.kube/kubeadmiral/kubeadmiral-host.yaml \
		--klog-v=4 \
		--klog-add-dir-header=true \
		--klog-logtostderr=false \
		--klog-log-file=/dev/stdout \
		--cluster-join-timeout=15s 2>&1

# Local up the KubeAdmiral.
#
# Args:
#   NUM_MEMBER_CLUSTERS: the number of member clusters you want to startup, default is 3, at least 1.
#   REGION:              region to build. e.g.: 'cn' means China mainland,
#                        the script will set the remote mirror address according to the region to speed up the build.
#
# Example:
#   # It will locally start a meta-cluster to deploy the kubeadmiral control-plane, and three member cluster clusters
#   make local-up
.PHONY: local-up
local-up:
	make clean-local-cluster
	REGION="$(REGION)" NUM_MEMBER_CLUSTERS="$(NUM_MEMBER_CLUSTERS)" bash hack/make-rules/local-up.sh

# Build binaries and docker images.
# The supported OS is linux, and user can specify the arch type (only amd64,arm64,arm are supported)
#
# ARGS:
#   REGISTRY:  image registry, the default value is "ghcr.io/kubewharf"
#   TAG:       image tag, the default value is "latest"
#   ARCHS:     list of target architectures, e.g.:amd64,arm64,arm. The default value is host arch
#   GOPROXY:   it specifies the download address of the dependent package
#   REGION:    region to build. e.g.: 'cn' means china mainland(the default value),
#              the script will set the remote mirror address according to the region to speed up the build.
#   DOCKER_BUILD_ARGS: additional parameters of the Dockerfile need to be passed in during the image building process,
#                      e.g.: `--build-arg BUILD_FLAGS=-race`
#   DOCKERFILE_PATH:   the Dockerfile path used by docker build, default is `kubeadmiral/hack/dockerfiles/Dockerfile`
#
# Examples:
#   # build images with the host arch, it will generate "ghcr.io/kubewharf/kubeadmiral-controller-manager:latest"
#   make images
#
#   # build images with amd64,arm64 arch, and use the mirror source in mainland China during the image building process
#   # note: If you specify multiple architectures, the image tag will be added with the architecture name,e.g.:
#   # 		"ghcr.io/kubewharf/kubeadmiral-controller-manager:latest-amd64"
#   # 		"ghcr.io/kubewharf/kubeadmiral-controller-manager:latest-arm64"
#   make images ARCHS=amd64,arm64 REGION="cn"
#
#   # enable race detection in go build
#   make images DOCKER_BUILD_ARGS="--build-arg BUILD_FLAGS=-race"
.PHONY: images
images:
	REGISTRY=$(REGISTRY) TAG=$(TAG) ARCHS=$(ARCHS) GOPROXY=$(GOPROXY) REGION=$(REGION) \
		DOCKER_BUILD_ARGS="$(DOCKER_BUILD_ARGS)" DOCKERFILE_PATH="$(DOCKERFILE_PATH)" \
		TARGET_NAME="$(TARGET_NAME)" bash hack/make-rules/build-images.sh

# Clean built binaries
.PHONY: clean
clean:
	rm -rf output kubeadmiral.log

# Clean up all the local kind clusters and related kubeconfigs
# Users can clean up the local kind clusters started by the `make local-up` command.
.PHONY: clean-local-cluster
clean-local-cluster:
	bash hack/make-rules/clean-local-cluster.sh

# Generate code and artifacts
.PHONY: generate
generate: pkg/client
pkg/client: $(shell find pkg/apis)
	bash ./hack/generate-groups.sh
# update the directory mtime to avoid unnecessary re-runs
	touch pkg/client

LINT_FLAGS := --timeout=5m --max-issues-per-linter=0 --max-same-issues=0 --uniq-by-line=false

# Run golangci-lint
.PHONY: lint
lint:
	golangci-lint run $(LINT_FLAGS)

# Run golangci-lint on files changed from the default branch
.PHONY: lint-new
lint-new: LINT_FLAGS += --new-from-rev=main --whole-files
lint-new: lint

# Run tests
.PHONY: test
test:
	go test -race -coverprofile coverage0.out ./pkg/...
# exclude generated files from coverage calculation
	sed '/generated/d' coverage0.out > coverage.out
	rm coverage0.out

# Run e2e tests
.PHONY: e2e
e2e:
	ginkgo run -race -tags=e2e $(EXTRA_GINKGO_FLAGS) test/e2e -- --kubeconfig=$(KUBECONFIG) $(EXTRA_E2E_FLAGS)
