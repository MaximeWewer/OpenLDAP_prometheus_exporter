#!/bin/bash

# Complete Operation Metrics Test Suite
# This script runs comprehensive tests for LDAP operation metrics collection
# Compatible with Linux environments (CI/CD, Docker, local development)

set -euo pipefail

# Script directory detection (works with both relative and absolute paths)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TESTS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Configuration with environment variable support
LDAP_HOST=${LDAP_HOST:-localhost}
LDAP_PORT=${LDAP_PORT:-389}
LDAP_URL="ldap://${LDAP_HOST}:${LDAP_PORT}"
ADMIN_DN="cn=admin,dc=example,dc=org"
ADMIN_PASSWORD=${ADMIN_PASSWORD:-adminpassword}
CONFIG_DN="cn=adminconfig,cn=config"
CONFIG_PASSWORD=${CONFIG_PASSWORD:-configpassword}
EXPORTER_PORT=${EXPORTER_PORT:-9360}
BUILD_BINARY=${BUILD_BINARY:-true}

# Test configuration
TEST_TRAFFIC_OPERATIONS=${TEST_TRAFFIC_OPERATIONS:-30}
TEST_WAIT_TIME=${TEST_WAIT_TIME:-3}
TEST_TIMEOUT=${TEST_TIMEOUT:-300}

# Colors for output (with fallback for non-interactive environments)
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

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "${BOLD}${BLUE}=== $1 ===${NC}"
}

# Cleanup function
cleanup() {
    log_info "Cleaning up test environment..."
    
    # Stop exporter if running
    if [[ -f "/tmp/exporter_test.pid" ]]; then
        local exporter_pid=$(cat /tmp/exporter_test.pid 2>/dev/null || echo "")
        if [[ -n "$exporter_pid" ]] && kill -0 "$exporter_pid" 2>/dev/null; then
            log_info "Stopping exporter (PID $exporter_pid)..."
            kill "$exporter_pid" 2>/dev/null || true
            sleep 1
            kill -9 "$exporter_pid" 2>/dev/null || true
        fi
        rm -f /tmp/exporter_test.pid
    fi
    
    # Clean up temporary files
    rm -f /tmp/exporter_test.log
    rm -f /tmp/ldap_baseline.txt
    rm -f /tmp/ldap_final.txt
    rm -f /tmp/metrics_baseline.txt
    rm -f /tmp/metrics_final.txt
}

# Set up cleanup trap
trap cleanup EXIT INT TERM

