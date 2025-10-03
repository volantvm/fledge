// Package logging provides structured logging utilities for Fledge.
package logging

import (
	"io"
	"log/slog"
	"os"
)

var (
	// Logger is the global structured logger instance.
	Logger *slog.Logger
)

// InitLogger initializes the global logger with the specified verbosity.
func InitLogger(verbose bool, quiet bool) {
	var level slog.Level
	var output io.Writer = os.Stdout

	if quiet {
		level = slog.LevelError
	} else if verbose {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	handler := slog.NewTextHandler(output, opts)
	Logger = slog.New(handler)
	slog.SetDefault(Logger)
}

// Info logs an informational message.
func Info(msg string, args ...any) {
	if Logger != nil {
		Logger.Info(msg, args...)
	}
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	if Logger != nil {
		Logger.Debug(msg, args...)
	}
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	if Logger != nil {
		Logger.Warn(msg, args...)
	}
}

// Error logs an error message.
func Error(msg string, args ...any) {
	if Logger != nil {
		Logger.Error(msg, args...)
	}
}

// Fatal logs an error message and exits the program.
func Fatal(msg string, args ...any) {
	if Logger != nil {
		Logger.Error(msg, args...)
	}
	os.Exit(1)
}
