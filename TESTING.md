# Tests

Test directory contains comprehensive tests for the OpenLDAP Prometheus Exporter.

## Structure

```text
tests/
├── integration_core_test.go          # Go-based core component tests
├── data/                             # Test data and configurations
│   ├── ldap-schemas/                 # LDAP schema files for Monitor backend setup
│   │   ├── base-monitoring.ldif
│   │   ├── enable-monitor.ldif
│   │   └── monitor-acl.ldif
│   └── test-data/                    # Sample LDAP entries for testing
│       └── multi-domain-users.ldif
└── scripts/                          # Test automation scripts
    ├── detect-ldap-structure.sh      # LDAP environment detection
    ├── run-all-tests.sh              # Complete test suite runner (local dev)
    ├── run-operation-metrics-test.sh # Operation metrics validation (used in CI/CD)
    └── setup-ldap.sh                 # LDAP environment setup (used in CI/CD)
```

## Test Types

### 1. Unit Tests (Go-based)

Core functionality tests without external dependencies:

```bash
go test -v ./...
go test -race -coverprofile=coverage.out ./...
```

### 2. Core Component Tests (Go-based)

Tests Go-specific functionality (connection pools, circuit breakers, internal logic):

```bash
# Test core Go components
go test -tags=integration -v ./tests/...

# With custom LDAP configuration
TEST_LDAP_URL=ldap://localhost:1389 \
TEST_LDAP_USERNAME="cn=adminconfig,cn=config" \
TEST_LDAP_PASSWORD=configpassword \
go test -tags=integration -v ./tests/...
```

### 3. Operation Metrics Tests (Bash-based)

Dynamic tests that configure LDAP Monitor backend and validate metrics collection:

```bash
# Complete operation metrics validation
./tests/scripts/run-operation-metrics-test.sh

# With custom configuration
LDAP_PORT=1389 EXPORTER_PORT=9360 \
./tests/scripts/run-operation-metrics-test.sh --traffic 50
```

### 4. Complete Test Suite

Run all test types with a single command:

```bash
# Run all tests (recommended)
./tests/scripts/run-all-tests.sh

# Run specific test types
./tests/scripts/run-all-tests.sh --unit-only
./tests/scripts/run-all-tests.sh --operation-only
./tests/scripts/run-all-tests.sh --no-operation
```

## Features Tested

### Core Go Components (integration_core_test.go)

- Connection pool lifecycle and statistics
- Circuit breaker protection and state transitions  
- PooledLDAPClient wrapper functionality
- Prometheus exporter Go integration
- DC (Domain Component) filtering logic
- Concurrent metric collection (Go routines)
- Metric consistency and stability

### Dynamic Operation Metrics

- LDAP Monitor backend configuration via LDIF files
- Real LDAP traffic generation and metrics validation
- Operation metrics collection (Bind, Search, Modify, etc.)
- Before/after metrics comparison
- Exporter integration with live LDAP operations

### Error Handling & Resilience

- LDAP server unavailability scenarios
- Invalid credentials and configuration handling
- Network connectivity issues simulation
- Circuit breaker state transitions

## Environment Requirements

### For Go Tests

- Go 1.21+
- Access to LDAP server (for integration tests)

### For Operation Tests

- Linux environment (native or WSL on Windows)
- LDAP client tools: `ldapsearch`, `ldapmodify`
- Network tools: `curl`
- Running LDAP server with Monitor backend capability

### Installation (Ubuntu/Debian)

```bash
sudo apt-get update
sudo apt-get install -y ldap-utils curl golang-go
```

## Configuration

All tests can be configured using environment variables:

### LDAP Server Configuration

- `LDAP_HOST`: LDAP server host (default: localhost)
- `LDAP_PORT`: LDAP server port (default: 389)
- `ADMIN_PASSWORD`: LDAP admin password (default: adminpassword)
- `CONFIG_PASSWORD`: LDAP config admin password (default: configpassword)

### Test Behavior

- `EXPORTER_PORT`: Port for test exporter instance (default: 9360)
- `TEST_TRAFFIC_OPERATIONS`: Number of LDAP operations to generate (default: 30)
- `TEST_TIMEOUT`: Test timeout in seconds (default: 300)
- `BUILD_BINARY`: Whether to rebuild exporter binary (default: true)

### Test Selection

- `RUN_UNIT_TESTS`: Enable unit tests (default: true)
- `RUN_INTEGRATION_TESTS`: Enable integration tests (default: true)  
- `RUN_OPERATION_TESTS`: Enable operation metrics tests (default: true)

## CI/CD Integration

Tests are automatically executed in GitHub Actions workflow (`.github/workflows/test.yml`):

### Automated Test Pipeline

1. **Unit Tests**:
   - Fast tests without external dependencies
   - `go test -race -coverprofile=coverage.out ./...`

2. **Integration Tests**:
   - Uses GitHub Actions `services:` with Bitnami OpenLDAP 2.6.10
   - Runs `tests/integration_core_test.go` with Go integration tags
   - Environment setup via `.github/actions/setup-ldap-test`

3. **Operation Tests**:
   - Installs LDAP tools (`ldap-utils`)
   - Executes `tests/scripts/run-operation-metrics-test.sh`
   - Validates real LDAP traffic and metrics collection

4. **Static Analysis**:
   - Code quality and security checks with `golangci-lint`
   - Vulnerability scanning with `gosec`

### Key Differences from Local Development

- **CI/CD**: Uses GitHub Actions services (no docker-compose needed)
- **Local**: Can use `run-all-tests.sh` which includes Docker environment setup
- **Both**: Share the same core test files and LDIF configurations

## Local Development

### Option 1: Using run-all-tests.sh (Recommended)

For comprehensive local testing including Docker environment setup:

```bash
# Run all tests (builds Docker environment automatically)
./tests/scripts/run-all-tests.sh

# Run specific test types
./tests/scripts/run-all-tests.sh --unit-only
./tests/scripts/run-all-tests.sh --operation-only
```

### Option 2: Manual testing with external LDAP

If you have an existing LDAP server:

```bash
# Unit tests only
go test -v ./...

# Integration tests with custom LDAP
TEST_LDAP_URL=ldap://localhost:389 \
TEST_LDAP_USERNAME="cn=adminconfig,cn=config" \
TEST_LDAP_PASSWORD=configpassword \
go test -tags=integration -v ./tests/...

# Operation metrics tests
LDAP_HOST=localhost LDAP_PORT=389 \
./tests/scripts/run-operation-metrics-test.sh
```

## Troubleshooting

### Common Issues

1. **LDAP tools not found**: Install `ldap-utils` package
2. **Connection refused**: Verify LDAP server is running and accessible
3. **Monitor backend not accessible**: LDAP server may not have Monitor backend enabled
4. **Permission errors**: Ensure LDAP credentials have sufficient privileges

### Debug Mode

Enable debug output for detailed test information:

```bash
LOG_LEVEL=DEBUG ./tests/scripts/run-operation-metrics-test.sh
```

### Test Isolation

Each test run uses unique ports and temporary files to avoid conflicts:

- Exporter instances use different ports (9360+)
- Temporary files use test-specific names
- LDAP data is properly cleaned between runs