# Check prerequisites
check_prerequisites() {
    log_step "Checking Prerequisites"
    
    local missing_tools=()
    
    # Check for required tools
    for tool in ldapsearch ldapmodify curl go; do
        if ! command -v "$tool" >/dev/null 2>&1; then
            missing_tools+=("$tool")
        fi
    done
    
    if [[ ${#missing_tools[@]} -gt 0 ]]; then
        log_error "Missing required tools: ${missing_tools[*]}"
        log_error "Please install: sudo apt-get update && sudo apt-get install -y ldap-utils curl golang-go"
        return 1
    fi
    
    log_success "All required tools are available"
    
    # Check Go version
    local go_version=$(go version | grep -o 'go[0-9.]*' | head -1)
    log_info "Go version: $go_version"
    
    return 0
}

# Test LDAP connectivity
test_ldap_connectivity() {
    log_step "Testing LDAP Connectivity"
    
    local connectivity_ok=false
    
    # Test with admin credentials
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" \
        -b "dc=example,dc=org" -s base "(objectClass=*)" dc >/dev/null 2>&1; then
        log_success "LDAP connectivity OK with admin credentials"
        connectivity_ok=true
    fi
    
    # Test with config credentials
    if ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
        -b "cn=config" -s base "(objectClass=*)" cn >/dev/null 2>&1; then
        log_success "LDAP connectivity OK with config credentials"
        connectivity_ok=true
    fi
    
    if [[ "$connectivity_ok" != "true" ]]; then
        log_error "Cannot connect to LDAP server at $LDAP_URL"
        log_error "Please ensure LDAP server is running and credentials are correct"
        return 1
    fi
    
    return 0
}

# Configure Monitor backend
configure_monitor_backend() {
    log_step "Configuring Monitor Backend"
    
    local ldif_dir="$TESTS_DIR/data/ldap-schemas"
    
    if [[ ! -d "$ldif_dir" ]]; then
        log_error "LDIF directory not found: $ldif_dir"
        return 1
    fi
    
    local ldif_files=(
        "$ldif_dir/monitor-acl.ldif"
        "$ldif_dir/enable-monitor.ldif"
        "$ldif_dir/base-monitoring.ldif"
    )
    
    local applied_count=0
    
    for ldif_file in "${ldif_files[@]}"; do
        if [[ -f "$ldif_file" ]]; then
            local filename=$(basename "$ldif_file")
            log_info "Applying $filename..."
            
            if ldapmodify -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
                -f "$ldif_file" >/dev/null 2>&1; then
                log_success "Applied $filename"
                ((applied_count++))
            else
                log_warning "$filename may already be applied or failed"
            fi
        else
            log_warning "LDIF file not found: $ldif_file"
        fi
    done
    
    if [[ $applied_count -eq 0 ]]; then
        log_warning "No LDIF files were successfully applied"
    else
        log_success "Applied $applied_count Monitor configuration files"
        sleep "$TEST_WAIT_TIME"  # Wait for changes to take effect
    fi
    
    return 0
}

# Test Monitor backend access
test_monitor_backend() {
    log_step "Testing Monitor Backend Access"
    
    # Test Monitor operations access
    if ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
        -b "cn=Operations,cn=Monitor" "(objectClass=monitorOperation)" cn >/dev/null 2>&1; then
        
        log_success "Monitor Operations backend is accessible"
        
        # List available operations
        local operations=$(ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
            -b "cn=Operations,cn=Monitor" "(objectClass=monitorOperation)" cn 2>/dev/null | \
            grep "^cn:" | cut -d' ' -f2 | sort)
        
        if [[ -n "$operations" ]]; then
            log_info "Available operations:"
            echo "$operations" | while read -r op; do
                log_info "  - $op"
            done
        fi
        
        return 0
    else
        log_error "Monitor Operations backend is not accessible"
        log_error "This means operation metrics will not be available"
        return 1
    fi
}

# Get operation counts from LDAP Monitor
get_ldap_operation_counts() {
    local output_file="${1:-/tmp/ldap_baseline.txt}"
    
    log_info "Getting LDAP operation counts..."
    
    if ! ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
        -b "cn=Operations,cn=Monitor" "(objectClass=monitorOperation)" \
        cn monitorOpInitiated monitorOpCompleted 2>/dev/null > "$output_file"; then
        log_warning "Could not retrieve operation counts from LDAP"
        return 1
    fi
    
    # Parse and display key operations
    local operations=("Bind" "Search" "Modify" "Add" "Delete")
    
    for op in "${operations[@]}"; do
        local initiated=$(grep -A 10 "cn=$op" "$output_file" | grep "monitorOpInitiated:" | awk '{print $2}' | head -1)
        local completed=$(grep -A 10 "cn=$op" "$output_file" | grep "monitorOpCompleted:" | awk '{print $2}' | head -1)
        
        if [[ -n "$initiated" && -n "$completed" ]]; then
            log_info "  $op: $initiated initiated, $completed completed"
        fi
    done
    
    return 0
}

# Build exporter
build_exporter() {
    log_step "Building Exporter"
    
    cd "$PROJECT_ROOT"
    
    local binary_name="openldap-exporter-test"
    
    if [[ "$BUILD_BINARY" == "true" ]] || [[ ! -f "$binary_name" ]]; then
        log_info "Building exporter binary..."
        
        if CGO_ENABLED=0 go build -o "$binary_name" ./cmd/; then
            log_success "Exporter built successfully: $binary_name"
        else
            log_error "Failed to build exporter"
            return 1
        fi
    else
        log_info "Using existing binary: $binary_name"
    fi
    
    return 0
}

# Start exporter
start_exporter() {
    log_step "Starting Exporter"
    
    cd "$PROJECT_ROOT"
    
    local binary_name="openldap-exporter-test"
    
    if [[ ! -f "$binary_name" ]]; then
        log_error "Exporter binary not found: $binary_name"
        return 1
    fi
    
    # Start exporter in background
    log_info "Starting exporter on port $EXPORTER_PORT..."
    
    LDAP_URL="$LDAP_URL" \
    LDAP_SERVER_NAME="openldap-test" \
    LDAP_USERNAME="$CONFIG_DN" \
    LDAP_PASSWORD="$CONFIG_PASSWORD" \
    LDAP_TLS=false \
    LDAP_TIMEOUT=10 \
    LOG_LEVEL=INFO \
    timeout "$TEST_TIMEOUT" "./$binary_name" -web.listen-address=":$EXPORTER_PORT" \
    > /tmp/exporter_test.log 2>&1 &
    
    local exporter_pid=$!
    echo "$exporter_pid" > /tmp/exporter_test.pid
    
    # Wait for exporter to start
    log_info "Waiting for exporter to be ready..."
    local attempts=0
    local max_attempts=15
    
    while [[ $attempts -lt $max_attempts ]]; do
        if curl -s --connect-timeout 2 "http://localhost:$EXPORTER_PORT/health" >/dev/null 2>&1; then
            log_success "Exporter is ready and accessible"
            return 0
        fi
        
        sleep 2
        ((attempts++))
    done
    
    log_error "Exporter failed to start or is not accessible"
    
    # Show logs if exporter failed
    if [[ -f /tmp/exporter_test.log ]]; then
        log_error "Exporter logs:"
        tail -20 /tmp/exporter_test.log | while read -r line; do
            log_error "  $line"
        done
    fi
    
    return 1
}

# Generate LDAP traffic
generate_ldap_traffic() {
    log_step "Generating LDAP Traffic"
    
    local operations_count="$TEST_TRAFFIC_OPERATIONS"
    log_info "Generating $operations_count LDAP operations..."
    
    local success_count=0
    local error_count=0
    
    for ((i=1; i<=operations_count; i++)); do
        local operation_type=$((i % 4))
        local success=false
        
        case $operation_type in
            0)  # Search in main base
                if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" \
                    -b "dc=example,dc=org" "(objectClass=*)" cn >/dev/null 2>&1; then
                    success=true
                fi
                ;;
            1)  # Search in config
                if ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
                    -b "cn=config" "(objectClass=*)" cn >/dev/null 2>&1; then
                    success=true
                fi
                ;;
            2)  # Search in Monitor
                if ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
                    -b "cn=Monitor" "(objectClass=*)" cn >/dev/null 2>&1; then
                    success=true
                fi
                ;;
            3)  # Search operations specifically
                if ldapsearch -x -H "$LDAP_URL" -D "$CONFIG_DN" -w "$CONFIG_PASSWORD" \
                    -b "cn=Operations,cn=Monitor" "(objectClass=monitorOperation)" cn >/dev/null 2>&1; then
                    success=true
                fi
                ;;
        esac
        
        if [[ "$success" == "true" ]]; then
            ((success_count++))
            echo -n "."
        else
            ((error_count++))
            echo -n "x"
        fi
        
        # Small delay to spread operations
        sleep 0.1
    done
    
    echo ""
    log_success "Generated $success_count successful operations (${error_count} errors)"
    
    # Wait for metrics to be updated
    log_info "Waiting ${TEST_WAIT_TIME}s for metrics to update..."
    sleep "$TEST_WAIT_TIME"
    
    return 0
}

