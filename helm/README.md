# KubeVirt Redfish Helm Chart

This Helm chart deploys the KubeVirt Redfish API server, which provides a Redfish-compliant REST API for managing KubeVirt virtual machines. The server enables integration with tools like Metal3/Ironic for bare metal provisioning workflows and Zero Touch Provisioning (ZTP) automation.

## Chart Version

- **Chart Version**: 0.2.1-73abb30
- **App Version**: v0.2.1
- **Kubernetes**: 1.25+
- **Helm**: 3.0+

## Prerequisites

- Kubernetes 1.25+ or OpenShift 4.18+
- Helm 3.0+
- KubeVirt v1.4+ (v1) installed and running
- CDI (Containerized Data Importer) for virtual media support

## Quick Start

### Option 1: OCI Registry Deployment (Recommended)

```bash
# Show available values
helm show values oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish --version 0.2.1

# Install with default values
helm install kubevirt-redfish oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish \
  --version 0.2.1 \
  --namespace kubevirt-redfish \
  --create-namespace

# Install with custom values
helm install kubevirt-redfish oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish \
  --version 0.2.1 \
  --namespace kubevirt-redfish \
  --create-namespace \
  -f my-values.yaml
```

### Option 2: Local Chart Deployment

```bash
# Install with default values
helm install kubevirt-redfish ./helm \
  --namespace kubevirt-redfish \
  --create-namespace

# Install with example values
helm install kubevirt-redfish ./helm \
  -f helm/values-example.yaml \
  --namespace kubevirt-redfish \
  --create-namespace

# Install with custom values
helm install kubevirt-redfish ./helm \
  -f my-values.yaml \
  --namespace kubevirt-redfish \
  --create-namespace

# Override specific values
helm install kubevirt-redfish ./helm \
  --set global.namespace=my-namespace \
  --set authentication.users[0].password=my-password \
  --namespace my-namespace \
  --create-namespace
```

## Configuration Files

### `values.yaml` (Default)
- **Purpose**: Well-documented template with all available options
- **Features**: 
  - Comprehensive documentation and examples
  - Generic configuration suitable for most deployments
  - All parameters documented with comments

### `values-example.yaml` (Example)
- **Purpose**: Practical example configuration
- **Features**:
  - Clean, simple configuration for quick setup
  - Debug logging enabled for testing
  - Example route hostname and storage class
  - Multi-namespace and multi-user setup

## Configuration

### Key Parameters

| Parameter | Description | Default | Example |
|-----------|-------------|---------|---------|
| `global.namespace` | Deployment namespace | `"kubevirt-redfish"` | `"my-namespace"` |
| `global.image.tag` | Container image tag | `"v0.2.1"` | `"v0.2.1"` |
| `deployment.replicas` | Number of replicas | `1` | `1` |
| `route.host` | OpenShift route hostname | `"kubevirt-redfish-default.apps.clustername.example.com"` | `"kubevirt-redfish-default.apps.clustername.example.com"` |
| `datavolume.storage_class` | Storage class for ISOs | `""` (default) | `"lvms-vg1"` |
| `authentication.users` | API users | `admin/admin123` | `admin/secure-password` |

### Chassis Configuration

Chassis represent logical groupings of VMs, typically corresponding to namespaces:

```yaml
chassis:
  - name: "production"
    namespace: "production-vms"
    service_account: "kubevirt-redfish"
    description: "Production VMs"
  - name: "development"
    namespace: "development-vms"
    service_account: "kubevirt-redfish"
    description: "Development VMs"
```

### Authentication Configuration

Configure API users with access to specific chassis:

```yaml
authentication:
  users:
    - username: "admin"
      password: "admin-password"
      chassis: ["production", "development"]
    - username: "developer"
      password: "developer-password"
      chassis: ["development"]
    - username: "operator"
      password: "operator-password"
      chassis: ["production"]
```

**Note**: Chassis follow DMTF Redfish API standards and can be mapped to Kubernetes namespaces.

## Usage Examples

### Development Environment

```yaml
# values-dev.yaml
global:
  namespace: "dev-kubevirt"

deployment:
  replicas: 1

authentication:
  users:
    - username: "dev"
      password: "dev123"
      chassis: ["dev"]

datavolume:
  allow_insecure_tls: true
  storage_class: "standard"
```

### Production Environment

