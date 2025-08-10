#!/bin/bash
set -e

# Integration test script for kubevirt-redfish
# This script tests the API with a real Kubernetes cluster
#
# Environment variables:
#   TEST_HOST: Server host (default: localhost)
#   TEST_PORT: Server port (default: 8443)
#   TEST_USER: Username (default: testuser)
#   TEST_PASS: Password (default: testpass)

echo "Running integration tests for kubevirt-redfish..."

# Set defaults for test environment
TEST_HOST="${TEST_HOST:-localhost}"
TEST_PORT="${TEST_PORT:-8443}"
TEST_USER="${TEST_USER:-testuser}"
TEST_PASS="${TEST_PASS:-testpass}"
TEST_URL="http://${TEST_HOST}:${TEST_PORT}"

echo "Test configuration:"
echo "  Host: ${TEST_HOST}"
echo "  Port: ${TEST_PORT}"
echo "  User: ${TEST_USER}"
echo "  URL: ${TEST_URL}"

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "Error: kubectl is not installed or not in PATH"
    exit 1
fi

# Check if we have access to a cluster
if ! kubectl cluster-info &> /dev/null; then
    echo "Error: Cannot connect to Kubernetes cluster"
    exit 1
fi

# Check if KubeVirt is installed
if ! kubectl get crd virtualmachines.kubevirt.io &> /dev/null; then
    echo "Warning: KubeVirt CRD not found. Some tests may fail."
fi

# Check if jq is available for JSON parsing
if ! command -v jq &> /dev/null; then
    echo "Error: jq is not installed or not in PATH"
    exit 1
fi

# Build the binary
echo "Building kubevirt-redfish binary..."
go build -o kubevirt-redfish ./cmd/main.go

# Create test configuration
echo "Creating test configuration..."
cat > test-config.yaml << EOF
server:
  host: "0.0.0.0"
  port: ${TEST_PORT}
  tls:
    enabled: false

chassis:
  - name: "test-chassis"
    namespace: "default"
    service_account: "default"
    description: "Test chassis for integration testing"

authentication:
  users:
    - username: "${TEST_USER}"
      password: "${TEST_PASS}"
      chassis: ["test-chassis"]

kubevirt:
  api_version: "v1"
  timeout: 30
EOF

# Start the server in background
echo "Starting kubevirt-redfish server..."
./kubevirt-redfish --config test-config.yaml &
SERVER_PID=$!

# Function to cleanup on exit
cleanup() {
    echo "Cleaning up..."
    if [ -n "$SERVER_PID" ]; then
        kill $SERVER_PID 2>/dev/null || true
    fi
    rm -f test-config.yaml
    rm -f kubevirt-redfish
}

# Set trap to cleanup on script exit
trap cleanup EXIT

# Wait for server to start
echo "Waiting for server to start..."
sleep 5

# Test if server is responding
echo "Testing server connectivity..."
if ! curl -s --max-time 10 "${TEST_URL}/redfish/v1/" > /dev/null; then
    echo "Error: Server is not responding at ${TEST_URL}"
    exit 1
fi

# Test service root endpoint
echo "Testing service root endpoint..."
if curl -s -u "${TEST_USER}:${TEST_PASS}" "${TEST_URL}/redfish/v1/" | jq . > /dev/null; then
    echo "✓ Service root endpoint working"
else
    echo "✗ Service root endpoint failed"
    exit 1
fi

# Test chassis collection endpoint
echo "Testing chassis collection endpoint..."
if curl -s -u "${TEST_USER}:${TEST_PASS}" "${TEST_URL}/redfish/v1/Chassis" | jq . > /dev/null; then
    echo "✓ Chassis collection endpoint working"
else
    echo "✗ Chassis collection endpoint failed"
    exit 1
fi

# Test specific chassis endpoint
echo "Testing specific chassis endpoint..."
if curl -s -u "${TEST_USER}:${TEST_PASS}" "${TEST_URL}/redfish/v1/Chassis/test-chassis" | jq . > /dev/null; then
    echo "✓ Specific chassis endpoint working"
else
    echo "✗ Specific chassis endpoint failed"
    exit 1
fi

# Test systems collection endpoint
echo "Testing systems collection endpoint..."
if curl -s -u "${TEST_USER}:${TEST_PASS}" "${TEST_URL}/redfish/v1/Systems" | jq . > /dev/null; then
    echo "✓ Systems collection endpoint working"
else
    echo "✗ Systems collection endpoint failed"
    exit 1
fi

# Test authentication
echo "Testing authentication..."
if curl -s -u wronguser:wrongpass "${TEST_URL}/redfish/v1/" | grep -q "401"; then
    echo "✓ Authentication working correctly"
else
    echo "✗ Authentication test failed"
    exit 1
fi

# Test authorization
echo "Testing authorization..."
if curl -s -u "${TEST_USER}:${TEST_PASS}" "${TEST_URL}/redfish/v1/Chassis/other-chassis" | grep -q "403"; then
    echo "✓ Authorization working correctly"
else
    echo "✗ Authorization test failed"
    exit 1
fi

echo "Integration tests completed successfully!" 