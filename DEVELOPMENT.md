# Development Guide

This guide explains how to develop and contribute to the OpenLDAP Prometheus Exporter project.

## Prerequisites

- Go 1.26+
- Git
- Make (optional but recommended)

## Development environment setup

### Install development tools

```bash
# With Make
make install-tools

# Or manually
go install github.com/kisielk/errcheck@latest
go install github.com/gordonklaus/ineffassign@latest
# Install golangci-lint separately from: https://golangci-lint.run/usage/install/
```

### Setup environment

```bash
# Initial setup
make dev-setup

# Install git hooks (optional)
make setup-hooks
```

## Development workflow

### Before coding

```bash
# Get latest changes
git pull origin main

# Create new branch
git checkout -b feature/my-new-feature
```

### During development

```bash
# Check code regularly
make pre-check

# Format code
make format

# Fix linting issues
make lint-fix

# Run tests
make test
```

### Before pushing

```bash
# Complete verification (like CI)
make ci-local

# If everything passes, push
git add .
git commit -m "feat: my new feature"
git push origin feature/my-new-feature
```

## Make commands

| Command | Description |
|---------|-------------|
| `make help` | Show help |
| `make pre-check` | Run all pre-commit checks (like CI) |
| `make format` | Format Go code |
| `make lint` | Run linters |
| `make lint-fix` | Auto-fix linting issues |
| `make test` | Run unit tests |
| `make test-coverage` | Tests with coverage |
| `make test-integration` | Integration tests |
| `make test-race` | Tests with race detector (requires CGO) |
| `make build` | Build application |
| `make ci-local` | Simulate CI pipeline locally |

## GitHub Actions quality assurance

The Quality Assurance workflow (`quality-assurance.yml`) includes comprehensive validation:

### Pre-check job (runs first, blocks other jobs if failed)

- Code formatting (gofmt)
- Go modules verification
- Basic static analysis (go vet)
- Ineffective assignments (ineffassign)
- Quick linting (golangci-lint)

### Main jobs (run in parallel after pre-check passes)

- **Unit tests**: Race detection, coverage analysis (70% minimum)
- **Integration tests**: Full LDAP service testing with Bitnami OpenLDAP
- **Static analysis**: 7 advanced tools (staticcheck, errcheck, shadow, goimports, misspell, gocyclo)
- **Security analysis**: gosec, govulncheck, dependency review

All jobs must pass for the workflow to succeed.

## Local pre-check

Run pre-check validations locally to match CI requirements:

```bash
make pre-check
```

This runs the same core validations as the GitHub Actions pre-check job.

## Testing

### Running tests

```bash
# Quick unit tests
make test

# Tests with coverage report
make test-coverage

# Integration tests (requires LDAP setup)
make test-integration

# Race detector tests (requires CGO)
CGO_ENABLED=1 make test-race

# All tests (like CI)
make ci-local
```

### Test requirements

- **Unit tests**: Must pass with race detector
- **Coverage**: Minimum 70% code coverage required
- **Integration tests**: Test against real LDAP service
- **No skipped tests**: All tests must run successfully

### Test environment setup

Integration tests require environment variables:

```bash
export TEST_LDAP_URL=ldap://localhost:389
export TEST_LDAP_USERNAME=admin
export TEST_LDAP_PASSWORD=password
export TEST_LDAP_BASE_DN=dc=example,dc=com
```

### Test structure

The project follows Go testing best practices:

- **Unit tests**: `*_test.go` files alongside source code
- **Fuzz tests**: `helpers_fuzz_test.go` for randomized input validation
- **Benchmark tests**: `benchmark_test.go` for performance regression detection
- **Integration tests**: `tests/` directory with build tag `integration`
- **Test helpers**: Setup/cleanup functions for consistent test environments
- **Table-driven tests**: Used for comprehensive scenario coverage

Key test packages:

- `cmd/main_test.go`: HTTP handlers, security headers, method validation, graceful shutdown
- `pkg/exporter/exporter_test.go`: Counter tracking, cleanup, atomic stats
- `pkg/exporter/helpers_fuzz_test.go`: Fuzz tests for key validation and CN extraction
- `pkg/exporter/benchmark_test.go`: Benchmarks for metrics, counters, validation
- `pkg/metrics/metrics_test.go`: All metric types (Gauge, Counter, Histogram), concurrent access
- `pkg/config/config_test.go`: Config loading, validation, metric/DC filtering
- `pkg/security/security_test.go`: Rate limiting, DN/filter validation, IP extraction
- `pkg/monitoring/monitoring_test.go`: Internal monitoring and metrics
- `pkg/pool/pool_test.go`: Connection pool lifecycle and maintenance
- `pkg/pool/pooled_client_test.go`: LDAP client wrapper tests
- `pkg/circuitbreaker/circuitbreaker_test.go`: Circuit breaker pattern tests
- `pkg/logger/logger_test.go`: Logging, sanitization, safe logging functions
- `tests/integration_core_test.go`: Integration tests with build tag

