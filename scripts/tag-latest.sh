#!/bin/bash
set -e

# Script to tag an existing image with 'latest'
# Usage: ./scripts/tag-latest.sh <commit-sha>
#
# Environment variables:
#   REGISTRY: Container registry (default: quay.io)
#   NAMESPACE: Registry namespace (default: bjozsa-redhat)
#   IMAGE_NAME: Image name (default: kubevirt-redfish)
#   QUAY_USERNAME: Registry username
#   QUAY_PASSWORD: Registry password

if [ $# -eq 0 ]; then
    echo "Usage: $0 <commit-sha>"
    echo "Example: $0 85b872ea"
    echo ""
    echo "Environment variables:"
    echo "  REGISTRY: Container registry (default: quay.io)"
    echo "  NAMESPACE: Registry namespace (default: bjozsa-redhat)"
    echo "  IMAGE_NAME: Image name (default: kubevirt-redfish)"
    echo "  QUAY_USERNAME: Registry username"
    echo "  QUAY_PASSWORD: Registry password"
    exit 1
fi

COMMIT_SHA=$1
REGISTRY="${REGISTRY:-quay.io}"
NAMESPACE="${NAMESPACE:-bjozsa-redhat}"
IMAGE_NAME="${IMAGE_NAME:-kubevirt-redfish}"
SOURCE_IMAGE="${REGISTRY}/${NAMESPACE}/${IMAGE_NAME}:${COMMIT_SHA}"
LATEST_IMAGE="${REGISTRY}/${NAMESPACE}/${IMAGE_NAME}:latest"

echo "Tagging image with 'latest'"
echo "Source: ${SOURCE_IMAGE}"
echo "Target: ${LATEST_IMAGE}"

# Check if credentials are provided
if [ -z "${QUAY_USERNAME}" ] || [ -z "${QUAY_PASSWORD}" ]; then
    echo "Error: QUAY_USERNAME and QUAY_PASSWORD environment variables are required"
    exit 1
fi

# Login to registry
echo "Logging into ${REGISTRY}..."
buildah login -u "${QUAY_USERNAME}" -p "${QUAY_PASSWORD}" "${REGISTRY}"

# Pull the image with tag "${SOURCE_IMAGE}"
echo "Pulling image..."
buildah pull "${SOURCE_IMAGE}"

# Tag the image with 'latest'
echo "Tagging image..."
buildah tag "${SOURCE_IMAGE}" "${LATEST_IMAGE}"

# Push the latest tag
echo "Pushing latest tag..."
buildah push "${LATEST_IMAGE}" "docker://${LATEST_IMAGE}"

echo "Successfully tagged ${COMMIT_SHA} as latest"
echo "Latest image available at: ${LATEST_IMAGE}" 