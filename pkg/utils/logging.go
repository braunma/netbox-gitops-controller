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

// verbose decides whether Debug lines are printed. It is on unless a command
// turns it off: a sync does, so that its output is the changes it makes and
// not a line for everything it looked at.
var verbose = true

// SetVerbose turns Debug output on or off for every logger.
func SetVerbose(on bool) {
	verbose = on
}

// Verbose reports whether Debug output is printed.
func Verbose() bool {
	return verbose
}

// DefaultOutput is where loggers currently write. Anything that prints
// alongside them — a diff, a plan — uses it so that --output json moves the
// whole of a command's human-readable output off stdout together.
func DefaultOutput() io.Writer {
	return defaultOutput
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

// NewDiscardLogger creates a logger whose non-error output goes nowhere, for a
// pass whose progress messages would only repeat those of the real one.
func NewDiscardLogger() *Logger {
	return &Logger{out: io.Discard}
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

// Debug logs a detail message in dim/gray: a step that was looked at and
// needed nothing. It is printed only while verbose output is on.
func (l *Logger) Debug(msg string, args ...interface{}) {
	if !verbose {
		return
	}
	dim := color.New(color.Faint).SprintFunc()
	fmt.Fprintf(l.writer(), dim(msg)+"\n", args...)
}

// DryRun logs a dry-run action in yellow
func (l *Logger) DryRun(action string, msg string, args ...interface{}) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprintf(l.writer(), yellow("[DRY-RUN] %s: "+msg)+"\n", append([]interface{}{action}, args...)...)
}
