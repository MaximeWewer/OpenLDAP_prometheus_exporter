package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Configuration constants for validation limits
const (
	// Timeout limits
	MinTimeout     = 1 * time.Second
	MaxTimeout     = 5 * time.Minute
	DefaultTimeout = 10 * time.Second

	// Update interval limits
	MinUpdateEvery     = 5 * time.Second
	MaxUpdateEvery     = 10 * time.Minute
	DefaultUpdateEvery = 15 * time.Second

	// Server name limits
	MaxServerNameLen = 128

	// Events exporter limits
	MinEventsInterval     = 5 * time.Second
	MaxEventsInterval     = 30 * time.Minute
	DefaultEventsInterval = 30 * time.Second
)

// EventsRotationMode describes how the events output file is rotated.
type EventsRotationMode string

const (
	// EventsRotationNone means events are appended to a single file indefinitely.
	EventsRotationNone EventsRotationMode = "none"
	// EventsRotationDaily means events are written to a new file every UTC day,
	// named by replacing the token "{date}" in the output path with YYYY-MM-DD.
	EventsRotationDaily EventsRotationMode = "daily"
)

// Config holds all configuration parameters for the OpenLDAP exporter
type Config struct {
	URL           string
	ServerName    string
	TLS           bool
	TLSSkipVerify bool
	TLSCA         string
	TLSCert       string
	TLSKey        string
	TLSServerName string // LDAP_TLS_SERVER_NAME: overrides the TLS SNI / certificate verification hostname (defaults to the host in LDAP_URL)
	Timeout       time.Duration
	UpdateEvery   time.Duration
	Username      string
	Password      *SecureString // Secure storage for password

	// Metric filtering options
	MetricsInclude []string // Only collect these metric groups
	MetricsExclude []string // Exclude these metric groups

	// DC filtering options
	DCInclude []string // Only monitor these domain components
	DCExclude []string // Exclude these domain components from monitoring

	// Authentication method
	AuthMethod string // "simple" (default) or "external" (SASL EXTERNAL for mTLS)

	// Rate limiting options
	RateLimitEnabled bool     // Enable/disable rate limiting (RATE_LIMIT_ENABLED)
	TrustedProxies   []string // CIDR ranges of proxies whose X-Forwarded-For / X-Real-IP headers we honor (HTTP_TRUSTED_PROXIES)

	// Events exporter options (JSON event stream derived from the accesslog overlay).
	// Independent of Prometheus scrapes: runs its own ticker and its own reqStart cursor
	// so enabling it does not perturb metric collection, and metric scrapes do not drain
	// the event stream before it is observed.
	EventsEnabled  bool               // OPENLDAP_EVENTS_ENABLED
	EventsInterval time.Duration      // OPENLDAP_EVENTS_INTERVAL (e.g. 30s)
	EventsOutput   string             // OPENLDAP_EVENTS_OUTPUT ("stdout" or a filesystem path; path may contain "{date}")
	EventsRotation EventsRotationMode // OPENLDAP_EVENTS_ROTATION ("none" or "daily", file output only)
	EventsTypes    []string           // OPENLDAP_EVENTS_TYPES (comma separated; empty = all types)
}

