package security

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/go-ldap/ldap/v3"
)

// attrRegex is compiled once at package init so ValidateLDAPAttribute
// stays allocation-free on the hot scrape path.
var attrRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9.-]*$`)

// ValidateLDAPDN validates an LDAP Distinguished Name for security and
// correctness. The check delegates the syntactic part to go-ldap's
// RFC 4514 parser (ldap.ParseDN), which handles OIDs, hyphens in
// attribute types, escaped commas, multi-valued RDNs (cn=a+sn=b) and
// other legitimate forms that the previous hand-rolled regex rejected.
// The remaining checks enforce length caps and reject sentinel payloads
// (null bytes, CRLF, path traversal, inline HTML/script) that have no
// place in a DN used by this exporter.
func ValidateLDAPDN(dn string) error {
	if dn == "" {
		return errors.New("DN cannot be empty")
	}

	// DoS protection
	if len(dn) > MaxDNLength {
		return errors.New("invalid DN: exceeds maximum length")
	}

	// Sentinels that slapd itself would also refuse, surfaced early to
	// keep failure modes uniform across callers.
	if strings.ContainsAny(dn, "\x00\r\n") {
		return errors.New("invalid DN: control characters not allowed")
	}
	if strings.Contains(dn, "../") || strings.Contains(dn, "..\\") {
		return errors.New("invalid DN: path traversal detected")
	}

	dnLower := strings.ToLower(dn)
	// These literal substrings never appear in a legitimate LDAP DN used
	// by this exporter and strongly suggest a copy-paste from user input.
	suspicious := []string{"<script", "</script>", "javascript:", "data:"}
	for _, needle := range suspicious {
		if strings.Contains(dnLower, needle) {
			return errors.New("invalid DN: suspicious content detected")
		}
	}

	// RFC 4514 syntactic validation via go-ldap. ParseDN normalises
	// escape handling, handles OIDs (2.5.4.3=value), hyphens in
	// attribute types (dc-foo=bar), multi-valued RDNs (cn=a+sn=b), and
	// unicode values — all of which the previous regex rejected.
	if _, err := ldap.ParseDN(dn); err != nil {
		return errors.New("invalid DN: malformed structure")
	}

	return nil
}

// ValidateLDAPFilter performs a **shape** check on an LDAP search filter:
// it enforces a length cap, rejects null bytes, verifies that the
// outermost parentheses wrap the expression, and checks bracket balance.
//
// It deliberately does NOT validate that embedded values are correctly
// escaped against RFC 4515 injection — doing so from a static string
// analysis is ambiguous. Callers that build filters by interpolating
// untrusted user input MUST escape every interpolated value with
// EscapeFilterValue (or ldap.EscapeFilter) BEFORE calling this function.
// The exporter itself only uses hard-coded filters, so this check is
// sufficient for its internal usage; it remains exported as a shape
// guard, not a security boundary.
func ValidateLDAPFilter(filter string) error {
	if filter == "" {
		return errors.New("filter cannot be empty")
	}
	if len(filter) > MaxFilterLength {
		return errors.New("invalid filter: exceeds maximum length")
	}
	if strings.Contains(filter, "\x00") {
		return errors.New("invalid filter: null byte detected")
	}
	if !hasBalancedParentheses(filter) {
		return errors.New("invalid filter: unbalanced parentheses")
	}
	if !strings.HasPrefix(filter, "(") || !strings.HasSuffix(filter, ")") {
		return errors.New("invalid filter: must be enclosed in parentheses")
	}
	return nil
}

// EscapeFilterValue escapes the RFC 4515 metacharacters ( ) * \ NUL in
// value so it can be safely interpolated into an LDAP search filter.
// This is the function callers must use when building a filter from
// untrusted input — `ValidateLDAPFilter` only checks filter shape, it
// does not inspect embedded values.
func EscapeFilterValue(value string) string {
	return ldap.EscapeFilter(value)
}

// ValidateLDAPAttribute validates an LDAP attribute name.
func ValidateLDAPAttribute(attr string) error {
	if attr == "" {
		return errors.New("attribute name cannot be empty")
	}
	if len(attr) > MaxAttributeLength {
		return errors.New("invalid attribute: exceeds maximum length")
	}
	if !attrRegex.MatchString(attr) {
		return errors.New("invalid attribute: contains invalid characters")
	}
	return nil
}

// hasBalancedParentheses checks if parentheses are balanced in a filter,
// honoring backslash escapes.
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

// SanitizeLDAPValue strips null bytes and Unicode control characters from
// a value. It does NOT escape LDAP filter or DN metacharacters — callers
// building a filter must also pass the result through EscapeFilterValue,
// and callers building a DN must let ldap.ParseDN / go-ldap's dn helpers
// handle escaping. Treat this function as a normaliser, not a
// security boundary.
func SanitizeLDAPValue(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// ValidateSuffixDN validates a suffix DN for contextCSN replication queries.
// Only allows DN structures that look like naming contexts (dc=..., o=...,
// ou=..., c=...). Rejects access to cn=config, cn=schema, and other
// sensitive trees.
func ValidateSuffixDN(dn string) error {
	if err := ValidateLDAPDN(dn); err != nil {
		return err
	}

	dnLower := strings.ToLower(dn)
	blockedTrees := []string{"cn=config", "cn=schema", "cn=subschema", "olc"}
	for _, blocked := range blockedTrees {
		if strings.Contains(dnLower, blocked) {
			return errors.New("access to configuration tree not allowed")
		}
	}

	validPrefixes := []string{"dc=", "o=", "ou=", "c="}
	for _, prefix := range validPrefixes {
		if strings.HasPrefix(dnLower, prefix) {
			return nil
		}
	}
	return errors.New("DN must be a valid naming context (dc=, o=, ou=, c=)")
}

// ValidateAccessLogDN validates that a DN is within the accesslog tree.
func ValidateAccessLogDN(dn string) error {
	if err := ValidateLDAPDN(dn); err != nil {
		return err
	}
	dnLower := strings.ToLower(dn)
	if dnLower != "cn=accesslog" && !strings.HasSuffix(dnLower, ",cn=accesslog") {
		return errors.New("DN must be within cn=accesslog tree")
	}
	return nil
}

// ValidateMonitorDN validates that a DN is within the monitor tree.
func ValidateMonitorDN(dn string) error {
	if err := ValidateLDAPDN(dn); err != nil {
		return err
	}
	dnLower := strings.ToLower(dn)
	if !strings.Contains(dnLower, "cn=monitor") {
		return errors.New("DN must be within cn=Monitor tree")
	}
	if strings.Contains(dnLower, "cn=config") || strings.Contains(dnLower, "cn=schema") {
		return errors.New("access outside monitor tree not allowed")
	}
	return nil
}
