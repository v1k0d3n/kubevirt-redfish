# Variables
IMAGE_REGISTRY ?= quay.io
IMAGE_NAMESPACE ?= bjozsa-redhat
IMAGE_NAME ?= kubevirt-redfish
IMAGE_TAG ?= latest
FULL_IMAGE_NAME = $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(IMAGE_TAG)

# Helm chart variables
HELM_CHART_NAME ?= kubevirt-redfish
HELM_CHART_VERSION ?= $(VERSION)
HELM_REGISTRY ?= $(IMAGE_REGISTRY)
HELM_NAMESPACE ?= $(IMAGE_NAMESPACE)
HELM_REPOSITORY ?= charts
HELM_FULL_REPO = oci://$(HELM_REGISTRY)/$(HELM_NAMESPACE)/$(HELM_REPOSITORY)

# Registry authentication variables (from environment)
QUAY_USERNAME ?= $(shell echo $$QUAY_USERNAME)
QUAY_PASSWORD ?= $(shell echo $$QUAY_PASSWORD)

# Version variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.2.0")
GIT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags with version information
LDFLAGS := -w -s \
	-X main.Version=$(VERSION) \
	-X main.GitCommit=$(GIT_COMMIT) \
	-X main.BuildDate=$(BUILD_DATE)

# Deployment variables (can be overridden via environment variables)
DEPLOYMENT_NAME ?= kubevirt-redfish
DEPLOYMENT_NAMESPACE ?= kubevirt-redfish

# Detect container runtime (prefer podman, fallback to docker)
CONTAINER_RUNTIME := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

# Detect if we're on Apple Silicon
IS_APPLE_SILICON := $(shell uname -m | grep -q arm64 && echo "true" || echo "false")

# Go build configuration
CGO_ENABLED := 0
GOOS := linux
GOARCH := amd64

# Build targets
.PHONY: build
build:
	@echo "Building kubevirt-redfish binary..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="$(LDFLAGS)" -o kubevirt-redfish ./cmd/main.go

# Build for local development
.PHONY: build-local
build-local:
	@echo "Building kubevirt-redfish binary for local development..."
	go build -o kubevirt-redfish ./cmd/main.go

# Build for specific platform
.PHONY: build-amd64
build-amd64:
	@echo "Building for AMD64 platform..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o kubevirt-redfish-amd64 ./cmd/main.go

# Build for ARM64
.PHONY: build-arm64
build-arm64:
	@echo "Building for ARM64 platform..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o kubevirt-redfish-arm64 ./cmd/main.go

# Build container image
.PHONY: build-image
build-image:
	@echo "Building container image: $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(VERSION)"
ifeq ($(CONTAINER_RUNTIME),)
	$(error "Neither podman nor docker found. Please install one of them.")
endif
	@echo "Using container runtime: $(CONTAINER_RUNTIME)"
ifeq ($(IS_APPLE_SILICON),true)
	@echo "Detected Apple Silicon - building multi-arch image for x86_64"
	$(CONTAINER_RUNTIME) buildx build --platform linux/amd64 -t $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(VERSION) .
else
	$(CONTAINER_RUNTIME) build -t $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(VERSION) .
endif

# Push container image
.PHONY: push-image
push-image:
	@echo "Pushing container image: $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(VERSION)"
ifeq ($(CONTAINER_RUNTIME),)
	$(error "Neither podman nor docker found. Please install one of them.")
endif
	$(CONTAINER_RUNTIME) push $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(VERSION)

# Build and push image
.PHONY: build-push
build-push: build-image push-image

# Helm chart targets
.PHONY: update-chart-version
update-chart-version:
	@echo "Updating Chart.yaml version to: $(VERSION)"
	@cd helm && VERSION_NO_V=$$(echo $(VERSION) | sed 's/^v//') && \
	if echo "$(VERSION)" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' >/dev/null 2>&1; then \
		echo "Release tag detected, using clean version: $$VERSION_NO_V"; \
		sed -i.bak "s/^version: .*/version: $$VERSION_NO_V/" Chart.yaml; \
	else \
		echo "Commit detected, using prefixed version: 0.0.0-$$VERSION_NO_V"; \
		sed -i.bak "s/^version: .*/version: 0.0.0-$$VERSION_NO_V/" Chart.yaml; \
	fi
	@cd helm && sed -i.bak 's/^appVersion: .*/appVersion: "$(VERSION)"/' Chart.yaml
	@echo "Chart.yaml updated"
	@echo "Debug: Chart.yaml version after update:"
	@cd helm && grep "^version:" Chart.yaml

.PHONY: build-chart
build-chart: update-chart-version
	@echo "Building Helm chart..."
	@echo "Chart version: $(VERSION)"
	cd helm && helm package . --destination .. && cd ..
	@VERSION_NO_V=$$(echo $(VERSION) | sed 's/^v//') && \
	if echo "$(VERSION)" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' >/dev/null 2>&1; then \
		echo "Helm chart built: $(HELM_CHART_NAME)-$$VERSION_NO_V.tgz"; \
	else \
		echo "Helm chart built: $(HELM_CHART_NAME)-0.0.0-$$VERSION_NO_V.tgz"; \
	fi

.PHONY: push-chart
push-chart:
	@echo "Pushing Helm chart to: $(HELM_FULL_REPO)"
	@echo "Authenticating with registry..."
	helm registry login $(HELM_REGISTRY) -u "$(QUAY_USERNAME)" -p "$(QUAY_PASSWORD)"
	@VERSION_NO_V=$$(echo $(VERSION) | sed 's/^v//') && \
	if echo "$(VERSION)" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' >/dev/null 2>&1; then \
		helm push $(HELM_CHART_NAME)-$$VERSION_NO_V.tgz $(HELM_FULL_REPO); \
	else \
		helm push $(HELM_CHART_NAME)-0.0.0-$$VERSION_NO_V.tgz $(HELM_FULL_REPO); \
	fi
	@echo "Helm chart pushed successfully"

.PHONY: build-push-chart
build-push-chart: build-chart push-chart

# Test targets
.PHONY: test
test:
	@echo "Running unit tests..."
	go test -v ./...

.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: test-race
test-race:
	@echo "Running tests with race detection..."
	go test -race -v ./...

.PHONY: test-short
test-short:
	@echo "Running short tests..."
	go test -v -short ./...

# Code quality targets
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...

.PHONY: vet
vet:
	@echo "Running go vet..."
	go vet ./...

.PHONY: lint
lint:
	@echo "Running golangci-lint..."
	golangci-lint run

.PHONY: tidy
tidy:
	@echo "Tidying go modules..."
	go mod tidy
	go mod verify

# Validation targets
.PHONY: validate
validate: fmt vet tidy
	@echo "Code validation completed"

# Development targets
.PHONY: dev-setup
dev-setup:
	@echo "Setting up development environment..."
	@echo "Installing dependencies..."
	go mod download
	@echo "Installing golangci-lint..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "Development environment setup complete"

# Configuration targets
.PHONY: create-config
create-config:
	@echo "Creating default configuration..."
	./kubevirt-redfish --create-config config.yaml

# Local testing targets
.PHONY: test-local
test-local: build-local
	@echo "Starting kubevirt-redfish locally for testing..."
	./kubevirt-redfish --config config.yaml

.PHONY: test-local-standalone
test-local-standalone: build-local
	@echo "Starting kubevirt-redfish in standalone mode..."
	./kubevirt-redfish --config config-standalone.yaml

# Integration testing
.PHONY: test-integration
test-integration: build
	@echo "Running integration tests..."
	@echo "Note: This requires a running Kubernetes cluster with KubeVirt"
	./scripts/test-integration.sh

# OpenShift deployment targets
.PHONY: deploy-openshift
deploy-openshift:
	@echo "Deploying kubevirt-redfish to OpenShift..."
	oc apply -f deploy/rbac.yaml
	oc apply -f deploy/deployment.yaml
	oc apply -f deploy/route.yaml

.PHONY: undeploy-openshift
undeploy-openshift:
	@echo "Undeploying kubevirt-redfish from OpenShift..."
	oc delete -f deploy/route.yaml --ignore-not-found
	oc delete -f deploy/deployment.yaml --ignore-not-found
	oc delete -f deploy/rbac.yaml --ignore-not-found

# Kubernetes deployment targets
.PHONY: deploy-k8s
deploy-k8s:
	@echo "Deploying kubevirt-redfish to Kubernetes..."
	kubectl apply -f deploy/rbac.yaml
	kubectl apply -f deploy/deployment.yaml
	kubectl apply -f deploy/service.yaml

.PHONY: undeploy-k8s
undeploy-k8s:
	@echo "Undeploying kubevirt-redfish from Kubernetes..."
	kubectl delete -f deploy/service.yaml --ignore-not-found
	kubectl delete -f deploy/deployment.yaml --ignore-not-found
	kubectl delete -f deploy/rbac.yaml --ignore-not-found

# Restart deployment
.PHONY: restart
restart:
	@echo "Restarting deployment $(DEPLOYMENT_NAME) in namespace $(DEPLOYMENT_NAMESPACE)..."
	kubectl rollout restart deployment/$(DEPLOYMENT_NAME) -n $(DEPLOYMENT_NAMESPACE)
	@echo "Deployment restart initiated. Use 'kubectl get pods -n $(DEPLOYMENT_NAMESPACE)' to monitor progress."

