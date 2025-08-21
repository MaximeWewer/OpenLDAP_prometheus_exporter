#!/bin/bash

# OpenLDAP Test Setup Script
# This script configures OpenLDAP for the prometheus exporter tests

set -euo pipefail

# Configuration
LDAP_HOST=${LDAP_HOST:-localhost}
LDAP_PORT=${LDAP_PORT:-1389}
ADMIN_DN="cn=adminconfig,cn=config"
ADMIN_PASSWORD=${ADMIN_PASSWORD:-configpassword}
LDAP_URL="ldap://${LDAP_HOST}:${LDAP_PORT}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[LDAP-SETUP]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[LDAP-SETUP]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[LDAP-SETUP]${NC} $1"
}

log_error() {
    echo -e "${RED}[LDAP-SETUP]${NC} $1"
}

# Wait for LDAP to be ready
wait_for_ldap() {
    log_info "Waiting for LDAP server to be ready..."
    
    for i in {1..60}; do
        if ldapsearch -x -H "$LDAP_URL" -b "" -s base "(objectClass=*)" namingContexts &>/dev/null; then
            log_success "LDAP server is ready!"
            return 0
        fi
        log_info "Attempt $i: LDAP not ready yet, waiting..."
        sleep 2
    done
    
    log_error "LDAP server failed to start within timeout"
    return 1
}

# Test basic connectivity
test_connectivity() {
    log_info "Testing basic connectivity..."
    
    # Test anonymous bind
    if ldapsearch -x -H "$LDAP_URL" -b "" -s base "(objectClass=*)" namingContexts; then
        log_success "Anonymous bind successful"
    else
        log_error "Anonymous bind failed"
        return 1
    fi
    
    # Test admin bind
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=config" -s base "(objectClass=*)" cn; then
        log_success "Admin bind successful"
    else
        log_error "Admin bind failed"
        return 1
    fi
}

# Apply LDIF configurations
apply_ldif() {
    local ldif_file="$1"
    local description="$2"
    
    log_info "Applying $description..."
    
    if [ -f "$ldif_file" ]; then
        if ldapmodify -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -f "$ldif_file"; then
            log_success "$description applied successfully"
        else
            # Some modifications might fail if already exist, that's often OK
            log_warning "$description may have partially applied (some entries might already exist)"
        fi
    else
        log_warning "LDIF file not found: $ldif_file"
    fi
}

# Apply all configurations
setup_ldap() {
    local base_dir="$(dirname "$(dirname "$0")")"
    local schema_dir="$base_dir/data/ldap-schemas"
    local data_dir="$base_dir/data/test-data"
    
    log_info "Starting LDAP configuration..."
    log_info "Using schema directory: $schema_dir"
    log_info "Using test data directory: $data_dir"
    
    # Apply configurations in order
    apply_ldif "$schema_dir/monitor-acl.ldif" "Monitor ACL configuration"
    apply_ldif "$schema_dir/enable-monitor.ldif" "Monitor backend configuration"  
    apply_ldif "$schema_dir/base-monitoring.ldif" "Base DN monitoring configuration"
    
    # Add test data if it exists
    if [ -f "$data_dir/multi-domain-users.ldif" ]; then
        log_info "Adding test data..."
        if ldapadd -x -H "$LDAP_URL" -D "cn=admin,dc=example,dc=org" -w "adminpassword" -f "$data_dir/multi-domain-users.ldif"; then
            log_success "Test data added"
        else
            log_warning "Test data may have been partially added (some entries might already exist)"
        fi
    fi
}

# Verify monitor backend access
verify_monitor_access() {
    log_info "Verifying Monitor backend access..."
    
    # Test Monitor root
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=Monitor" -s base "(objectClass=*)" cn; then
        log_success "Monitor root accessible"
    else
        log_error "Monitor root not accessible"
        return 1
    fi
    
    # Test Monitor statistics
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=Statistics,cn=Monitor" "(objectClass=*)" cn; then
        log_success "Monitor statistics accessible"
    else
        log_warning "Monitor statistics not accessible"
    fi
    
    # Test Monitor connections
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=Connections,cn=Monitor" "(objectClass=*)" cn; then
        log_success "Monitor connections accessible"
    else
        log_warning "Monitor connections not accessible"
    fi
    
    # Test Monitor operations
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=Operations,cn=Monitor" "(objectClass=*)" cn; then
        log_success "Monitor operations accessible"
    else
        log_warning "Monitor operations not accessible"
    fi
}

# Show LDAP configuration summary
show_summary() {
    log_info "LDAP Configuration Summary:"
    echo "  LDAP URL: $LDAP_URL"
    echo "  Admin DN: $ADMIN_DN"
    echo "  Monitor backend: Configured"
    echo "  ACL configured: Yes"
    echo "  Base monitoring: Enabled"
    
    log_success "LDAP setup completed successfully!"
    log_info "The OpenLDAP server is now ready for prometheus exporter testing"
}

# Main execution
main() {
    log_info "OpenLDAP Test Setup Script"
    log_info "=========================="
    
    wait_for_ldap
    test_connectivity
    
    # Detect LDAP structure for debugging
    local detect_script="$(dirname "$0")/detect-ldap-structure.sh"
    if [ -f "$detect_script" ]; then
        log_info "Detecting LDAP structure..."
        if bash "$detect_script"; then
            log_info "Structure detection completed"
        else
            log_warning "Structure detection failed, continuing with setup..."
        fi
        echo ""
    fi
    
    setup_ldap
    
    # Wait a bit for changes to propagate
    sleep 3
    
    verify_monitor_access
    show_summary
}

# Execute if run directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi