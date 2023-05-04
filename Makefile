SHELL := /bin/bash

BUILD_FLAGS :=
BINARY := manager

.PHONY: all
all: build

# Production build
.PHONY: build
build:
	mkdir -p output
	go build $(BUILD_FLAGS) -o output/$(BINARY) cmd/controller-manager/main.go

# Debug build
.PHONY: debug
debug: BUILD_FLAGS+=-race
debug: BINARY:=$(BINARY)_debug
debug: build

# Clean built binaries
.PHONY: clean
clean:
	rm -r output

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
