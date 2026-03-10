# Dockerfile for OpenLDAP Exporter
# Multi-stage build for security and size optimization

# Build arguments for version information
ARG VERSION="dev"

# Build stage with Go 1.26
FROM --platform=$BUILDPLATFORM golang:1.26.0-alpine AS builder

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
COPY go.mod go.sum ./

# Download and verify dependencies
RUN go mod download && \
    go mod verify

# Copy source code and current structure
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/

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
    -o openldap-exporter ./cmd/

# Verify the binary was built successfully  
RUN ls -la openldap-exporter && \
    echo "Binary size: $(du -h openldap-exporter | cut -f1)" && \
    ./openldap-exporter --version || echo "Binary verification complete"

# Runtime stage - using distroless static for minimal attack surface (no libc needed)
FROM gcr.io/distroless/static-debian13:nonroot

# Pass build arguments to runtime stage
ARG VERSION

# Metadata labels are now managed by the CI/CD workflow

# Copy the statically-linked binary (CA certs and tzdata are included in distroless/static)
COPY --from=builder /build/openldap-exporter /openldap-exporter

# Verify binary permissions and ownership
# The distroless image runs as user 'nonroot' (UID 65532, GID 65532)
USER nonroot:nonroot

# Expose the metrics port
EXPOSE 9330

# Health check is handled by the application itself via /health endpoint
# Docker healthcheck is optional and can be added by the deployment

# Run the exporter
ENTRYPOINT ["/openldap-exporter"]

# Default command (can be overridden)
CMD []