// LoadConfig loads configuration from environment variables. The signature
// returns an error deliberately — every misconfiguration surfaces as a
// returned error instead of calling logger.Fatal internally, so the
// caller (cmd/main.go) decides how to exit and so the function is
// actually unit-testable without process termination.
func LoadConfig() (*Config, error) {
	// Get password: LDAP_PASSWORD_FILE takes precedence over LDAP_PASSWORD
	passwordValue := loadPassword()
	var securePassword *SecureString
	if passwordValue != "" {
		var err error
		securePassword, err = NewSecureString(passwordValue)
		if err != nil {
			return nil, fmt.Errorf("failed to create secure password storage: %w", err)
		}
		// Go strings are immutable, assigning "" to passwordValue would not
		// actually zero the underlying backing array — the secure copy lives
		// in securePassword from this point on. The local string is left to
		// the garbage collector.
	}

	config := &Config{
		URL:           getEnvOrDefault("LDAP_URL", ""),
		ServerName:    getEnvOrDefault("LDAP_SERVER_NAME", "openldap"),
		TLS:           getEnvBoolOrDefault("LDAP_TLS", false),
		TLSSkipVerify: getEnvBoolOrDefault("LDAP_TLS_SKIP_VERIFY", false),
		TLSCA:         getEnvOrDefault("LDAP_TLS_CA", ""),
		TLSCert:       getEnvOrDefault("LDAP_TLS_CERT", ""),
		TLSKey:        getEnvOrDefault("LDAP_TLS_KEY", ""),
		TLSServerName: getEnvOrDefault("LDAP_TLS_SERVER_NAME", ""),
		Timeout:       time.Duration(getEnvIntOrDefault("LDAP_TIMEOUT", 10)) * time.Second,
		UpdateEvery:   time.Duration(getEnvIntOrDefault("LDAP_UPDATE_EVERY", 15)) * time.Second,
		Username:      getEnvOrDefault("LDAP_USERNAME", ""),
		Password:      securePassword,
	}

	if config.URL == "" {
		return nil, errors.New("LDAP_URL environment variable is required")
	}

	// SASL EXTERNAL uses client cert — no username/password needed
	authMethod := strings.ToLower(getEnvOrDefault("LDAP_AUTH_METHOD", "simple"))
	if authMethod != "external" {
		if config.Username == "" {
			return nil, errors.New("LDAP_USERNAME environment variable is required")
		}
		if config.Password == nil || config.Password.IsEmpty() {
			return nil, errors.New("LDAP_PASSWORD or LDAP_PASSWORD_FILE is required")
		}
	}

	// Validate timeout values
	if config.Timeout <= 0 {
		logger.Warn("config", "Invalid LDAP_TIMEOUT, using default", map[string]interface{}{
			"provided": config.Timeout,
			"default":  DefaultTimeout,
		})
		config.Timeout = DefaultTimeout
	}
	if config.Timeout > MaxTimeout {
		logger.Warn("config", "LDAP_TIMEOUT is very high, using maximum", map[string]interface{}{
			"provided": config.Timeout,
			"maximum":  MaxTimeout,
		})
		config.Timeout = MaxTimeout
	}

	if config.UpdateEvery <= 0 {
		logger.Warn("config", "Invalid LDAP_UPDATE_EVERY, using default", map[string]interface{}{
			"provided": config.UpdateEvery,
			"default":  DefaultUpdateEvery,
		})
		config.UpdateEvery = DefaultUpdateEvery
	}
	if config.UpdateEvery < MinUpdateEvery {
		logger.Warn("config", "LDAP_UPDATE_EVERY is too low, using minimum", map[string]interface{}{
			"provided": config.UpdateEvery,
			"minimum":  MinUpdateEvery,
		})
		config.UpdateEvery = MinUpdateEvery
	}
	if config.UpdateEvery > MaxUpdateEvery {
		logger.Warn("config", "LDAP_UPDATE_EVERY is very high, using maximum", map[string]interface{}{
			"provided": config.UpdateEvery,
			"maximum":  MaxUpdateEvery,
		})
		config.UpdateEvery = MaxUpdateEvery
	}

	// Validate server name
	if len(config.ServerName) > MaxServerNameLen {
		logger.Warn("config", "Server name too long, truncating", map[string]interface{}{
			"original_length": len(config.ServerName),
			"max_length":      MaxServerNameLen,
		})
		config.ServerName = config.ServerName[:MaxServerNameLen]
	}

	// Load metric filtering configuration
	config.MetricsInclude = parseMetricsList(getEnvOrDefault("OPENLDAP_METRICS_INCLUDE", ""))
	config.MetricsExclude = parseMetricsList(getEnvOrDefault("OPENLDAP_METRICS_EXCLUDE", ""))

	// Load DC filtering configuration
	config.DCInclude = parseMetricsList(getEnvOrDefault("OPENLDAP_DC_INCLUDE", ""))
	config.DCExclude = parseMetricsList(getEnvOrDefault("OPENLDAP_DC_EXCLUDE", ""))

	// Load authentication method
	config.AuthMethod = strings.ToLower(getEnvOrDefault("LDAP_AUTH_METHOD", "simple"))
	if config.AuthMethod != "simple" && config.AuthMethod != "external" {
		logger.Warn("config", "Invalid LDAP_AUTH_METHOD, using 'simple'", map[string]interface{}{
			"provided": config.AuthMethod,
			"valid":    []string{"simple", "external"},
		})
		config.AuthMethod = "simple"
	}

	// Load rate limiting configuration
	config.RateLimitEnabled = getEnvBoolOrDefault("RATE_LIMIT_ENABLED", true)
	config.TrustedProxies = parseMetricsList(getEnvOrDefault("HTTP_TRUSTED_PROXIES", ""))

	// Load events exporter configuration
	config.EventsEnabled = getEnvBoolOrDefault("OPENLDAP_EVENTS_ENABLED", false)
	config.EventsInterval = parseEventsInterval(getEnvOrDefault("OPENLDAP_EVENTS_INTERVAL", ""))
	config.EventsOutput = strings.TrimSpace(getEnvOrDefault("OPENLDAP_EVENTS_OUTPUT", "stdout"))
	config.EventsRotation = parseEventsRotation(getEnvOrDefault("OPENLDAP_EVENTS_ROTATION", string(EventsRotationNone)))
	config.EventsTypes = parseMetricsList(getEnvOrDefault("OPENLDAP_EVENTS_TYPES", ""))

	// Validate and warn about metric filtering configuration
	config.validateMetricFiltering()
	config.validateDCFiltering()

	// Security warnings
	if config.TLSSkipVerify {
		logger.SafeWarn("config", "TLS certificate verification is disabled - this is insecure in production!", map[string]interface{}{
			"setting": "LDAP_TLS_SKIP_VERIFY=true",
			"risk":    "Man-in-the-middle attacks possible",
		})
	}

	return config, nil
}

