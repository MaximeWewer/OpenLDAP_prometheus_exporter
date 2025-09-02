package security

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// Pre-compiled regular expressions for better performance
var (
	dnRegex = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9]*=[^,=]+)(,\s*[a-zA-Z][a-zA-Z0-9]*=[^,=]+)*$`)
)

// ValidateLDAPDN validates an LDAP Distinguished Name for security and correctness
func ValidateLDAPDN(dn string) error {
	if dn == "" {
		return errors.New("DN cannot be empty")
	}

	// Check for path traversal attacks
	if strings.Contains(dn, "../") || strings.Contains(dn, "..\\") {
		return errors.New("invalid DN: path traversal detected")
	}

	// Check for null bytes (security vulnerability)
	if strings.Contains(dn, "\x00") {
		return errors.New("invalid DN: null byte detected")
	}

	// Check for excessively long DN (DoS protection)
	if len(dn) > MaxDNLength {
		return errors.New("invalid DN: exceeds maximum length")
	}

	// Check for suspicious characters that could indicate injection attempts
	suspiciousChars := []string{
		"<script", "</script>", // XSS
		"javascript:", "data:", // Script injection
		"\r", "\n", // CRLF injection
	}

	dnLower := strings.ToLower(dn)
	for _, suspicious := range suspiciousChars {
		if strings.Contains(dnLower, suspicious) {
			return errors.New("invalid DN: suspicious content detected")
		}
	}

	// Validate basic DN structure using regex
	// This regex matches basic DN format: cn=value,ou=value,dc=value
	if !dnRegex.MatchString(dn) {
		return errors.New("invalid DN: malformed structure")
	}

	// Additional validation: check for balanced quotes and escaping
	if err := validateDNEscaping(dn); err != nil {
		return err
	}

	return nil
}

// ValidateLDAPFilter validates an LDAP search filter for security
func ValidateLDAPFilter(filter string) error {
	if filter == "" {
		return errors.New("filter cannot be empty")
	}

	// Check for excessively long filter (DoS protection)
	if len(filter) > 2048 {
		return errors.New("invalid filter: exceeds maximum length (2048 characters)")
	}

	// Check for null bytes
	if strings.Contains(filter, "\x00") {
		return errors.New("invalid filter: null byte detected")
	}

	// Validate balanced parentheses
	if !hasBalancedParentheses(filter) {
		return errors.New("invalid filter: unbalanced parentheses")
	}

	// Check for basic filter structure
	if !strings.HasPrefix(filter, "(") || !strings.HasSuffix(filter, ")") {
		return errors.New("invalid filter: must be enclosed in parentheses")
	}

	return nil
}

// ValidateLDAPAttribute validates an LDAP attribute name
func ValidateLDAPAttribute(attr string) error {
	if attr == "" {
		return errors.New("attribute name cannot be empty")
	}

	// Check for reasonable length
	if len(attr) > 256 {
		return errors.New("invalid attribute: exceeds maximum length (256 characters)")
	}

	// Check for valid attribute name format (alphanumeric, hyphens, dots)
	attrRegex := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9.-]*$`)
	if !attrRegex.MatchString(attr) {
		return errors.New("invalid attribute: contains invalid characters")
	}

	// Reject attributes that could be dangerous
	dangerousAttrs := []string{
		"userPassword", "unicodePwd", // Password fields
	}

	attrLower := strings.ToLower(attr)
	for _, dangerous := range dangerousAttrs {
		if attrLower == strings.ToLower(dangerous) {
			return errors.New("invalid attribute: access to sensitive attribute denied")
		}
	}

	return nil
}

// validateDNEscaping checks for proper escaping in DN components
func validateDNEscaping(dn string) error {
	// Split DN into components
	components := strings.Split(dn, ",")

	for _, component := range components {
		component = strings.TrimSpace(component)

		// Each component should have = separator
		if !strings.Contains(component, "=") {
			return errors.New("invalid DN component: missing '=' separator")
		}

		parts := strings.SplitN(component, "=", 2)
		if len(parts) != 2 {
			return errors.New("invalid DN component: malformed")
		}

		value := parts[1]

		// Check for unescaped special characters
		specialChars := ",=+<>#;\""
		for i, r := range value {
			if strings.ContainsRune(specialChars, r) {
				// Check if properly escaped (preceded by backslash)
				if i == 0 || value[i-1] != '\\' {
					return errors.New("invalid DN: unescaped special character")
				}
			}
		}

		// Check for unescaped leading/trailing spaces
		if len(value) > 0 {
			if (value[0] == ' ' || value[len(value)-1] == ' ') &&
				(len(value) < 2 || value[0:2] != "\\ " && value[len(value)-2:] != "\\ ") {
				return errors.New("invalid DN: unescaped leading or trailing space")
			}
		}
	}

	return nil
}

// hasBalancedParentheses checks if parentheses are balanced in a filter
func hasBalancedParentheses(filter string) bool {
	count := 0
	escaped := false

	for _, r := range filter {
		if escaped {
			escaped = false
			continue
		}

		if r == '\\' {
			escaped = true
			continue
		}

		if r == '(' {
			count++
		} else if r == ')' {
			count--
			if count < 0 {
				return false
			}
		}
	}

	return count == 0
}

// SanitizeLDAPValue sanitizes a value for safe use in LDAP operations
func SanitizeLDAPValue(value string) string {
	// Remove null bytes
	value = strings.ReplaceAll(value, "\x00", "")

	// Remove control characters
	var builder strings.Builder
	for _, r := range value {
		if !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
	}

	return builder.String()
}

// ValidateMonitorDN validates that a DN is in the monitor tree (security check)
func ValidateMonitorDN(dn string) error {
	if err := ValidateLDAPDN(dn); err != nil {
		return err
	}

	// Ensure DN is within monitor tree
	dnLower := strings.ToLower(dn)
	if !strings.Contains(dnLower, "cn=monitor") {
		return errors.New("DN must be within cn=Monitor tree")
	}

	// Ensure no directory traversal outside monitor
	if strings.Contains(dnLower, "cn=config") ||
		strings.Contains(dnLower, "cn=schema") {
		return errors.New("access outside monitor tree not allowed")
	}

	return nil
}