# Logging targets
.PHONY: logs
logs:
	@echo "Getting pod logs..."
	kubectl logs -f deployment/kubevirt-redfish -n default

.PHONY: logs-openshift
logs-openshift:
	@echo "Getting pod logs from OpenShift..."
	oc logs -f deployment/kubevirt-redfish -n default

# Port forwarding for local testing
.PHONY: port-forward
port-forward:
	@echo "Setting up port forwarding..."
	kubectl port-forward deployment/kubevirt-redfish 8443:8443 -n default

.PHONY: port-forward-openshift
port-forward-openshift:
	@echo "Setting up port forwarding from OpenShift..."
	oc port-forward deployment/kubevirt-redfish 8443:8443 -n default

# API testing
.PHONY: test-api
test-api:
	@echo "Testing kubevirt-redfish API..."
	curl -s -u admin:admin123 http://localhost:8443/redfish/v1/ | jq .

.PHONY: test-api-https
test-api-https:
	@echo "Testing kubevirt-redfish API with HTTPS..."
	curl -s -k -u admin:admin123 https://localhost:8443/redfish/v1/ | jq .

# Clean up targets
.PHONY: clean
clean:
	@echo "Cleaning up build artifacts..."
	rm -f kubevirt-redfish
	rm -f kubevirt-redfish-amd64
	rm -f kubevirt-redfish-arm64
	rm -f coverage.out
	rm -f coverage.html
	rm -f $(HELM_CHART_NAME)-*.tgz
	rm -f helm/Chart.yaml.bak

.PHONY: clean-all
clean-all: clean
	@echo "Cleaning up all artifacts..."
	rm -rf vendor/
	go clean -cache -modcache -testcache

# Version information
.PHONY: version
version:
	@echo "KubeVirt Redfish API Server"
	@echo "Version: $(shell git describe --tags --always --dirty 2>/dev/null || echo "unknown")"
	@echo "Git Commit: $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")"
	@echo "Build Date: $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")"

# Check container runtime
.PHONY: check-runtime
check-runtime:
	@echo "Container runtime: $(CONTAINER_RUNTIME)"
	@echo "Apple Silicon detected: $(IS_APPLE_SILICON)"
	@echo "Full image name: $(FULL_IMAGE_NAME)"
	@echo "Go version: $(shell go version)"
	@echo "Git version: $(shell git --version 2>/dev/null || echo "git not found")"

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build              - Build the Go binary for production"
	@echo "  build VERSION=v1.0.0 - Build binary for specific version"
	@echo "  build-local        - Build the Go binary for local development"
	@echo "  build-amd64        - Build binary for AMD64 platform"
	@echo "  build-arm64        - Build binary for ARM64 platform"
	@echo "  build-image        - Build the container image"
	@echo "  push-image         - Push the container image to registry"
	@echo "  build-push         - Build and push the container image"
	@echo "  build-push VERSION=v1.0.0 - Build and push specific version"
	@echo "  build-chart        - Build the Helm chart"
	@echo "  push-chart         - Push the Helm chart to registry"
	@echo "  build-push-chart   - Build and push the Helm chart"
	@echo "  build-push-chart VERSION=v1.0.0 - Build and push specific chart version"
	@echo ""
	@echo "Testing:"
	@echo "  test               - Run unit tests"
	@echo "  test-coverage      - Run tests with coverage report"
	@echo "  test-race          - Run tests with race detection"
	@echo "  test-short         - Run short tests only"
	@echo "  test-local         - Test locally with built binary"
	@echo "  test-integration   - Run integration tests"
	@echo "  test-api           - Test the API locally"
	@echo ""
	@echo "Code Quality:"
	@echo "  fmt                - Format code"
	@echo "  vet                - Run go vet"
	@echo "  lint               - Run golangci-lint"
	@echo "  tidy               - Tidy go modules"
	@echo "  validate           - Run all validation checks"
	@echo ""
	@echo "Development:"
	@echo "  dev-setup          - Setup development environment"
	@echo "  create-config      - Create default configuration"
	@echo "  version            - Show version information"
	@echo "  check-runtime      - Check container runtime and platform"
	@echo ""
	@echo "Deployment:"
	@echo "  deploy-openshift   - Deploy to OpenShift"
	@echo "  undeploy-openshift - Remove from OpenShift"
	@echo "  deploy-k8s         - Deploy to Kubernetes"
	@echo "  undeploy-k8s       - Remove from Kubernetes"
	@echo "  logs               - Get pod logs"
	@echo "  port-forward       - Port forward for local testing"
	@echo ""
	@echo "Cleanup:"
	@echo "  clean              - Clean up build artifacts"
	@echo "  clean-all          - Clean up all artifacts"
	@echo ""
	@echo "Help:"
	@echo "  help               - Show this help" 