// Clear securely cleans up sensitive data from the configuration
func (c *Config) Clear() {
	if c.Password != nil {
		c.Password.Clear()
		c.Password = nil
	}
}

// loadPassword reads the LDAP password from LDAP_PASSWORD_FILE (Docker/Kubernetes secrets)
// or falls back to LDAP_PASSWORD environment variable.
func loadPassword() string {
	// LDAP_PASSWORD_FILE takes precedence (Docker secrets, Kubernetes secrets)
	if filePath := os.Getenv("LDAP_PASSWORD_FILE"); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			logger.Fatal("config", "Failed to read LDAP_PASSWORD_FILE", err, map[string]interface{}{
				"path": filePath,
			})
		}

		password := strings.TrimRight(string(data), "\n\r")
		if password == "" {
			logger.Fatal("config", "LDAP_PASSWORD_FILE is empty", nil, map[string]interface{}{
				"path": filePath,
			})
		}

		// Clear the byte slice from memory
		for i := range data {
			data[i] = 0
		}

		logger.SafeInfo("config", "Password loaded from file", map[string]interface{}{
			"source": "LDAP_PASSWORD_FILE",
		})
		return password
	}

	// Fallback to LDAP_PASSWORD environment variable
	return getEnvOrDefault("LDAP_PASSWORD", "")
}

// getEnvOrDefault retrieves an environment variable or returns the default value if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBoolOrDefault retrieves a boolean environment variable or returns the default value if not set or invalid
func getEnvBoolOrDefault(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// getEnvIntOrDefault retrieves an integer environment variable or returns the default value if not set or invalid
func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

// parseEventsInterval parses an interval string such as "30s" or "1m" for the
// events runner and clamps it into the allowed range. Empty falls back to default.
func parseEventsInterval(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultEventsInterval
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		logger.SafeWarn("config", "Invalid OPENLDAP_EVENTS_INTERVAL, using default", map[string]interface{}{
			"provided": value,
			"default":  DefaultEventsInterval.String(),
		})
		return DefaultEventsInterval
	}
	if d < MinEventsInterval {
		logger.SafeWarn("config", "OPENLDAP_EVENTS_INTERVAL too low, clamped to minimum", map[string]interface{}{
			"provided": d.String(),
			"minimum":  MinEventsInterval.String(),
		})
		return MinEventsInterval
	}
	if d > MaxEventsInterval {
		logger.SafeWarn("config", "OPENLDAP_EVENTS_INTERVAL too high, clamped to maximum", map[string]interface{}{
			"provided": d.String(),
			"maximum":  MaxEventsInterval.String(),
		})
		return MaxEventsInterval
	}
	return d
}

// parseEventsRotation parses the rotation mode for the file-backed events writer.
func parseEventsRotation(value string) EventsRotationMode {
	switch EventsRotationMode(strings.ToLower(strings.TrimSpace(value))) {
	case EventsRotationNone, "":
		return EventsRotationNone
	case EventsRotationDaily:
		return EventsRotationDaily
	default:
		logger.SafeWarn("config", "Invalid OPENLDAP_EVENTS_ROTATION, using 'none'", map[string]interface{}{
			"provided": value,
			"valid":    []string{string(EventsRotationNone), string(EventsRotationDaily)},
		})
		return EventsRotationNone
	}
}

