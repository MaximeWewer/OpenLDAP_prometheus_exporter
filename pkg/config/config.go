package config

import (
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

	// Rate limiting options
	RateLimitEnabled bool // Enable/disable rate limiting (RATE_LIMIT_ENABLED)
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	// Get password and create secure string
	passwordValue := getEnvOrDefault("LDAP_PASSWORD", "")
	var securePassword *SecureString
	if passwordValue != "" {
		var err error
		securePassword, err = NewSecureString(passwordValue)
		if err != nil {
			logger.Fatal("config", "Failed to create secure password storage", err)
		}
		// Password is now securely stored in SecureString
	}

	config := &Config{
		URL:           getEnvOrDefault("LDAP_URL", ""),
		ServerName:    getEnvOrDefault("LDAP_SERVER_NAME", "openldap"),
		TLS:           getEnvBoolOrDefault("LDAP_TLS", false),
		TLSSkipVerify: getEnvBoolOrDefault("LDAP_TLS_SKIP_VERIFY", false),
		TLSCA:         getEnvOrDefault("LDAP_TLS_CA", ""),
		TLSCert:       getEnvOrDefault("LDAP_TLS_CERT", ""),
		TLSKey:        getEnvOrDefault("LDAP_TLS_KEY", ""),
		Timeout:       time.Duration(getEnvIntOrDefault("LDAP_TIMEOUT", 10)) * time.Second,
		UpdateEvery:   time.Duration(getEnvIntOrDefault("LDAP_UPDATE_EVERY", 15)) * time.Second,
		Username:      getEnvOrDefault("LDAP_USERNAME", ""),
		Password:      securePassword,
	}

	if config.URL == "" {
		logger.Fatal("config", "LDAP_URL environment variable is required", nil)
	}
	if config.Username == "" {
		logger.Fatal("config", "LDAP_USERNAME environment variable is required", nil)
	}
	if config.Password == nil || config.Password.IsEmpty() {
		logger.Fatal("config", "LDAP_PASSWORD environment variable is required", nil)
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

	// Load rate limiting configuration
	config.RateLimitEnabled = getEnvBoolOrDefault("RATE_LIMIT_ENABLED", true)

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
		"database", "server", "log", "sasl",
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
