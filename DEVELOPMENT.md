# Development Guide

This guide explains how to develop and contribute to the OpenLDAP Prometheus Exporter project.

## Prerequisites

- Go 1.23+
- Git
- Make (optional but recommended)

## Development environment setup

### Install development tools

```bash
# With Make
make install-tools

# Or manually
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/kisielk/errcheck@latest
go install github.com/gordonklaus/ineffassign@latest
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
- **Integration tests**: Full LDAP service testing
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

# Race detector tests
make test-race

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
- **Integration tests**: `tests/` directory with build tag `integration`
- **Test helpers**: Setup/cleanup functions for consistent test environments
- **Table-driven tests**: Used for comprehensive scenario coverage
- **Mock objects**: Used to isolate units under test

Key test packages:

- `cmd/main_test.go`: HTTP handlers and main function tests
- `pkg/exporter/*_test.go`: Core LDAP exporter functionality
- `pkg/security/*_test.go`: Security validation and rate limiting
- `pkg/monitoring/*_test.go`: Internal monitoring and metrics

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
