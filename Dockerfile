# Build stage
FROM docker.io/library/golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o kubevirt-redfish cmd/main.go

# Final stage
FROM docker.io/library/alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1001 -S kubevirt && \
    adduser -u 1001 -S kubevirt -G kubevirt

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/kubevirt-redfish .

# Create config directory and temp directory for ISO downloads
RUN mkdir -p /app/config /tmp && \
    chown -R kubevirt:kubevirt /app && \
    chmod 755 /tmp

# Switch to non-root user
USER kubevirt

# Expose port
EXPOSE 8443

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8443/redfish/v1/ || exit 1

# Run the application
ENTRYPOINT ["./kubevirt-redfish"]
CMD ["-config", "/app/config/config.yaml"] 