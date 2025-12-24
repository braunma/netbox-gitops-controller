package utils

import (
	"fmt"
	"github.com/fatih/color"
	"os"
)

// Logger provides structured logging for the application
type Logger struct {
	dryRun bool
}

// NewLogger creates a new logger instance
func NewLogger(dryRun bool) *Logger {
	return &Logger{dryRun: dryRun}
}

// Success logs a success message in green
func (l *Logger) Success(msg string, args ...interface{}) {
	green := color.New(color.FgGreen).SprintFunc()
	fmt.Fprintf(os.Stdout, green("✓ "+msg)+"\n", args...)
}

// Info logs an informational message in cyan
func (l *Logger) Info(msg string, args ...interface{}) {
	cyan := color.New(color.FgCyan).SprintFunc()
	fmt.Fprintf(os.Stdout, cyan(msg)+"\n", args...)
}

// Warning logs a warning message in yellow
func (l *Logger) Warning(msg string, args ...interface{}) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprintf(os.Stdout, yellow("⚠ "+msg)+"\n", args...)
}

// Error logs an error message in red
func (l *Logger) Error(msg string, err error, args ...interface{}) {
	red := color.New(color.FgRed).SprintFunc()
	if err != nil {
		fmt.Fprintf(os.Stderr, red("✗ "+msg+": %v")+"\n", append(args, err)...)
	} else {
		fmt.Fprintf(os.Stderr, red("✗ "+msg)+"\n", args...)
	}
}

// Debug logs a debug message in dim/gray
func (l *Logger) Debug(msg string, args ...interface{}) {
	dim := color.New(color.Faint).SprintFunc()
	fmt.Fprintf(os.Stdout, dim(msg)+"\n", args...)
}

// DryRun logs a dry-run action in yellow
func (l *Logger) DryRun(action string, msg string, args ...interface{}) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprintf(os.Stdout, yellow("[DRY-RUN] %s: "+msg)+"\n", append([]interface{}{action}, args...)...)
}