# Test exporter metrics
test_exporter_metrics() {
    log_step "Testing Exporter Metrics Collection"
    
    local metrics_url="http://localhost:$EXPORTER_PORT/metrics"
    local metrics_file="/tmp/metrics_final.txt"
    
    log_info "Fetching metrics from exporter..."
    
    if ! curl -s --connect-timeout 10 --max-time 30 "$metrics_url" > "$metrics_file" 2>/dev/null; then
        log_error "Failed to fetch metrics from $metrics_url"
        return 1
    fi
    
    # Check file size
    local file_size=$(wc -c < "$metrics_file")
    if [[ $file_size -lt 100 ]]; then
        log_error "Metrics response is too small ($file_size bytes)"
        return 1
    fi
    
    log_info "Retrieved metrics ($file_size bytes)"
    
    # Check for operation metrics
    local ops_initiated=$(grep -c "openldap_operations_initiated_delta" "$metrics_file" 2>/dev/null || echo "0")
    local ops_completed=$(grep -c "openldap_operations_completed_delta" "$metrics_file" 2>/dev/null || echo "0")
    
    log_info "Found metrics:"
    log_info "  - Operations initiated entries: $ops_initiated"
    log_info "  - Operations completed entries: $ops_completed"
    
    if [[ $ops_initiated -gt 0 ]] || [[ $ops_completed -gt 0 ]]; then
        log_success "Operation metrics are being collected!"
        
        # Show sample metrics
        log_info "Sample operation metrics:"
        grep "openldap_operations_initiated_delta" "$metrics_file" | head -5 | while read -r line; do
            log_info "  $line"
        done
        
        # Parse specific operations
        local bind_ops=$(grep 'openldap_operations_initiated_delta.*operation="bind"' "$metrics_file" | grep -o '[0-9]\+$' | head -1)
        local search_ops=$(grep 'openldap_operations_initiated_delta.*operation="search"' "$metrics_file" | grep -o '[0-9]\+$' | head -1)
        
        if [[ -n "$bind_ops" ]]; then
            log_info "  - Bind operations: $bind_ops"
        fi
        if [[ -n "$search_ops" ]]; then
            log_info "  - Search operations: $search_ops"
        fi
        
        return 0
    else
        log_warning "No operation metrics found"
        
        # Show what metrics are available
        local total_metrics=$(grep -c "^# HELP" "$metrics_file" || echo "0")
        log_info "Total available metric families: $total_metrics"
        
        if [[ $total_metrics -gt 0 ]]; then
            log_info "Available metrics (first 10):"
            grep "^# HELP" "$metrics_file" | head -10 | while read -r line; do
                local metric_name=$(echo "$line" | awk '{print $3}')
                log_info "  - $metric_name"
            done
        fi
        
        return 1
    fi
}

