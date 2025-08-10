#!/bin/bash
set -e

# Script to build and push container to registry using Buildah
# Usage: ./scripts/build-and-push-container.sh <tag>
#
# Environment variables:
#   REGISTRY: Container registry (default: quay.io)
#   NAMESPACE: Registry namespace (default: bjozsa-redhat)
#   IMAGE_NAME: Image name (default: kubevirt-redfish)
#   QUAY_USERNAME: Registry username
#   QUAY_PASSWORD: Registry password

if [ $# -eq 0 ]; then
    echo "Usage: $0 <tag>"
    echo "Example: $0 v0.1.0"
    echo ""
    echo "Environment variables:"
    echo "  REGISTRY: Container registry (default: quay.io)"
    echo "  NAMESPACE: Registry namespace (default: bjozsa-redhat)"
    echo "  IMAGE_NAME: Image name (default: kubevirt-redfish)"
    echo "  QUAY_USERNAME: Registry username"
    echo "  QUAY_PASSWORD: Registry password"
    exit 1
fi

TAG=$1
REGISTRY="${REGISTRY:-quay.io}"
NAMESPACE="${NAMESPACE:-bjozsa-redhat}"
IMAGE_NAME="${IMAGE_NAME:-kubevirt-redfish}"
FULL_IMAGE_NAME="${REGISTRY}/${NAMESPACE}/${IMAGE_NAME}:${TAG}"

echo "Building container image for tag: ${TAG}"
echo "Image: ${FULL_IMAGE_NAME}"
echo "Registry: ${REGISTRY}"
echo "Namespace: ${NAMESPACE}"
echo "Image Name: ${IMAGE_NAME}"

# Debug: Show available variables (without sensitive data)
echo "Debug: Available variables:"
echo "REGISTRY: ${REGISTRY}"
echo "NAMESPACE: ${NAMESPACE}"
echo "IMAGE_NAME: ${IMAGE_NAME}"
echo "QUAY_USERNAME: ${QUAY_USERNAME:+set}"
echo "QUAY_PASSWORD: ${QUAY_PASSWORD:+set}"

# Check if credentials are provided
if [ -z "${QUAY_USERNAME}" ] || [ -z "${QUAY_PASSWORD}" ]; then
    echo "Error: QUAY_USERNAME and QUAY_PASSWORD environment variables are required"
    exit 1
fi

# Login to registry
echo "Logging into ${REGISTRY}..."
buildah login -u "${QUAY_USERNAME}" -p "${QUAY_PASSWORD}" "${REGISTRY}"

# Build the container using Buildah
echo "Building container image..."
buildah bud --format docker --pull-always -t "${FULL_IMAGE_NAME}" .

# Push the container
echo "Pushing container image..."
buildah push "${FULL_IMAGE_NAME}" "docker://${FULL_IMAGE_NAME}"

echo "Container build and push completed successfully"
echo "Image available at: ${FULL_IMAGE_NAME}" 