## Pre-push checklist

- [ ] Code is formatted (`make format`)
- [ ] Tests pass (`make test`)
- [ ] Coverage is >= 70% (`make test-coverage`)
- [ ] Linting passes (`make lint`)
- [ ] Pre-checks pass (`make pre-check`)
- [ ] Build works (`make build`)

## Common issues

### Formatting issues

```bash
# Auto-fix
make format
```

### Linting issues

```bash
# View issues
make lint

# Auto-fix
make lint-fix
```

### Module issues

```bash
# Clean and reorganize
make tidy
```

### Test failures

```bash
# Run specific test packages
go test -v ./cmd
go test -v ./pkg/exporter
go test -v ./pkg/security
go test -v ./pkg/monitoring

# Run with race detector for concurrency issues
go test -race -v ./...

# Check test coverage for specific packages
go test -coverprofile=coverage.out ./pkg/exporter
go tool cover -html=coverage.out
```

### Running fuzz and benchmark tests

```bash
# Fuzz tests (randomized input testing)
go test -fuzz=FuzzValidateKey -fuzztime=10s ./pkg/exporter/
go test -fuzz=FuzzExtractCNFromFirstComponent -fuzztime=10s ./pkg/exporter/

# Benchmark tests
go test -bench=. -benchmem ./pkg/exporter/
```

## Project structure

```text
cmd/
└── main.go                         # Entry point, HTTP server, signal handling

pkg/
├── config/                         # Configuration loading and validation
│   ├── config.go                   # Env var parsing, metric/DC filtering
│   └── secure.go                   # SecureString (ChaCha20-Poly1305 encrypted password)
├── exporter/                       # Prometheus collector implementation
│   ├── exporter.go                 # Collector interface, lifecycle, concurrency control
│   ├── collectors.go               # Common collector utilities
│   ├── collectors_resource.go      # Connections, threads, time, waiters, health metrics
│   ├── collectors_statistics.go    # Statistics counters (bytes, PDU, referrals, entries)
│   ├── collectors_info.go          # Overlays, TLS, backends, listeners, server info, SASL, log
│   ├── collectors_database.go      # Database entries and replication info
│   ├── collectors_replication.go   # contextCSN parsing and replication lag
│   ├── counter_management.go       # Monotonic counter delta tracking with typed map
│   └── helpers.go                  # Retry logic, key validation, DN extraction
├── metrics/                        # Prometheus metric definitions
│   └── metrics.go                  # All GaugeVec, CounterVec, HistogramVec definitions
├── pool/                           # LDAP connection pool
│   ├── pool.go                     # Pool struct, constructor, Close, Stats
│   ├── pool_connections.go         # Connection creation, TLS config, SASL EXTERNAL bind
│   ├── pool_operations.go          # Get/Release connection operations
│   ├── pool_maintenance.go         # Background health checks and idle cleanup
│   ├── pool_metrics.go             # Pool monitoring metric updates
│   └── pooled_client.go            # High-level LDAP client with circuit breaker
├── monitoring/                     # Internal exporter metrics
│   ├── monitoring.go               # Struct, constructor, histogram buckets
│   ├── monitoring_pool.go          # Pool-related metric recording
│   ├── monitoring_collection.go    # Circuit breaker, rate limit, system metrics
│   ├── monitoring_events.go        # Event recording and scrape tracking
│   └── monitoring_registry.go      # Prometheus registry registration
├── circuitbreaker/                 # Circuit breaker pattern
│   └── circuitbreaker.go           # State machine (closed/open/half-open)
├── logger/                         # Structured logging with sanitization
│   └── logger.go                   # Zerolog wrapper, sensitive field redaction
└── security/                       # Security utilities
    ├── validation.go               # LDAP DN and filter input validation
    └── ratelimit.go                # Token bucket rate limiter per IP
```

## Development tips

1. Run `make pre-check` frequently to avoid surprises
2. Use git hooks with `make setup-hooks` for automatic checks
3. Test locally with `make ci-local` to simulate complete CI
4. Document your changes and update tests
5. Keep commits small and atomic

## Common mistakes to avoid

- Pushing unformatted code
- Ignoring linting warnings
- Not testing before pushing
- Forgetting to update tests
- Breaking code coverage
