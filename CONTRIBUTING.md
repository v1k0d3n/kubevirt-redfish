# Contributing to KubeVirt Redfish

Thank you for your interest in contributing to KubeVirt Redfish! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

- [Overview](#overview)
- [Getting Started](#getting-started)
- [Development Workflow](#development-workflow)
- [Testing & Quality](#testing--quality)
- [Submitting Changes](#submitting-changes)
- [Becoming a Maintainer](#becoming-a-maintainer)
- [Makefile Reference](#makefile-reference)
- [Troubleshooting](#troubleshooting)

## Overview

KubeVirt Redfish is a Redfish-compatible API server for KubeVirt/OpenShift Virtualization that enables management of virtual machines through standard Redfish protocols. This can be extremely useful for advanced Redfish automation within a virtualized environment. For example, when using OpenShift's Advanced Cluster Manager, you can create simulated Redfish endpoints for end-to-end [Zero Touch Provisioning](https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/edge_computing/ztp-deploying-far-edge-sites) (ZTP) workflows. We welcome contributions from the community and are actively seeking additional maintainers to help grow the project.

## Getting Started

### Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.21+** - [Download from golang.org](https://golang.org/dl/)
- **Git** - [Download from git-scm.com](https://git-scm.com/)
- **Make** - Usually pre-installed on Unix-like systems
- **Container Runtime** - Docker or Podman
- **Kubernetes Cluster** - For integration testing (minikube, kind, or OpenShift)
- **Helm 3.12+** - [Download from helm.sh](https://helm.sh/)

### Setup

1. **Fork and Clone:**
   ```bash
   git clone https://github.com/YOUR_USERNAME/kubevirt-redfish.git
   cd kubevirt-redfish
   git remote add upstream https://github.com/v1k0d3n/kubevirt-redfish.git
   ```

2. **Quick Setup (Recommended):**
   ```bash
   make dev-setup          # Install dependencies and tools
   make create-config      # Create default configuration
   make build-local        # Build for local development
   ```

3. **Environment Variables (Optional):**
   ```bash
   # Container registry settings
   export REGISTRY="quay.io"
   export NAMESPACE="your-namespace"
   export IMAGE_NAME="kubevirt-redfish"
   
   # Registry authentication (for pushing images)
   export QUAY_USERNAME="your-username"
   export QUAY_PASSWORD="your-password"
   
   # Test configuration
   export TEST_HOST="localhost"
   export TEST_PORT="8443"
   export TEST_USER="testuser"
   export TEST_PASS="testpass"
   ```

## Development Workflow

### Project Structure

```
kubevirt-redfish/
├── cmd/                   # Application entry points
│   └── main.go            # Main application
├── config/                # Configuration files
│   └── config.yaml        # Default configuration
├── helm/                  # Helm chart
├── pkg/                   # Core packages
│   ├── auth/              # Authentication
│   ├── config/            # Configuration management
│   ├── errors/            # Error handling
│   ├── kubevirt/          # KubeVirt integration
│   ├── logger/            # Logging
│   ├── redfish/           # Redfish types
│   └── server/            # HTTP server
├── scripts/               # Utility scripts
└── Makefile               # Build and development tasks
```

### Configuration

Create a custom configuration for your development:

```bash
make create-config    # Create default configuration
vim config.yaml       # Edit configuration
```

Example configuration:
```yaml
server:
  host: "0.0.0.0"
  port: 8443
  tls:
    enabled: false

chassis:
  - name: "chassis-0"
    namespace: "namespace-0"
    service_account: "redfish-sa"
    description: "Test chassis"

authentication:
  users:
    - username: "admin"
      password: "admin123"
      chassis: ["chassis-0"]
```

### Development Process

1. **Create a feature branch:**
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make changes and validate:**
   ```bash
   make fmt lint validate    # Format, lint, and validate code
   make test                 # Run tests
   ```

3. **Test locally:**
   ```bash
   make build-local          # Build for local development
   make test-local           # Test locally
   ```

## Testing & Quality

### Testing

```bash
# Unit tests
make test                 # Run all unit tests
make test-coverage        # Run tests with coverage report
make test-race           # Run tests with race detection
make test-short          # Run short tests only

# Integration tests (requires K8s cluster with KubeVirt)
make test-integration    # Run integration tests

# Local testing
make test-local          # Build and test locally
make test-api            # Test HTTP API endpoint
make test-api-https      # Test HTTPS API endpoint
```

### Code Quality

```bash
# Code formatting and validation
make fmt                 # Format code
make lint                # Run golangci-lint
make validate            # Run all validation checks

# Coverage
make test-coverage       # Generate coverage report
open coverage.html       # View coverage in browser
```

### API Testing Examples

```bash
# Test Redfish API endpoints
curl -s -u admin:admin123 http://localhost:8443/redfish/v1/ | jq .
curl -s -k -u admin:admin123 https://localhost:8443/redfish/v1/ | jq .
```

## Submitting Changes

### Pull Request Process

**Create a Pull Request** on GitHub with:
- Clear, descriptive title
- Detailed description of changes
- Relevant tests included
- Documentation updates if needed

### Commit Message Format

We follow conventional commit format:

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

**Types:** `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

**Examples:**
```
feat(server): add support for Redfish v1.16.0
fix(auth): resolve authentication bypass issue
docs(readme): update installation instructions
```

## Becoming a Maintainer

We're actively seeking additional maintainers! Here's how to get involved:

### Current Maintainers

- **Primary Maintainer:** [@v1k0d3n](https://github.com/v1k0d3n)

### Maintainer Responsibilities

Maintainers are responsible for:

- **Code Review** - Reviewing pull requests
- **Release Management** - Cutting releases and managing versions
- **Issue Triage** - Managing issues and feature requests
- **Documentation** - Maintaining and improving documentation
- **Community** - Supporting community members

### How to Become a Maintainer

1. **Start Contributing** - Submit several quality pull requests
2. **Engage with Community** - Help with issues and discussions
3. **Demonstrate Expertise** - Show deep understanding of the codebase
4. **Express Interest** - Let us know you're interested in becoming a maintainer

## Makefile Reference

### Build Commands

```bash
# Local development
make build-local          # Build for local development
make build               # Build production binary
make build-amd64         # Build for AMD64 platform
make build-arm64         # Build for ARM64 platform

# Container images
make build-image         # Build container image
make push-image          # Push container image
make build-push          # Build and push image

# Helm charts
make build-chart         # Build Helm chart
make push-chart          # Push Helm chart
make build-push-chart    # Build and push chart
```

### Development Commands

```bash
# Setup and configuration
make dev-setup           # Setup development environment
make create-config       # Create default configuration
make check-runtime       # Check container runtime and platform

# Code quality
make fmt                 # Format code
make lint                # Run golangci-lint
make validate            # Run all validation checks
make tidy                # Tidy go modules

# Testing
make test                # Run unit tests
make test-coverage       # Run tests with coverage
make test-race          # Run tests with race detection
make test-short         # Run short tests only
make test-local         # Test locally
make test-integration   # Run integration tests
make test-api           # Test API locally
make test-api-https     # Test API with HTTPS

# Deployment
make deploy-openshift    # Deploy to OpenShift
make deploy-k8s          # Deploy to Kubernetes
make restart             # Restart deployment
make logs                # Get pod logs
make port-forward        # Port forward for testing

# Cleanup
make clean               # Clean build artifacts
make clean-all           # Clean all artifacts
```

### Environment Variables

```bash
# Override defaults
VERSION=v1.0.0 make build
IMAGE_REGISTRY=docker.io make build-image
DEPLOYMENT_NAMESPACE=my-namespace make deploy-k8s
```

## Troubleshooting

### Common Issues

#### Build Issues

```bash
# Clean and rebuild
make clean
make build-local

# Check Go version
go version

# Update dependencies
go mod tidy
```

#### Test Issues

```bash
# Run tests with verbose output
go test -v ./...

# Check test coverage
make test-coverage

# Run specific test
go test -v ./pkg/server -run TestFunctionName
```

#### Container Issues

```bash
# Check container runtime
make check-runtime

# Build with specific runtime
CONTAINER_RUNTIME=docker make build-image
```

#### Integration Test Issues

```bash
# Check cluster access
kubectl cluster-info

# Verify KubeVirt installation
kubectl get pods -n kubevirt

# Check test configuration
./scripts/test-integration.sh --help
```

### Getting Help

- **GitHub Issues** - [Create an issue](https://github.com/v1k0d3n/kubevirt-redfish/issues)
- **Discussions** - [GitHub Discussions](https://github.com/v1k0d3n/kubevirt-redfish/discussions)
- **Documentation** - Check the [README.md](README.md) and [docs/](docs/) directory

### Development Tips

- **Use the Makefile** - Most common tasks are available as make targets
- **Follow Go conventions** - Use `go fmt`, `go vet`, and `golangci-lint`
- **Test locally** - Always test changes locally before submitting
- **Update documentation** - Keep docs in sync with code changes

---

Thank you for contributing to KubeVirt Redfish! Your contributions help make this project better for everyone in the community. 