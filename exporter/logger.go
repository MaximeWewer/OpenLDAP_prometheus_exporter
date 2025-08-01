package main

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// NewLogger creates a new logger instance with the specified component name
func NewLogger(component string) zerolog.Logger {
	return log.With().Str("component", component).Logger()
}

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

// Debug logs a debug message with optional structured fields using global logger
func Debug(message string, fields ...map[string]interface{}) {
	event := log.Debug()
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Info logs an info message with optional structured fields using global logger
func Info(message string, fields ...map[string]interface{}) {
	event := log.Info()
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Warn logs a warning message with optional structured fields using global logger
func Warn(message string, fields ...map[string]interface{}) {
	event := log.Warn()
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Error logs an error message with the error and optional structured fields using global logger
func Error(message string, err error, fields ...map[string]interface{}) {
	event := log.Error().Err(err)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}

// Fatal logs a fatal error message and terminates the program using global logger
func Fatal(message string, err error, fields ...map[string]interface{}) {
	event := log.Fatal().Err(err)
	if len(fields) > 0 {
		for k, v := range fields[0] {
			event = event.Interface(k, v)
		}
	}
	event.Msg(message)
}