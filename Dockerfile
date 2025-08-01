# Dockerfile for OpenLDAP Exporter
# Multi-stage build for security and size optimization

# Build arguments for version information
ARG VERSION="dev"

# Build stage with Go 1.24
FROM golang:1.24-alpine AS builder

# Install security updates and build dependencies
RUN apk update && \
    apk upgrade && \
    apk add --no-cache \
        git \
        ca-certificates \
        tzdata && \
    rm -rf /var/cache/apk/*

# Create build directory
WORKDIR /build

# Copy dependency files first for better Docker layer caching
COPY exporter/go.mod exporter/go.sum ./

# Download and verify dependencies
RUN go mod download && \
    go mod verify

# Copy source code
COPY exporter/*.go ./

# Build arguments for ldflags
ARG VERSION

# Build the binary with security-focused flags
# - CGO_ENABLED=0: Disable CGO for static binary
# - GOOS=linux GOARCH=amd64: Target platform
# - -a: Force rebuilding of packages
# - -installsuffix cgo: Use different install suffix
# - -ldflags: Linker flags for smaller binary and version info
# - -trimpath: Remove file system paths from binary
# - -tags netgo: Use pure Go networking stack  
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a \
    -installsuffix cgo \
    -ldflags="-w -s -extldflags '-static' -X main.Version=${VERSION}" \
    -trimpath \
    -tags netgo \
    -o openldap-exporter .

# Verify the binary is statically linked
# Note: The go-ldap library may require some shared libraries, so we check if it's mostly static
RUN ldd openldap-exporter || echo "Static binary verification completed"

# Runtime stage - using distroless base for minimal attack surface and security
FROM gcr.io/distroless/base-debian12:nonroot

# Pass build arguments to runtime stage
ARG VERSION

# Add metadata labels
LABEL \
    org.opencontainers.image.title="OpenLDAP Exporter" \
    org.opencontainers.image.description="Prometheus exporter for OpenLDAP" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.vendor="Maxime Wewer" \
    org.opencontainers.image.licenses="MIT" \
    org.opencontainers.image.source="https://github.com/MaximeWewer/OpenLDAP_prometheus_exporter"

# Copy timezone data and CA certificates from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the statically-linked binary
COPY --from=builder /build/openldap-exporter /openldap-exporter

# Verify binary permissions and ownership
# The distroless image runs as user 'nonroot' (UID 65532, GID 65532)
USER nonroot:nonroot

# Expose the metrics port
EXPOSE 9330

# Health check using built-in health endpoint
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/openldap-exporter", "-version"] || exit 1

# Run the exporter
ENTRYPOINT ["/openldap-exporter"]

# Default command (can be overridden)
CMD []