// parseMetricsList parses a comma-separated list of metric group names
func parseMetricsList(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// validateMetricFiltering validates the metric filtering configuration and logs appropriate messages
func (c *Config) validateMetricFiltering() {
	validMetricGroups := []string{
		"connections", "statistics", "operations", "threads", "time",
		"waiters", "overlays", "tls", "backends", "listeners", "health",
		"database", "server", "log", "sasl", "replication", "ppolicy", "accesslog",
	}

	// Validate include list
	if len(c.MetricsInclude) > 0 {
		invalidIncludes := []string{}
		for _, metric := range c.MetricsInclude {
			if !isValidMetricGroup(metric, validMetricGroups) {
				invalidIncludes = append(invalidIncludes, metric)
			}
		}

		if len(invalidIncludes) > 0 {
			logger.SafeWarn("config", "Invalid metric groups in INCLUDE filter - will be ignored", map[string]interface{}{
				"invalid_metrics": invalidIncludes,
				"valid_metrics":   validMetricGroups,
			})
		}

		logger.SafeInfo("config", "Metric filtering: INCLUDE mode active", map[string]interface{}{
			"included_metrics": c.MetricsInclude,
			"total_groups":     len(validMetricGroups),
			"note":             "EXCLUDE filter ignored when INCLUDE is specified",
		})
	}

	// Validate exclude list (only if no include list)
	if len(c.MetricsExclude) > 0 {
		invalidExcludes := []string{}
		for _, metric := range c.MetricsExclude {
			if !isValidMetricGroup(metric, validMetricGroups) {
				invalidExcludes = append(invalidExcludes, metric)
			}
		}

		if len(invalidExcludes) > 0 {
			logger.SafeWarn("config", "Invalid metric groups in EXCLUDE filter - will be ignored", map[string]interface{}{
				"invalid_metrics": invalidExcludes,
				"valid_metrics":   validMetricGroups,
			})
		}

		if len(c.MetricsInclude) > 0 {
			logger.SafeWarn("config", "EXCLUDE filter ignored - INCLUDE filter takes precedence", map[string]interface{}{
				"excluded_metrics": c.MetricsExclude,
				"behavior":         "Only INCLUDE filter will be applied",
			})
		} else {
			logger.SafeInfo("config", "Metric filtering: EXCLUDE mode active", map[string]interface{}{
				"excluded_metrics": c.MetricsExclude,
				"total_groups":     len(validMetricGroups),
			})
		}
	}

	// Default behavior when no filters
	if len(c.MetricsInclude) == 0 && len(c.MetricsExclude) == 0 {
		logger.SafeInfo("config", "Metric filtering: DEFAULT mode - collecting all metric groups", map[string]interface{}{
			"total_groups": len(validMetricGroups),
			"all_metrics":  validMetricGroups,
		})
	}
}

// isValidMetricGroup checks if a metric group name is valid
func isValidMetricGroup(metricGroup string, validGroups []string) bool {
	for _, valid := range validGroups {
		if valid == metricGroup {
			return true
		}
	}
	return false
}

// ShouldCollectMetric determines if a metric group should be collected based on include/exclude filters
func (c *Config) ShouldCollectMetric(metricGroup string) bool {
	// INCLUDE filter has absolute priority - only collect metrics in the include list
	if len(c.MetricsInclude) > 0 {
		for _, included := range c.MetricsInclude {
			if included == metricGroup {
				return true
			}
		}
		return false
	}

	// EXCLUDE filter - collect everything except excluded metrics
	if len(c.MetricsExclude) > 0 {
		for _, excluded := range c.MetricsExclude {
			if excluded == metricGroup {
				return false
			}
		}
	}

	// DEFAULT behavior: collect all metrics when no filters are specified
	return true
}

// validateDCFiltering validates the DC filtering configuration and logs appropriate messages
func (c *Config) validateDCFiltering() {
	// Validate include list
	if len(c.DCInclude) > 0 {
		logger.SafeInfo("config", "DC filtering: INCLUDE mode active", map[string]interface{}{
			"included_domains": c.DCInclude,
			"note":             "Only metrics from specified domain components will be collected",
		})

		if len(c.DCExclude) > 0 {
			logger.SafeWarn("config", "DC EXCLUDE filter ignored - INCLUDE filter takes precedence", map[string]interface{}{
				"excluded_domains": c.DCExclude,
				"behavior":         "Only DC INCLUDE filter will be applied",
			})
		}
	} else if len(c.DCExclude) > 0 {
		logger.SafeInfo("config", "DC filtering: EXCLUDE mode active", map[string]interface{}{
			"excluded_domains": c.DCExclude,
			"note":             "Metrics from specified domain components will be excluded",
		})
	} else {
		logger.SafeInfo("config", "DC filtering: DEFAULT mode - monitoring all domain components", map[string]interface{}{
			"note": "No DC filtering applied",
		})
	}
}

// ShouldMonitorDC determines if a domain component should be monitored based on include/exclude filters
func (c *Config) ShouldMonitorDC(domainComponent string) bool {
	// INCLUDE filter has absolute priority - only monitor DCs in the include list
	if len(c.DCInclude) > 0 {
		for _, included := range c.DCInclude {
			if included == domainComponent {
				return true
			}
		}
		return false
	}

	// EXCLUDE filter - monitor everything except excluded DCs
	if len(c.DCExclude) > 0 {
		for _, excluded := range c.DCExclude {
			if excluded == domainComponent {
				return false
			}
		}
	}

	// DEFAULT behavior: monitor all DCs when no filters are specified
	return true
}