```yaml
# values-prod.yaml
global:
  namespace: "prod-kubevirt"

deployment:
  replicas: 3

authentication:
  users:
    - username: "admin"
      password: "your-secure-password"
      chassis: ["production"]

datavolume:
  allow_insecure_tls: false
  storage_class: "fast-ssd"
```

### Multi-Tenant Environment

```yaml
# values-multitenant.yaml
chassis:
  - name: "tenant1"
    namespace: "tenant1-vms"
    description: "Tenant 1 VMs"
  - name: "tenant2"
    namespace: "tenant2-vms"
    description: "Tenant 2 VMs"

authentication:
  users:
    - username: "tenant1-admin"
      password: "tenant1-pass"
      chassis: ["tenant1"]
    - username: "tenant2-admin"
      password: "tenant2-pass"
      chassis: ["tenant2"]
```

## API Usage

### Access the Redfish API

```bash
# Get the route URL
kubectl get route kubevirt-redfish -n your-namespace

# Port-forward for local testing
kubectl port-forward svc/kubevirt-redfish 8443:8443 -n your-namespace
```

### Example API Calls

```bash
# Set variables
export ROUTE_URL=kubevirt-redfish-default.apps.clustername.example.com
export TEST_VM=my-vm-name
export TEST_USER='admin'
export TEST_PASS='password'

# Root endpoint (unauthenticated, required by DMTF standards)
curl -k https://$ROUTE_URL/redfish/v1/

# Get VM information (authenticated)
curl -u $TEST_USER:$TEST_PASS https://$ROUTE_URL/redfish/v1/Systems/$TEST_VM

# Power on a VM
curl -X POST -u $TEST_USER:$TEST_PASS \
  -H "Content-Type: application/json" \
  -d '{"ResetType": "On"}' \
  https://$ROUTE_URL/redfish/v1/Systems/$TEST_VM/Actions/ComputerSystem.Reset

# Insert virtual media (ISO)
curl -X POST -u $TEST_USER:$TEST_PASS \
  -H "Content-Type: application/json" \
  -d '{"Image": "https://example.com/boot.iso"}' \
  https://$ROUTE_URL/redfish/v1/Systems/$TEST_VM/VirtualMedia/cdrom0/Actions/VirtualMedia.InsertMedia
```

## Virtual Media Operations

The server supports full Redfish VirtualMedia operations:

- **Insert Media**: Downloads ISO from URL and creates DataVolume
- **Eject Media**: Removes DataVolume and cleans up storage
- **Boot Override**: Configures VM to boot from CD-ROM
- **Race Condition Prevention**: Unique helper pod naming with timestamps

### DataVolume Configuration

```yaml
datavolume:
  storage_size: "3Gi"              # Size for ISO storage
  storage_class: "fast-ssd"        # Storage class for DataVolumes
  allow_insecure_tls: false        # Security for ISO downloads
  vm_update_timeout: "2m"          # VM update timeout
  iso_download_timeout: "30m"      # ISO download timeout
```

## Security

### RBAC Resources

The chart creates necessary RBAC resources:

- **ServiceAccount**: `kubevirt-redfish`
- **ClusterRole**: Permissions for KubeVirt operations
- **ClusterRoleBinding**: Binds role to service account

### Security Context

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000000001  # OpenShift compatible UID
  runAsGroup: 1000000001
  fsGroup: 1000000001
```

### Authentication

Basic authentication with username/password pairs. **Important**: Change default passwords in production!

### OpenShift Security Context Constraints (SCC)

**Automatic Setup (Default)**: The chart automatically detects OpenShift and applies the appropriate SCC during installation. No manual intervention required!

#### Manual Setup (Optional)

If you prefer to handle SCCs manually or the automatic setup fails, you have these options:

##### Option 1: Use Restricted SCC (Recommended - Most Secure)

The `restricted` SCC is the most secure option and should work for this application:

```bash
oc adm policy add-scc-to-user restricted -z kubevirt-redfish -n your-namespace
```

##### Option 2: Use Custom SCC (Fallback)

If the restricted SCC doesn't work, you can enable the custom SCC included in this chart:

```yaml
# In your values.yaml
rbac:
  scc:
    enabled: true
    custom: true
