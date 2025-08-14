# Migration Guide: Chassis-Based URI Structure

## Overview

This guide helps you migrate from the legacy Redfish API endpoints to the new chassis-based URI structure. The new structure eliminates VM name collisions and provides better Redfish specification compliance.

## What Changed

### **Before (Legacy)**
```
/redfish/v1/Systems/{vm-name}
```

### **After (New Chassis-Based)**
```
/redfish/v1/Chassis/{chassis-name}/Systems/{vm-name}
```

## Benefits

- **Eliminates VM name collisions** across different namespaces
- **Redfish specification compliant** URI structure
- **Clear resource hierarchy** with chassis isolation
- **Backward compatibility** maintained with automatic redirects

## Migration Steps

### 1. **No Configuration Changes Required**

Your existing Helm deployments and configurations will continue to work without any changes. The new chassis-based structure is automatically available.

### 2. **Update API Calls (Recommended)**

#### **Get System Information**

**Legacy:**
```bash
curl -u user:pass https://your-redfish-server/redfish/v1/Systems/my-vm
```

**New (Recommended):**
```bash
curl -u user:pass https://your-redfish-server/redfish/v1/Chassis/my-chassis/Systems/my-vm
```

#### **Power Operations**

**Legacy:**
```bash
curl -X POST -u user:pass \
  -H "Content-Type: application/json" \
  -d '{"ResetType": "On"}' \
  https://your-redfish-server/redfish/v1/Systems/my-vm/Actions/ComputerSystem.Reset
```

**New (Recommended):**
```bash
curl -X POST -u user:pass \
  -H "Content-Type: application/json" \
  -d '{"ResetType": "On"}' \
  https://your-redfish-server/redfish/v1/Chassis/my-chassis/Systems/my-vm/Actions/ComputerSystem.Reset
```

#### **Virtual Media Operations**

**Legacy:**
```bash
curl -X POST -u user:pass \
  -H "Content-Type: application/json" \
  -d '{"Image": "https://example.com/boot.iso"}' \
  https://your-redfish-server/redfish/v1/Systems/my-vm/VirtualMedia/cdrom0/Actions/VirtualMedia.InsertMedia
```

**New (Recommended):**
```bash
curl -X POST -u user:pass \
  -H "Content-Type: application/json" \
  -d '{"Image": "https://example.com/boot.iso"}' \
  https://your-redfish-server/redfish/v1/Chassis/my-chassis/Systems/my-vm/VirtualMedia/cdrom0/Actions/VirtualMedia.InsertMedia
```

### 3. **Discover Available Chassis**

To find which chassis (namespaces) are available:

```bash
curl -u user:pass https://your-redfish-server/redfish/v1/Chassis
```

### 4. **Discover Systems in a Chassis**

To find VMs in a specific chassis:

```bash
curl -u user:pass https://your-redfish-server/redfish/v1/Chassis/my-chassis/Systems
```

## Backward Compatibility

### **Automatic Redirects**

Legacy endpoints continue to work with automatic redirects:

```bash
# This will automatically redirect to the chassis-based URL
curl -u user:pass https://your-redfish-server/redfish/v1/Systems/my-vm
```

**Response Headers:**
```
HTTP/1.1 301 Moved Permanently
Location: /redfish/v1/Chassis/my-chassis/Systems/my-vm
Deprecation: true
Sunset: 2025-12-31
Link: </redfish/v1/Chassis/my-chassis/Systems/my-vm>; rel="successor-version"
```

### **Deprecation Timeline**

- **Phase 1 (Current)**: New endpoints available, legacy endpoints work with redirects
- **Phase 2 (Next Release)**: Legacy endpoints show deprecation warnings
- **Phase 3 (Future Release)**: Legacy endpoints removed

## Troubleshooting

### **VM Not Found Errors**

If you get "System not found" errors:

1. **Check chassis access**: Ensure your user has access to the chassis containing the VM
2. **Verify chassis name**: Use the chassis discovery endpoint to find the correct chassis name
3. **Check namespace**: Ensure the VM exists in the namespace mapped to the chassis

### **Authentication Issues**

If you get authentication errors:

1. **Verify user credentials**: Check username and password
2. **Check chassis permissions**: Ensure the user has access to the requested chassis
3. **Review configuration**: Check the authentication section in your `values.yaml`

## Examples

### **Complete Migration Example**

**Before (Legacy):**
```bash
# Get VM list
curl -u admin:password https://redfish.example.com/redfish/v1/Systems

# Get specific VM
curl -u admin:password https://redfish.example.com/redfish/v1/Systems/web-server-01

# Power on VM
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"ResetType": "On"}' \
  https://redfish.example.com/redfish/v1/Systems/web-server-01/Actions/ComputerSystem.Reset
```

**After (New Chassis-Based):**
```bash
# Get chassis list
curl -u admin:password https://redfish.example.com/redfish/v1/Chassis

# Get VMs in production chassis
curl -u admin:password https://redfish.example.com/redfish/v1/Chassis/production/Systems

# Get specific VM
curl -u admin:password https://redfish.example.com/redfish/v1/Chassis/production/Systems/web-server-01

# Power on VM
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"ResetType": "On"}' \
  https://redfish.example.com/redfish/v1/Chassis/production/Systems/web-server-01/Actions/ComputerSystem.Reset
```

## Support

If you encounter issues during migration:

1. **Check the logs**: Use `kubectl logs` or `oc logs` to see detailed error messages
2. **Review configuration**: Ensure your chassis and authentication configuration is correct
3. **Test with legacy endpoints**: Verify that automatic redirects are working
4. **Open an issue**: Report problems on the GitHub repository

---

**Note**: This migration is designed to be non-disruptive. Your existing deployments and scripts will continue to work while you gradually migrate to the new chassis-based endpoints. 