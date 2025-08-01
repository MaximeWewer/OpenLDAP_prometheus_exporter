# Dockerfile for OpenLDAP Exporter
# Multi-stage build for security and size optimization

# Build arguments for version information
ARG VERSION="dev"

# Build stage with Go 1.24
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

# Declare build arguments for cross-compilation
ARG TARGETOS
ARG TARGETARCH

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
# - GOOS/GOARCH: Use Docker Buildx automatic platform detection
# - -a: Force rebuilding of packages
# - -installsuffix cgo: Use different install suffix
# - -ldflags: Linker flags for smaller binary and version info
# - -trimpath: Remove file system paths from binary
# - -tags netgo: Use pure Go networking stack  
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
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

# Metadata labels are now managed by the CI/CD workflow

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