```

Then apply the SCC:

```bash
oc adm policy add-scc-to-user kubevirt-redfish-scc -z kubevirt-redfish -n your-namespace
```

##### Option 3: Use Anyuid SCC (Last Resort - Least Secure)

**Only use this if both above options fail:**

```bash
oc adm policy add-scc-to-user anyuid -z kubevirt-redfish -n your-namespace
```

##### Disable SCC Functionality

If you want to handle SCCs manually, disable SCC functionality:

```yaml
# In your values.yaml
rbac:
  scc:
    enabled: false
```

##### Enable Custom SCC

If you need a custom SCC instead of using OpenShift's built-in ones:

```yaml
# In your values.yaml
rbac:
  scc:
    enabled: true
    custom: true
    customName: "my-custom-scc"  # Optional: customize the SCC name
```

#### Security Comparison

| SCC Type | Security Level | UID Restrictions | Host Access | Privileges |
|----------|---------------|------------------|-------------|------------|
| **Restricted** | **Highest** | Must use UID ranges | None | Minimal |
| Custom SCC | High | Must use UID 1000000001 | None | Minimal |
| Anyuid | Low | Any UID allowed | Potential | Elevated |

#### Troubleshooting SCC Issues

If you encounter permission errors:

1. **Check if the UID in values.yaml matches OpenShift range**: Should be `1000000001`
2. **Verify service account exists**: `oc get sa kubevirt-redfish -n your-namespace`
3. **Check pod events**: `oc describe pod <pod-name> -n your-namespace`
4. **If still failing**: Temporarily use anyuid, then investigate why restricted isn't working

**Note**: The chart is configured with OpenShift-compatible UIDs by default. If you're not using OpenShift, you can change the UIDs back to standard values (e.g., `1000`).

## Monitoring and Logging

### Log Levels

```yaml
env:
  LOG_LEVEL: "info"                   # Application log level / Options: debug, info, warn, error
  REDFISH_LOG_LEVEL: "INFO"           # Redfish-specific logging / Options: DEBUG, INFO, WARN, ERROR
  REDFISH_LOGGING_ENABLED: "true"     # Enable structured logging
```

### Health Checks

- **Liveness Probe**: `/redfish/v1/` endpoint
- **Readiness Probe**: `/redfish/v1/` endpoint
- **Graceful Shutdown**: Signal handling for clean termination

## Troubleshooting

### Check Deployment Status

```bash
# Check pods
kubectl get pods -n your-namespace -l app.kubernetes.io/name=kubevirt-redfish

# Check services
kubectl get svc -n your-namespace -l app.kubernetes.io/name=kubevirt-redfish

# Check routes (OpenShift)
kubectl get route -n your-namespace -l app.kubernetes.io/name=kubevirt-redfish
```

### View Logs

```bash
# Get pod logs
kubectl logs -f deployment/kubevirt-redfish -n your-namespace

# Filter for specific operations
kubectl logs deployment/kubevirt-redfish -n your-namespace | grep -E "(virtual media|race condition|PVC|ISO)"
```

### Common Issues

1. **PVC Creation Fails**: Check storage class availability
2. **ISO Download Fails**: Verify URL accessibility and TLS settings
3. **Authentication Fails**: Verify username/password and chassis access
4. **Race Conditions**: Check for "pods already exists" errors in logs

## Upgrading

```bash
# Upgrade with OCI registry
helm upgrade kubevirt-redfish oci://quay.io/bjozsa-redhat/charts/kubevirt-redfish \
  --version 0.2.1 \
  --namespace kubevirt-redfish

# Upgrade with local chart
helm upgrade kubevirt-redfish ./helm \
  --namespace kubevirt-redfish

# Upgrade with custom values
helm upgrade kubevirt-redfish ./helm \
  -f my-values.yaml \
  --namespace kubevirt-redfish
```

## Troubleshooting

### Common Issues

1. **PVC Creation Fails**: Check storage class availability
2. **ISO Download Fails**: Verify URL accessibility and TLS settings
3. **Authentication Fails**: Verify username/password and chassis access
4. **Permission Errors**: Check RBAC and SCC configuration

### Getting Help

- **GitHub Issues**: [Create an issue](https://github.com/v1k0d3n/kubevirt-redfish/issues)
- **Documentation**: Check the [main README](https://github.com/v1k0d3n/kubevirt-redfish)
- **Logs**: Use `kubectl logs -f deployment/kubevirt-redfish -n your-namespace` 