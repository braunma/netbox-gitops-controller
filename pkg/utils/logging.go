// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
)

// defaultOutput is where newly created loggers write non-error messages.
// Errors always go to stderr.
var defaultOutput io.Writer = os.Stdout

// SetDefaultOutput redirects all subsequently created loggers to w. The CLI
// uses this with --output json to keep stdout reserved for the JSON plan.
func SetDefaultOutput(w io.Writer) {
	defaultOutput = w
}

// Logger provides structured logging for the application
type Logger struct {
	dryRun bool
	out    io.Writer
}

// NewLogger creates a new logger instance
func NewLogger(dryRun bool) *Logger {
	return &Logger{dryRun: dryRun, out: defaultOutput}
}

// writer returns the logger's output, falling back to the package default
// so that nil or directly constructed loggers keep working.
func (l *Logger) writer() io.Writer {
	if l == nil || l.out == nil {
		return defaultOutput
	}
	return l.out
}

// Success logs a success message in green
func (l *Logger) Success(msg string, args ...interface{}) {
	green := color.New(color.FgGreen).SprintFunc()
	fmt.Fprintf(l.writer(), green("✓ "+msg)+"\n", args...)
}

// Info logs an informational message in cyan
func (l *Logger) Info(msg string, args ...interface{}) {
	cyan := color.New(color.FgCyan).SprintFunc()
	fmt.Fprintf(l.writer(), cyan(msg)+"\n", args...)
}

// Warning logs a warning message in yellow
func (l *Logger) Warning(msg string, args ...interface{}) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprintf(l.writer(), yellow("⚠ "+msg)+"\n", args...)
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
	fmt.Fprintf(l.writer(), dim(msg)+"\n", args...)
}

// DryRun logs a dry-run action in yellow
func (l *Logger) DryRun(action string, msg string, args ...interface{}) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprintf(l.writer(), yellow("[DRY-RUN] %s: "+msg)+"\n", append([]interface{}{action}, args...)...)
}
