#!/bin/bash

# Complete Test Suite Runner
# Runs all types of tests: unit, integration, and operation metrics
# Compatible with Linux environments (local, CI/CD, Docker)

set -euo pipefail

# Script directory detection
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Configuration
RUN_UNIT_TESTS=${RUN_UNIT_TESTS:-true}
RUN_INTEGRATION_TESTS=${RUN_INTEGRATION_TESTS:-true}
RUN_OPERATION_TESTS=${RUN_OPERATION_TESTS:-true}
RUN_STATIC_ANALYSIS=${RUN_STATIC_ANALYSIS:-false}

# Test configuration
LDAP_HOST=${LDAP_HOST:-localhost}
LDAP_PORT=${LDAP_PORT:-389}
ADMIN_PASSWORD=${ADMIN_PASSWORD:-adminpassword}
CONFIG_PASSWORD=${CONFIG_PASSWORD:-configpassword}
EXPORTER_PORT=${EXPORTER_PORT:-9370}

# Colors for output
if [[ -t 1 ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    BOLD='\033[1m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    BOLD=''
    NC=''
fi

log_info() {
    echo -e "${BLUE}[RUNNER]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[RUNNER]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[RUNNER]${NC} $1"
}

log_error() {
    echo -e "${RED}[RUNNER]${NC} $1"
}

log_section() {
    echo -e "${BOLD}${BLUE}"
    echo "========================================"
    echo "$1"
    echo "========================================"
    echo -e "${NC}"
}

# Check if running in CI
is_ci() {
    [[ "${CI:-false}" == "true" ]] || [[ -n "${GITHUB_ACTIONS:-}" ]] || [[ -n "${GITLAB_CI:-}" ]]
}

# Check prerequisites for operation tests
check_ldap_tools() {
    local missing_tools=()
    
    for tool in ldapsearch ldapmodify curl; do
        if ! command -v "$tool" >/dev/null 2>&1; then
            missing_tools+=("$tool")
        fi
    done
    
    if [[ ${#missing_tools[@]} -gt 0 ]]; then
        log_warning "Missing LDAP tools: ${missing_tools[*]}"
        
        if is_ci; then
            log_info "Installing LDAP tools in CI environment..."
            sudo apt-get update -qq
            sudo apt-get install -y ldap-utils curl
            return 0
        else
            log_error "Please install missing tools: ${missing_tools[*]}"
            log_error "Ubuntu/Debian: sudo apt-get install ldap-utils curl"
            log_error "RHEL/CentOS: sudo yum install openldap-clients curl"
            return 1
        fi
    fi
    
    return 0
}

# Run unit tests
run_unit_tests() {
    log_section "Running Unit Tests"
    
    cd "$PROJECT_ROOT"
    
    log_info "Running unit tests with coverage..."
    if go test -race -coverprofile=coverage.out -covermode=atomic ./...; then
        log_success "Unit tests passed"
        
        # Show coverage
        local coverage=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')
        log_info "Test coverage: $coverage%"
        
        return 0
    else
        log_error "Unit tests failed"
        return 1
    fi
}

# Run integration tests (Go-based)
run_integration_tests() {
    log_section "Running Integration Tests"
    
    cd "$PROJECT_ROOT"
    
    # Set up environment for integration tests
    export TEST_LDAP_URL="ldap://${LDAP_HOST}:${LDAP_PORT}"
    export TEST_LDAP_USERNAME="cn=adminconfig,cn=config"
    export TEST_LDAP_PASSWORD="$CONFIG_PASSWORD"
    
    log_info "Running Go integration tests..."
    log_info "LDAP URL: $TEST_LDAP_URL"
    
    if go test -tags=integration -v ./tests/...; then
        log_success "Integration tests passed"
        return 0
    else
        log_error "Integration tests failed"
        return 1
    fi
}

# Run operation metrics tests (bash-based)
run_operation_tests() {
    log_section "Running Operation Metrics Tests"
    
    # Check prerequisites
    if ! check_ldap_tools; then
        log_error "Cannot run operation tests without LDAP tools"
        return 1
    fi
    
    local operation_script="$SCRIPT_DIR/run-operation-metrics-test.sh"
    
    if [[ ! -f "$operation_script" ]]; then
        log_error "Operation test script not found: $operation_script"
        return 1
    fi
    
    chmod +x "$operation_script"
    
    # Set up environment
    export LDAP_HOST
    export LDAP_PORT
    export ADMIN_PASSWORD
    export CONFIG_PASSWORD
    export EXPORTER_PORT
    export TEST_TRAFFIC_OPERATIONS=20
    export TEST_TIMEOUT=120
    
    log_info "Running operation metrics tests..."
    log_info "LDAP: ${LDAP_HOST}:${LDAP_PORT}"
    log_info "Exporter port: $EXPORTER_PORT"
    
    if "$operation_script" --ldap-host "$LDAP_HOST" --ldap-port "$LDAP_PORT" --exporter-port "$EXPORTER_PORT" --traffic 20; then
        log_success "Operation metrics tests passed"
        return 0
    else
        log_error "Operation metrics tests failed"
        return 1
    fi
}

# Run static analysis
run_static_analysis() {
    log_section "Running Static Analysis"
    
    cd "$PROJECT_ROOT"
    
    log_info "Checking code formatting..."
    if [[ "$(gofmt -s -l . | wc -l)" -gt 0 ]]; then
        log_error "Code is not properly formatted:"
        gofmt -s -l .
        return 1
    fi
    
    log_info "Running go vet..."
    if ! go vet ./...; then
        log_error "go vet found issues"
        return 1
    fi
    
    log_success "Static analysis passed"
    return 0
}

# Test LDAP connectivity before running tests
test_ldap_connectivity() {
    log_info "Testing LDAP connectivity..."
    
    if ! command -v ldapsearch >/dev/null 2>&1; then
        log_warning "ldapsearch not available, skipping connectivity test"
        return 0
    fi
    
    local ldap_url="ldap://${LDAP_HOST}:${LDAP_PORT}"
    
    # Test with config credentials (most permissive)
    if ldapsearch -x -H "$ldap_url" -D "cn=adminconfig,cn=config" -w "$CONFIG_PASSWORD" \
        -b "cn=config" -s base "(objectClass=*)" cn >/dev/null 2>&1; then
        log_success "LDAP connectivity OK"
        return 0
    else
        log_warning "LDAP connectivity test failed"
        log_warning "This may be normal if LDAP server is not available"
        return 1
    fi
}

# Show test summary
show_summary() {
    log_section "Test Summary"
    
    local total_tests=0
    local passed_tests=0
    local failed_tests=0
    local skipped_tests=0
    
    if [[ "$RUN_UNIT_TESTS" == "true" ]]; then
        ((total_tests++))
        if [[ "${unit_test_result:-1}" -eq 0 ]]; then
            ((passed_tests++))
            log_success "Unit Tests: PASSED"
        else
            ((failed_tests++))
            log_error "Unit Tests: FAILED"
        fi
    else
        ((skipped_tests++))
        log_info "Unit Tests: SKIPPED"
    fi
    
    if [[ "$RUN_INTEGRATION_TESTS" == "true" ]]; then
        ((total_tests++))
        if [[ "${integration_test_result:-1}" -eq 0 ]]; then
            ((passed_tests++))
            log_success "Integration Tests: PASSED"
        else
            ((failed_tests++))
            log_error "Integration Tests: FAILED"
        fi
    else
        ((skipped_tests++))
        log_info "Integration Tests: SKIPPED"
    fi
    
    if [[ "$RUN_OPERATION_TESTS" == "true" ]]; then
        ((total_tests++))
        if [[ "${operation_test_result:-1}" -eq 0 ]]; then
            ((passed_tests++))
            log_success "Operation Tests: PASSED"
        else
            ((failed_tests++))
            log_error "Operation Tests: FAILED"
        fi
    else
        ((skipped_tests++))
        log_info "Operation Tests: SKIPPED"
    fi
    
    if [[ "$RUN_STATIC_ANALYSIS" == "true" ]]; then
        ((total_tests++))
        if [[ "${static_analysis_result:-1}" -eq 0 ]]; then
            ((passed_tests++))
            log_success "Static Analysis: PASSED"
        else
            ((failed_tests++))
            log_error "Static Analysis: FAILED"
        fi
    else
        ((skipped_tests++))
        log_info "Static Analysis: SKIPPED"
    fi
    
    echo ""
    log_info "Total: $total_tests, Passed: $passed_tests, Failed: $failed_tests, Skipped: $skipped_tests"
    
    if [[ $failed_tests -eq 0 ]]; then
        log_success "🎉 All tests passed!"
        return 0
    else
        log_error "$failed_tests test suite(s) failed"
        return 1
    fi
}

# Main execution
main() {
    echo "========================================"
    echo "OpenLDAP Exporter - Complete Test Suite"
    echo "========================================"
    echo "Project: $(basename "$PROJECT_ROOT")"
    echo "LDAP: ${LDAP_HOST}:${LDAP_PORT}"
    echo "Exporter Port: $EXPORTER_PORT"
    echo "CI Environment: $(is_ci && echo "Yes" || echo "No")"
    echo "========================================"
    echo ""
    
    # Test LDAP connectivity (non-blocking)
    test_ldap_connectivity || true
    
    local start_time=$(date +%s)
    
    # Run requested test suites
    local unit_test_result=0
    local integration_test_result=0
    local operation_test_result=0
    local static_analysis_result=0
    
    if [[ "$RUN_UNIT_TESTS" == "true" ]]; then
        run_unit_tests || unit_test_result=$?
    fi
    
    if [[ "$RUN_INTEGRATION_TESTS" == "true" ]]; then
        run_integration_tests || integration_test_result=$?
    fi
    
    if [[ "$RUN_OPERATION_TESTS" == "true" ]]; then
        run_operation_tests || operation_test_result=$?
    fi
    
    if [[ "$RUN_STATIC_ANALYSIS" == "true" ]]; then
        run_static_analysis || static_analysis_result=$?
    fi
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    echo ""
    log_info "Test execution completed in ${duration}s"
    
    # Show summary and determine exit code
    if show_summary; then
        exit 0
    else
        exit 1
    fi
}

# Show usage
show_usage() {
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  -h, --help              Show this help"
    echo "  --unit-only            Run only unit tests"
    echo "  --integration-only     Run only integration tests"  
    echo "  --operation-only       Run only operation metrics tests"
    echo "  --no-unit             Skip unit tests"
    echo "  --no-integration      Skip integration tests"
    echo "  --no-operation        Skip operation tests"
    echo "  --with-static         Include static analysis"
    echo "  --ldap-host HOST      LDAP server host"
    echo "  --ldap-port PORT      LDAP server port"
    echo ""
    echo "Environment variables:"
    echo "  LDAP_HOST, LDAP_PORT, ADMIN_PASSWORD, CONFIG_PASSWORD"
    echo "  RUN_UNIT_TESTS, RUN_INTEGRATION_TESTS, RUN_OPERATION_TESTS"
    echo ""
    echo "Examples:"
    echo "  $0                                  # Run all tests"
    echo "  $0 --unit-only                    # Only unit tests"
    echo "  $0 --no-operation                 # Skip operation tests"
    echo "  $0 --ldap-port 1389 --with-static # Custom port + static analysis"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_usage
            exit 0
            ;;
        --unit-only)
            RUN_UNIT_TESTS=true
            RUN_INTEGRATION_TESTS=false
            RUN_OPERATION_TESTS=false
            shift
            ;;
        --integration-only)
            RUN_UNIT_TESTS=false
            RUN_INTEGRATION_TESTS=true
            RUN_OPERATION_TESTS=false
            shift
            ;;
        --operation-only)
            RUN_UNIT_TESTS=false
            RUN_INTEGRATION_TESTS=false
            RUN_OPERATION_TESTS=true
            shift
            ;;
        --no-unit)
            RUN_UNIT_TESTS=false
            shift
            ;;
        --no-integration)
            RUN_INTEGRATION_TESTS=false
            shift
            ;;
        --no-operation)
            RUN_OPERATION_TESTS=false
            shift
            ;;
        --with-static)
            RUN_STATIC_ANALYSIS=true
            shift
            ;;
        --ldap-host)
            LDAP_HOST="$2"
            shift 2
            ;;
        --ldap-port)
            LDAP_PORT="$2"
            shift 2
            ;;
        *)
            log_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Execute main function
main "$@"