# Compare before/after metrics
compare_metrics() {
    log_step "Comparing Before/After Metrics"
    
    if [[ -f /tmp/ldap_baseline.txt && -f /tmp/ldap_final.txt ]]; then
        log_info "Comparing LDAP Monitor operation counts..."
        
        # Compare key operations
        local operations=("Bind" "Search" "Modify")
        
        for op in "${operations[@]}"; do
            local baseline_initiated=$(grep -A 10 "cn=$op" /tmp/ldap_baseline.txt | grep "monitorOpInitiated:" | awk '{print $2}' | head -1)
            local final_initiated=$(grep -A 10 "cn=$op" /tmp/ldap_final.txt | grep "monitorOpInitiated:" | awk '{print $2}' | head -1)
            
            if [[ -n "$baseline_initiated" && -n "$final_initiated" ]]; then
                local diff=$((final_initiated - baseline_initiated))
                if [[ $diff -gt 0 ]]; then
                    log_success "$op operations increased by $diff"
                else
                    log_info "$op operations unchanged"
                fi
            fi
        done
    else
        log_warning "Could not compare before/after LDAP counts (missing baseline files)"
    fi
    
    return 0
}

# Generate test report
generate_report() {
    log_step "Test Report"
    
    local report_file="/tmp/operation_metrics_test_report.txt"
    
    {
        echo "========================================"
        echo "LDAP Operation Metrics Test Report"
        echo "========================================"
        echo "Date: $(date)"
        echo "LDAP URL: $LDAP_URL"
        echo "Exporter Port: $EXPORTER_PORT"
        echo "Traffic Generated: $TEST_TRAFFIC_OPERATIONS operations"
        echo ""
        
        if [[ -f /tmp/metrics_final.txt ]]; then
            echo "=== EXPORTER METRICS SUMMARY ==="
            
            # Basic stats
            local total_metrics=$(grep -c "^# HELP" /tmp/metrics_final.txt || echo "0")
            echo "Total metric families: $total_metrics"
            
            # Operation metrics
            local ops_initiated=$(grep -c "openldap_operations_initiated_delta" /tmp/metrics_final.txt || echo "0")
            local ops_completed=$(grep -c "openldap_operations_completed_delta" /tmp/metrics_final.txt || echo "0")
            echo "Operation initiated metrics: $ops_initiated"
            echo "Operation completed metrics: $ops_completed"
            
            # Server status
            local up_status=$(grep "openldap_up " /tmp/metrics_final.txt | grep -o '[0-9]$' | head -1)
            echo "Server status: ${up_status:-unknown}"
            
            echo ""
            echo "=== OPERATION COUNTS ==="
            grep "openldap_operations_initiated_delta" /tmp/metrics_final.txt | while read -r line; do
                echo "$line"
            done
            
        else
            echo "No metrics file available"
        fi
        
    } > "$report_file"
    
    log_success "Test report generated: $report_file"
    cat "$report_file"
    
    return 0
}

