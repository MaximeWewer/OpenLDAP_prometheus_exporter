package logger

import (
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// InitLogger initializes the global logger with specified component name and log level
func InitLogger(component string, logLevel string) {
	// Set global log level
	level := zerolog.InfoLevel
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		level = zerolog.DebugLevel
	case "INFO":
		level = zerolog.InfoLevel
	case "WARN":
		level = zerolog.WarnLevel
	case "ERROR":
		level = zerolog.ErrorLevel
	case "FATAL":
		level = zerolog.FatalLevel
	}

	zerolog.SetGlobalLevel(level)

	// Configure JSON output to stdout
	log.Logger = zerolog.New(os.Stdout).
		With().
		Timestamp().
		Str("component", component).
		Logger()
}

// Debug logs a debug message with package context and optional structured fields
func Debug(pkg string, message string, fields ...map[string]interface{}) {
	event := log.Debug().Str("package", pkg)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Info logs an info message with package context and optional structured fields
func Info(pkg string, message string, fields ...map[string]interface{}) {
	event := log.Info().Str("package", pkg)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Warn logs a warning message with package context and optional structured fields
func Warn(pkg string, message string, fields ...map[string]interface{}) {
	event := log.Warn().Str("package", pkg)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Error logs an error message with package context, error and optional structured fields
func Error(pkg string, message string, err error, fields ...map[string]interface{}) {
	event := log.Error().Str("package", pkg).Err(err)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Fatal logs a fatal error message with package context and terminates the program
func Fatal(pkg string, message string, err error, fields ...map[string]interface{}) {
	event := log.Fatal().Str("package", pkg).Err(err)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Sensitive field names that should be sanitized in logs
var sensitiveFields = []string{
	"password", "passwd", "pwd", "secret", "token", "key", "auth",
	"credential", "pass", "userPassword", "userpassword",
}

// Pre-compiled regex patterns for performance optimization
var (
	// URL patterns that might contain credentials
	credentialURLPattern = regexp.MustCompile(`(ldap[s]?://)([^:]+):([^@]+)@(.+)`)
	
	// Compiled password patterns for error message sanitization
	passwordPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)password[=:]\s*\S+`),
		regexp.MustCompile(`(?i)passwd[=:]\s*\S+`),
		regexp.MustCompile(`(?i)pwd[=:]\s*\S+`),
		regexp.MustCompile(`(?i)token[=:]\s*\S+`),
		regexp.MustCompile(`(?i)secret[=:]\s*\S+`),
		regexp.MustCompile(`(?i)key[=:]\s*\S+`),
	}
	
	// Bind DN password pattern for LDAP error messages
	bindPattern = regexp.MustCompile(`(?i)(bind\s+failed.*password:\s*)(\S+)`)
)

// Pool for reusing maps to reduce allocations
var (
	mapPool = sync.Pool{
		New: func() interface{} {
			return make(map[string]interface{}, 8) // Pre-allocate capacity for common case
		},
	}
	// Mutex to protect map pool operations in high-concurrency scenarios
	mapPoolMutex sync.RWMutex
)

// getMapFromPool returns a clean map from the pool with thread safety
func getMapFromPool() map[string]interface{} {
	mapPoolMutex.RLock()
	m := mapPool.Get().(map[string]interface{})
	mapPoolMutex.RUnlock()
	
	// Clear the map in case it has leftover data
	for k := range m {
		delete(m, k)
	}
	return m
}

// putMapToPool returns a map to the pool with thread safety
func putMapToPool(m map[string]interface{}) {
	if m != nil {
		mapPoolMutex.Lock()
		mapPool.Put(m)
		mapPoolMutex.Unlock()
	}
}

// sanitizeFields sanitizes sensitive information from log fields
func sanitizeFields(fields map[string]interface{}) map[string]interface{} {
	if fields == nil {
		return nil
	}

	sanitized := getMapFromPool()
	for k, v := range fields {
		sanitized[k] = sanitizeValue(k, v)
	}
	return sanitized
}

// sanitizeValue sanitizes a specific field value based on its key name
func sanitizeValue(key string, value interface{}) interface{} {
	// Check if field name indicates sensitive data
	keyLower := strings.ToLower(key)
	for _, sensitive := range sensitiveFields {
		if strings.Contains(keyLower, sensitive) {
			return "***REDACTED***"
		}
	}

	// Handle URL values that might contain credentials
	if strValue, ok := value.(string); ok {
		if strings.Contains(keyLower, "url") || strings.Contains(keyLower, "uri") {
			return sanitizeURL(strValue)
		}

		// Handle DN values that might contain sensitive info
		if strings.Contains(keyLower, "dn") && strings.Contains(strValue, "userPassword") {
			return sanitizeDN(strValue)
		}
	}

	return value
}

// sanitizeURL removes credentials from LDAP URLs
func sanitizeURL(url string) string {
	if credentialURLPattern.MatchString(url) {
		return credentialURLPattern.ReplaceAllString(url, "${1}${2}:***@${4}")
	}
	return url
}

// sanitizeDN removes sensitive attributes from DN strings
func sanitizeDN(dn string) string {
	// Remove userPassword and other sensitive attributes from DN
	for _, attr := range []string{"userPassword", "userpassword", "password"} {
		pattern := regexp.MustCompile(`(?i)` + attr + `=[^,]*,?`)
		dn = pattern.ReplaceAllString(dn, "")
	}
	return strings.TrimSuffix(dn, ",")
}

// SafeDebug logs a debug message with package context and sanitized fields
func SafeDebug(pkg string, message string, fields ...map[string]interface{}) {
	event := log.Debug().Str("package", pkg)
	if len(fields) > 0 {
		sanitized := sanitizeFields(fields[0])
		for k, v := range sanitized {
			event = event.Interface(k, v)
		}
		putMapToPool(sanitized)
	}
	event.Msg(message)
}

// SafeInfo logs an info message with package context and sanitized fields
func SafeInfo(pkg string, message string, fields ...map[string]interface{}) {
	event := log.Info().Str("package", pkg)
	if len(fields) > 0 {
		sanitized := sanitizeFields(fields[0])
		for k, v := range sanitized {
			event = event.Interface(k, v)
		}
		putMapToPool(sanitized)
	}
	event.Msg(message)
}

// SafeWarn logs a warning message with package context and sanitized fields
func SafeWarn(pkg string, message string, fields ...map[string]interface{}) {
	event := log.Warn().Str("package", pkg)
	if len(fields) > 0 {
		sanitized := sanitizeFields(fields[0])
		for k, v := range sanitized {
			event = event.Interface(k, v)
		}
		putMapToPool(sanitized)
	}
	event.Msg(message)
}

// sanitizeError sanitizes sensitive information from error messages
func sanitizeError(err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()

	// Sanitize URLs in error messages
	errMsg = sanitizeURL(errMsg)

	// Remove any passwords or tokens that might appear in error messages using pre-compiled patterns
	for _, pattern := range passwordPatterns {
		errMsg = pattern.ReplaceAllString(errMsg, "***REDACTED***")
	}

	// Remove bind DN passwords from LDAP error messages using pre-compiled pattern
	errMsg = bindPattern.ReplaceAllString(errMsg, "${1}***REDACTED***")

	// Create new error with sanitized message
	return &sanitizedError{msg: errMsg}
}

// sanitizedError wraps a sanitized error message
type sanitizedError struct {
	msg string
}

func (e *sanitizedError) Error() string {
	return e.msg
}

// SafeError logs an error message with package context, sanitized error and sanitized fields
func SafeError(pkg string, message string, err error, fields ...map[string]interface{}) {
	// Sanitize the error message
	sanitizedErr := sanitizeError(err)

	event := log.Error().Str("package", pkg).Err(sanitizedErr)
	if len(fields) > 0 {
		sanitized := sanitizeFields(fields[0])
		for k, v := range sanitized {
			event = event.Interface(k, v)
		}
		putMapToPool(sanitized)
	}
	event.Msg(message)
}
