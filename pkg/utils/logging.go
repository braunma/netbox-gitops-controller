// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
)

// defaultOutput is where newly created loggers write non-error messages.
// Errors always go to stderr.
var defaultOutput io.Writer = os.Stdout

// logFormat selects how newly created loggers render each line.
type logFormat int

const (
	// FormatText renders human-readable, optionally coloured lines.
	FormatText logFormat = iota
	// FormatJSON renders one JSON object per line: {"level","msg"}.
	FormatJSON
)

// defaultFormat is the format newly created loggers use.
var defaultFormat = FormatText

// SetDefaultOutput redirects all subsequently created loggers to w. The CLI
// uses this with --output json to keep stdout reserved for the JSON plan.
func SetDefaultOutput(w io.Writer) {
	defaultOutput = w
}

// DefaultOutput is where loggers currently write. Anything that prints
// alongside them — a diff, a plan — uses it so that --output json moves the
// whole of a command's human-readable output off stdout together.
func DefaultOutput() io.Writer {
	return defaultOutput
}

// SetLogFormat selects the render format for subsequently created loggers.
// A pipeline sets FormatJSON so every line is machine-parseable; --no-color or
// a non-TTY stream already strips colour from FormatText via SetColor.
func SetLogFormat(f logFormat) {
	defaultFormat = f
}

// SetColor forces colour on or off for all output. fatih/color already
// disables colour when stdout is not a TTY and when NO_COLOR is set; this lets
// --no-color make the choice explicit regardless.
func SetColor(enabled bool) {
	color.NoColor = !enabled
}

// Logger provides structured logging for the application
type Logger struct {
	dryRun bool
	out    io.Writer
	format logFormat
}

// NewLogger creates a new logger instance
func NewLogger(dryRun bool) *Logger {
	return &Logger{dryRun: dryRun, out: defaultOutput, format: defaultFormat}
}

// writer returns the logger's output, falling back to the package default
// so that nil or directly constructed loggers keep working.
func (l *Logger) writer() io.Writer {
	if l == nil || l.out == nil {
		return defaultOutput
	}
	return l.out
}

// emit renders one line to w at the given level. In JSON mode the colouring
// function and prefix are ignored and a {"level","msg"} object is written; in
// text mode the message is coloured and prefixed as before.
func (l *Logger) emit(w io.Writer, level, prefix string, colour func(a ...interface{}) string, msg string, args ...interface{}) {
	text := fmt.Sprintf(msg, args...)
	if l != nil && l.format == FormatJSON {
		line, err := json.Marshal(map[string]string{"level": level, "msg": text})
		if err != nil {
			// A message that will not marshal is still worth emitting.
			fmt.Fprintf(w, `{"level":%q,"msg":%q}`+"\n", level, text)
			return
		}
		fmt.Fprintln(w, string(line))
		return
	}
	fmt.Fprintln(w, colour(prefix+text))
}

// Success logs a success message in green
func (l *Logger) Success(msg string, args ...interface{}) {
	l.emit(l.writer(), "success", "✓ ", color.New(color.FgGreen).SprintFunc(), msg, args...)
}

// Info logs an informational message in cyan
func (l *Logger) Info(msg string, args ...interface{}) {
	l.emit(l.writer(), "info", "", color.New(color.FgCyan).SprintFunc(), msg, args...)
}

// Warning logs a warning message in yellow
func (l *Logger) Warning(msg string, args ...interface{}) {
	l.emit(l.writer(), "warning", "⚠ ", color.New(color.FgYellow).SprintFunc(), msg, args...)
}

// Error logs an error message in red. Errors always go to stderr.
func (l *Logger) Error(msg string, err error, args ...interface{}) {
	if err != nil {
		l.emit(os.Stderr, "error", "✗ ", color.New(color.FgRed).SprintFunc(), msg+": %v", append(args, err)...)
		return
	}
	l.emit(os.Stderr, "error", "✗ ", color.New(color.FgRed).SprintFunc(), msg, args...)
}

// Debug logs a debug message in dim/gray
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.emit(l.writer(), "debug", "", color.New(color.Faint).SprintFunc(), msg, args...)
}

// DryRun logs a dry-run action in yellow
func (l *Logger) DryRun(action string, msg string, args ...interface{}) {
	l.emit(l.writer(), "dry-run", "", color.New(color.FgYellow).SprintFunc(),
		"[DRY-RUN] "+action+": "+msg, args...)
}
