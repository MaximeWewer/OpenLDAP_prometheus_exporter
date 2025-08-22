# OpenLDAP Prometheus Exporter Makefile

.PHONY: help build test lint format clean pre-check install-tools

# Default target
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development commands
pre-check: ## Run pre-check validations (same as CI)
	@echo "Running pre-check validations..."
	@echo ""
	@echo "1/6 Checking Go modules..."
	@go mod verify > /dev/null 2>&1 && echo "✓ Go modules verified" || (echo "✗ Go modules verification failed" && exit 1)
	@echo "2/6 Checking code formatting..."
	@if [ "$$(gofmt -s -l . | wc -l)" -gt 0 ]; then \
		echo "✗ Code formatting issues found:"; \
		gofmt -s -l .; \
		echo "Run 'make format' to fix"; \
		exit 1; \
	else \
		echo "✓ Code formatting is correct"; \
	fi
	@echo "3/6 Running go vet..."
	@go vet ./... > /dev/null 2>&1 && echo "✓ Go vet passed" || (echo "✗ Go vet failed" && exit 1)
	@echo "4/6 Checking for ineffective assignments..."
	@which ineffassign > /dev/null 2>&1 || go install github.com/gordonklaus/ineffassign@latest
	@ineffassign ./... > /dev/null 2>&1 && echo "✓ No ineffective assignments" || (echo "✗ Ineffective assignments found" && exit 1)
	@echo "5/6 Running golangci-lint..."
	@which golangci-lint > /dev/null 2>&1 || (echo "Install golangci-lint from https://golangci-lint.run/usage/install/" && exit 1)
	@golangci-lint run --timeout=3m > /dev/null 2>&1 && echo "✓ Linting passed" || (echo "✗ Linting failed - run 'make lint' for details" && exit 1)
	@echo "6/6 Checking test compilation..."
	@go test -c ./... > /dev/null 2>&1 && echo "✓ All tests compile" || (echo "✗ Test compilation failed" && exit 1)
	@echo ""
	@echo "🎉 All pre-checks passed! Ready to push."

format: ## Format Go code
	@echo "Formatting code..."
	@gofmt -s -w .
	@go mod tidy
	@echo "Code formatted"

lint: ## Run linting tools
	@echo "Running linters..."
	@golangci-lint run
	@echo "Linting completed"

lint-fix: ## Run linting tools with auto-fix
	@echo "Running linters with auto-fix..."
	@golangci-lint run --fix
	@echo "Linting with auto-fix completed"

vet: ## Run go vet
	@echo "Running go vet..."
	@go vet ./...
	@echo "go vet completed"

# Testing commands
test: ## Run unit tests
	@echo "Running unit tests..."
	@go test -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out | tail -1

test-integration: ## Run integration tests
	@echo "Running integration tests..."
	@go test -tags=integration -v ./tests/...

test-race: ## Run tests with race detector (requires CGO)
	@echo "Running tests with race detector..."
	@CGO_ENABLED=1 go test -race -v ./...

# Build commands
build: ## Build the application
	@echo "Building application..."
	@go build -o bin/openldap-exporter ./cmd/main.go
	@echo "Build completed: bin/openldap-exporter"

build-all: ## Build for all platforms
	@echo "Building for all platforms..."
	@mkdir -p bin
	@GOOS=linux GOARCH=amd64 go build -o bin/openldap-exporter-linux-amd64 ./cmd/main.go
	@GOOS=windows GOARCH=amd64 go build -o bin/openldap-exporter-windows-amd64.exe ./cmd/main.go
	@GOOS=darwin GOARCH=amd64 go build -o bin/openldap-exporter-darwin-amd64 ./cmd/main.go
	@GOOS=darwin GOARCH=arm64 go build -o bin/openldap-exporter-darwin-arm64 ./cmd/main.go
	@echo "Multi-platform build completed"

# Installation commands
install-tools: ## Install development tools
	@echo "Installing development tools..."
	@echo "Installing golangci-lint..."
	@echo "Please install from: https://golangci-lint.run/usage/install/"
	@go install github.com/kisielk/errcheck@latest
	@go install github.com/gordonklaus/ineffassign@latest
	@echo "Development tools installed"

# Maintenance commands
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@rm -f coverage.out
	@go clean
	@echo "Clean completed"

tidy: ## Tidy go modules
	@echo "Tidying modules..."
	@go mod tidy
	@echo "Modules tidied"

# Docker commands
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	@docker build -t openldap-exporter .
	@echo "Docker image built"

# CI simulation
ci-local: pre-check test test-coverage ## Simulate CI pipeline locally
	@echo "Local CI simulation completed successfully"

# Development workflow
dev-setup: install-tools ## Set up development environment
	@echo "Development environment setup completed"
	@echo ""
	@echo "Useful commands:"
	@echo "  make pre-check     # Run all pre-commit checks"
	@echo "  make test          # Run tests"
	@echo "  make format        # Format code"
	@echo "  make lint-fix      # Fix linting issues"

# Git hooks setup
setup-hooks: ## Setup git pre-commit hooks
	@echo "Setting up git hooks..."
	@echo '#!/bin/bash' > .git/hooks/pre-commit
	@echo 'make pre-check' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Git pre-commit hook installed"
	@echo "Pre-commit will run automatically before each commit"