# Main test execution
main() {
    echo "========================================"
    echo "LDAP Operation Metrics Test Suite"
    echo "========================================"
    echo "Project: $(basename "$PROJECT_ROOT")"
    echo "Test Dir: $TESTS_DIR"
    echo "LDAP URL: $LDAP_URL"
    echo "Exporter Port: $EXPORTER_PORT"
    echo "========================================"
    echo ""
    
    local test_start_time=$(date +%s)
    local failed_tests=0
    
    # Run test phases
    local test_phases=(
        "check_prerequisites"
        "test_ldap_connectivity"
        "configure_monitor_backend"
        "test_monitor_backend"
        "build_exporter"
        "start_exporter"
    )
    
    for phase in "${test_phases[@]}"; do
        if ! $phase; then
            log_error "Test phase failed: $phase"
            ((failed_tests++))
            
            # Some failures are acceptable, continue with remaining tests
            if [[ "$phase" == "test_monitor_backend" ]]; then
                log_warning "Continuing without Monitor backend (operation metrics may not be available)"
            elif [[ "$phase" == "check_prerequisites" || "$phase" == "test_ldap_connectivity" || "$phase" == "build_exporter" || "$phase" == "start_exporter" ]]; then
                log_error "Critical test phase failed, aborting"
                exit 1
            fi
        fi
    done
    
    # Get baseline metrics
    get_ldap_operation_counts /tmp/ldap_baseline.txt || true
    
    # Generate traffic and test
    generate_ldap_traffic || ((failed_tests++))
    
    # Get final metrics
    get_ldap_operation_counts /tmp/ldap_final.txt || true
    
    # Test exporter metrics collection
    test_exporter_metrics || ((failed_tests++))
    
    # Compare results
    compare_metrics || true
    
    # Generate report
    generate_report || true
    
    # Final results
    local test_end_time=$(date +%s)
    local test_duration=$((test_end_time - test_start_time))
    
    echo ""
    log_step "Test Results"
    log_info "Test duration: ${test_duration}s"
    
    if [[ $failed_tests -eq 0 ]]; then
        log_success "All tests passed successfully!"
        exit 0
    else
        log_warning "$failed_tests test phases had issues"
        log_info "Check the output above for details"
        exit 1
    fi
}

# Show usage
show_usage() {
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  -h, --help                Show this help"
    echo "  --ldap-host HOST         LDAP server host (default: localhost)"
    echo "  --ldap-port PORT         LDAP server port (default: 389)"
    echo "  --exporter-port PORT     Exporter port (default: 9360)"
    echo "  --no-build               Skip building exporter binary"
    echo "  --traffic COUNT          Number of operations to generate (default: 30)"
    echo "  --timeout SECONDS        Test timeout (default: 300)"
    echo ""
    echo "Environment variables:"
    echo "  LDAP_HOST, LDAP_PORT, EXPORTER_PORT"
    echo "  ADMIN_PASSWORD, CONFIG_PASSWORD"
    echo "  TEST_TRAFFIC_OPERATIONS, TEST_TIMEOUT"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Run with defaults"
    echo "  $0 --ldap-port 1389 --traffic 50    # Custom port and traffic"
    echo "  ADMIN_PASSWORD=secret $0             # Custom password"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_usage
            exit 0
            ;;
        --ldap-host)
            LDAP_HOST="$2"
            LDAP_URL="ldap://${LDAP_HOST}:${LDAP_PORT}"
            shift 2
            ;;
        --ldap-port)
            LDAP_PORT="$2"
            LDAP_URL="ldap://${LDAP_HOST}:${LDAP_PORT}"
            shift 2
            ;;
        --exporter-port)
            EXPORTER_PORT="$2"
            shift 2
            ;;
        --no-build)
            BUILD_BINARY=false
            shift
            ;;
        --traffic)
            TEST_TRAFFIC_OPERATIONS="$2"
            shift 2
            ;;
        --timeout)
            TEST_TIMEOUT="$2"
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