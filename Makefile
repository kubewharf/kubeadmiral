.PHONY: all generate manager test lint kind e2e

all: manager

# Generate code
generate: pkg/client
pkg/client: $(shell find pkg/apis)
	bash ./hack/generate-groups.sh
	touch pkg/client

# Build manager binary
manager:
	mkdir -p output
	go build -o output/manager cmd/controller-manager/main.go

# Run tests
test:
	go test -race -coverprofile coverage.out ./pkg/...

# Run golangci-lint
lint:
	golangci-lint run

# Creates federation using kind
kind: generate
	bash ./hack/dev-up.sh

e2e:
	ginkgo run -tags=e2e $(EXTRA_GINKGO_FLAGS) test/e2e -- --kubeconfig=$(KUBECONFIG) $(EXTRA_E2E_FLAGS)
