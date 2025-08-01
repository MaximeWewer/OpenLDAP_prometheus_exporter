package main

import (
	"os"
	"strconv"
	"time"
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
	Password      string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
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
		Password:      getEnvOrDefault("LDAP_PASSWORD", ""),
	}

	if config.URL == "" {
		Fatal("LDAP_URL environment variable is required", nil)
	}
	if config.Username == "" {
		Fatal("LDAP_USERNAME environment variable is required", nil)
	}
	if config.Password == "" {
		Fatal("LDAP_PASSWORD environment variable is required", nil)
	}
	if config.Timeout <= 0 {
		Fatal("LDAP_TIMEOUT must be greater than 0", nil)
	}
	if config.UpdateEvery <= 0 {
		Fatal("LDAP_UPDATE_EVERY must be greater than 0", nil)
	}

	return config, nil
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