# KubeVirt Redfish

[![CI/CD](https://github.com/v1k0d3n/kubevirt-redfish/actions/workflows/ci.yml/badge.svg)](https://github.com/v1k0d3n/kubevirt-redfish/actions)
[![Go](https://img.shields.io/badge/go-1.21+-blue.svg)](https://golang.org/dl/)
[![Coverage](https://codecov.io/gh/v1k0d3n/kubevirt-redfish/branch/main/graph/badge.svg)](https://codecov.io/gh/v1k0d3n/kubevirt-redfish)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Container](https://img.shields.io/badge/container-quay.io-red.svg)](https://quay.io/repository/bjozsa-redhat/kubevirt-redfish)
[![Helm](https://img.shields.io/badge/helm-oci-blue.svg)](https://quay.io/repository/bjozsa-redhat/charts/kubevirt-redfish)
[![Platform](https://img.shields.io/badge/platform-OpenShift-red.svg)](https://docs.openshift.com/container-platform/latest/virt/about-virt.html)

A Redfish-compatible API server for KubeVirt/OpenShift Virtualization that enables out-of-band management of virtual machines through standard Redfish protocols.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Architecture](#architecture)
- [Installation](#installation)
  - [Prerequisites](#prerequisites)
  - [Quick Start (Helm)](#installation-quickstart-via-helm)
  - [Verification](#verification)
- [Configuration](#configuration)
- [Usage](#usage)
- [Contributing](#contributing)
- [License](#license)

## Overview

KubeVirt Redfish bridges the gap between traditional virtualization management tools and cloud-native KubeVirt environments by providing a Redfish-compliant API interface. This enables existing datacenter management tools, IPMI utilities, and automation scripts to manage KubeVirt virtual machines using familiar Redfish protocols.

## Features

- **Redfish API Compatibility** - Standard Redfish protocol support for VM management
- **KubeVirt Integration** - Native integration with KubeVirt/OpenShift Virtualization
- **Authentication & Authorization** - Kubernetes RBAC integration
- **Multi-Architecture Support** - AMD64 and ARM64 container images
- **Cloud-Native** - Kubernetes-native deployment and configuration
- **Observability** - Comprehensive logging and metrics
- **High Availability** - Scalable and resilient architecture

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Redfish       │    │   KubeVirt       │    │   KubeVirt      │
│   Clients       │───▶│   Redfish        │───▶│   VMs           │
│   (ACM, etc.)   │    │   API Server     │    │   (OCP/k8s)     │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

## Installation

### Prerequisites

- Kubernetes cluster with KubeVirt or OpenShift with OpenShift Virtualization
- Helm 3.12+ 
- `kubectl` or `oc` CLI access

## Installation QuickStart (via Helm)

The `kubevirt-redfish` project uses [Quay.io](https://quay.io/) to store both the [container](https://quay.io/repository/bjozsa-redhat/kubevirt-redfish?tab=tags) and [Helm](https://quay.io/repository/bjozsa-redhat/charts/kubevirt-redfish?tab=tags) chart artifacts. To install `kubevirt-redfish`, please review the instructions below.

1. You can review the sample `values.yaml` included with the Helm chart with the following command.
   ```bash
   helm show values oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish --version 0.2.1
   ```

   NOTE: If you prefer to have a cleaned version of the `values.yaml`, you can run the following to remove any comments from the `values.yaml` output:
   ```bash
   helm show values oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish --version 0.2.1 | sed 's/\s*#.*$//' | grep -v '^\s*$'
   ```

2. To save the `values.yaml` to your your local environment for direct editing, just redirect the output to a YAML file.
   ```bash
   helm show values oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish --version 0.2.1 > my-values.yaml
   ```

3. You can also save the chart locally by issuing the following command.
   ```bash
   # Download the Chart locally:
   helm pull oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish --version 0.2.1
   
   # Explode the tar file:
   tar -xzf kubevirt-redfish-0.2.1.tgz

   # Make a copy of the sample values.yaml manifest:
   cp kubevirt-redfish/values.yaml my-values.yaml
   ```

4. Now you can install the Chart using your own custom values.yaml:
   ```bash
   helm install kubevirt-redfish oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish \
     --namespace kubevirt-redfish \
     --create-namespace \
     -f my-values.yaml
   ```

5. You can also ***optionally*** use inline edits during the `helm install` command.
   ```bash
   helm install kubevirt-redfish oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish \
     --set image.tag=v0.2.1 \
     --set service.type=LoadBalancer \
     --namespace kubevirt-redfish \
     --create-namespace
   ```

### Verification

You can verify the installation with the following commands.
```bash
oc get pods -n kubevirt-redfish
helm list -n kubevirt-redfish
```

## Configuration

Key configuration options available in `values.yaml`:

```yaml
# =============================================================================
# ROUTE CONFIGURATION (OpenShift)
# =============================================================================

route:
  enabled: true
  host: "kubevirt-redfish-namespace-0.clustername.apps.example.com"
  tls:
    termination: "edge"
    insecureEdgeTerminationPolicy: "Redirect"

# =============================================================================
# CHASSIS CONFIGURATION
# =============================================================================

chassis:
  - name: "chassis-0"
    namespace: "namespace-0"
    service_account: "kubevirt-redfish"
    description: "My KVM cluster with test VMs"
  - name: "chassis-1"
    namespace: "namespace-1"
    service_account: "kubevirt-redfish"
    description: "My KVM cluster with test VMs"

# =============================================================================
# AUTHENTICATION CONFIGURATION
# =============================================================================

authentication:
  users:
    - username: "admin"
      password: "changeme"  # CHANGE THIS PASSWORD!
      chassis: ["chassis-0"]
    - username: "user"
      password: "changeme"  # CHANGE THIS PASSWORD!
      chassis: ["chassis-1"]

# =============================================================================
# DATAVOLUME CONFIGURATION
# =============================================================================

datavolume:
  storage_size: "3Gi"
  allow_insecure_tls: true
  storage_class: "lvms-vg1"
  vm_update_timeout: "2m"
  iso_download_timeout: "30m"
```

## Usage

Once deployed, the Redfish API will be available at the service (k8s) or route (OCP) endpoint. In OpenShift, this will be the route endpoint (which the Helm Chart will use, if configured).

**Example** - Query the root URL without authentication (this is defined as per the Redfish specification)

```
curl -k https://kubevirt-redfish-{namespace}.apps.{cluster_name}.{domain_name}/redfish/v1/
```

**Example** - Return a list of managed systems (VMs)

```
curl -k -u user:pass https://kubevirt-redfish-{namespace}.apps.{cluster_name}.{domain_name}/redfish/v1/Systems
```

**Example** - Powering on a VM

```
curl -kX POST -u user:pass https://kubevirt-redfish-{namespace}.apps.{cluster_name}.{domain_name}/redfish/v1/Systems/{vm-id}/Actions/ComputerSystem.Reset \
  -H "Content-Type: application/json" \
  -d '{"ResetType": "On"}'
```

**Example** - Returned output from powering on a VM
```json
{
  "@odata.context": "/redfish/v1/$metadata#ActionResponse.ActionResponse",
  "@odata.id": "/redfish/v1/Systems/{vm-id}/Actions/ComputerSystem.Reset",
  "@odata.type": "#ActionResponse.v1_0_0.ActionResponse",
  "Id": "Reset",
  "Messages": [
    {
      "Message": "Power action On executed successfully"
    }
  ],
  "Name": "Reset Action",
  "Status": {
    "Health": "OK",
    "State": "Completed"
  }
}
```

## Troubleshooting

The `kubevirt-redfish` project has a fairly robust logging system that can be used for troubleshooting client-to-server communication. If you need to troubleshoot specific Redfish client-to-server requests, you can do something like the following below.

```shell
# Search for all logs with a specific correlation ID
oc logs -n kubevirt-redfish deployment/kubevirt-redfish | grep "298d7450ac8695c7"

# This will produce some additional details to help you understand more details about your client/server communication (this is just an empty/unauthenicated request from OpenShift's Advanced Cluster Manager)
2025/08/09 00:22:47 {"timestamp":"2025-08-09T00:22:47.510Z","level":"INFO","message":"Redfish API request received","correlation_id":"298d7450ac8695c7","fields":{"correlation_id":"298d7450ac8695c7","method":"GET","operation":"request","path":"/redfish/v1/","status":"started","user":"unknown"}}
2025/08/09 00:22:47 {"timestamp":"2025-08-09T00:22:47.510Z","level":"DEBUG","message":"Authentication attempt","correlation_id":"298d7450ac8695c7","fields":{"correlation_id":"298d7450ac8695c7","method":"GET","operation":"authentication","path":"/redfish/v1/","remote_addr":"10.128.0.2:45814","user":"unknown","user_agent":"kube-probe/1.32"}}
2025/08/09 00:22:47 {"timestamp":"2025-08-09T00:22:47.510Z","level":"INFO","message":"Redfish API response completed","correlation_id":"298d7450ac8695c7","fields":{"correlation_id":"298d7450ac8695c7","duration":"164.438µs","method":"GET","operation":"response","path":"/redfish/v1/","status":"completed","status_code":200,"user":"unknown"}}
```



## Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

---

**Built for the KubeVirt, Kubernetes, and OpenShift communities**
