#!/bin/bash

# LDAP Structure Detection Script
# This script detects the OpenLDAP database structure for proper configuration

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
    echo -e "${BLUE}[DETECT]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[DETECT]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[DETECT]${NC} $1"
}

log_error() {
    echo -e "${RED}[DETECT]${NC} $1"
}

# Detect database structure
detect_database_structure() {
    log_info "Detecting OpenLDAP database structure..."
    
    # Query all databases
    local databases
    if databases=$(ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=config" "(objectClass=olcDatabaseConfig)" dn 2>/dev/null); then
        log_success "Successfully queried database configuration"
        
        echo "$databases" | grep "^dn:" | while read -r line; do
            db=$(echo "$line" | sed 's/dn: //')
            log_info "Found database: $db"
            
            # Get database type and suffix
            if db_info=$(ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "$db" -s base "(objectClass=*)" olcDatabase olcSuffix 2>/dev/null); then
                local db_type suffix
                db_type=$(echo "$db_info" | grep "^olcDatabase:" | sed 's/olcDatabase: //')
                suffix=$(echo "$db_info" | grep "^olcSuffix:" | sed 's/olcSuffix: //')
                
                if [ -n "$db_type" ]; then
                    log_info "  Type: $db_type"
                fi
                if [ -n "$suffix" ]; then
                    log_info "  Suffix: $suffix"
                fi
            fi
            echo ""
        done
        
    else
        log_error "Failed to query database configuration"
        return 1
    fi
}

# Detect monitor backend status
detect_monitor_status() {
    log_info "Checking Monitor backend status..."
    
    # Try to access Monitor backend
    if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=Monitor" -s base "(objectClass=*)" cn &>/dev/null; then
        log_success "Monitor backend is accessible"
        
        # Check specific monitor entries
        local monitor_entries=("Connections" "Statistics" "Operations" "Threads" "Time" "Databases")
        for entry in "${monitor_entries[@]}"; do
            if ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "cn=$entry,cn=Monitor" -s base "(objectClass=*)" cn &>/dev/null; then
                log_success "  Monitor $entry: Available"
            else
                log_warning "  Monitor $entry: Not available"
            fi
        done
    else
        log_error "Monitor backend is not accessible"
        return 1
    fi
}

# Detect base DN structure
detect_base_dn_structure() {
    log_info "Checking base DN structure..."
    
    # Common base DNs to check
    local base_dns=("dc=example,dc=org" "dc=company,dc=net" "dc=test,dc=local" "dc=production,dc=com")
    
    for base_dn in "${base_dns[@]}"; do
        if ldapsearch -x -H "$LDAP_URL" -b "$base_dn" -s base "(objectClass=*)" dc &>/dev/null; then
            log_success "  Base DN $base_dn: Exists"
            
            # Count entries in this base DN
            local count
            count=$(ldapsearch -x -H "$LDAP_URL" -b "$base_dn" "(objectClass=*)" dn 2>/dev/null | grep -c "^dn:" || echo "0")
            log_info "    Entries: $count"
        else
            log_warning "  Base DN $base_dn: Does not exist"
        fi
    done
}

# Check ACL configuration
check_acl_configuration() {
    log_info "Checking ACL configuration..."
    
    # Check config database ACL
    if config_acl=$(ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "olcDatabase={0}config,cn=config" -s base "(objectClass=*)" olcAccess 2>/dev/null); then
        log_success "Config database ACL accessible"
        if echo "$config_acl" | grep -q "adminconfig"; then
            log_success "  adminconfig has access to config database"
        else
            log_warning "  adminconfig access to config database not found"
        fi
    else
        log_warning "Could not check config database ACL"
    fi
    
    # Check monitor database ACL
    if monitor_acl=$(ldapsearch -x -H "$LDAP_URL" -D "$ADMIN_DN" -w "$ADMIN_PASSWORD" -b "olcDatabase={1}monitor,cn=config" -s base "(objectClass=*)" olcAccess 2>/dev/null); then
        log_success "Monitor database ACL accessible"
        if echo "$monitor_acl" | grep -q "adminconfig"; then
            log_success "  adminconfig has access to monitor database"
        else
            log_warning "  adminconfig access to monitor database not found"
        fi
    else
        log_warning "Could not check monitor database ACL"
    fi
}

# Generate configuration recommendations
generate_recommendations() {
    log_info "Configuration Recommendations:"
    echo ""
    echo "Based on the detected structure, here are the recommended configurations:"
    echo ""
    echo "1. Database Structure:"
    echo "   - Use {0}config for configuration database"
    echo "   - Use {1}monitor for monitor database"
    echo "   - Use {2}mdb for main data database"
    echo ""
    echo "2. ACL Configuration:"
    echo "   - Ensure adminconfig has manage access to config database"
    echo "   - Ensure adminconfig has read access to monitor database"
    echo "   - Configure main database ACLs for user access"
    echo ""
    echo "3. Monitor Backend:"
    echo "   - Verify Monitor backend is enabled"
    echo "   - Check that monitoring is enabled on main database"
    echo "   - Test access to Monitor entries"
    echo ""
}

# Main execution
main() {
    log_info "OpenLDAP Structure Detection Script"
    log_info "===================================="
    echo ""
    
    detect_database_structure
    echo ""
    
    detect_monitor_status
    echo ""
    
    detect_base_dn_structure
    echo ""
    
    check_acl_configuration
    echo ""
    
    generate_recommendations
    
    log_success "Structure detection completed!"
}

# Execute if run directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi