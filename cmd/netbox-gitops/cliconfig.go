// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Exit codes are uniform across every subcommand, so a pipeline can branch on
// them without parsing output. Documented in docs/CI_CD.md.
const (
	exitOK             = 0 // success, or --dry-run/--diff found nothing pending
	exitError          = 1 // any error
	exitChangesPending = 2 // --detailed-exitcode / --diff: changes are pending
	exitGuardViolation = 3 // a safety guard (e.g. --assert-site) refused the run
)

// guardError marks a failure that a safety guard raised, so main can map it to
// the dedicated exit code 3 rather than the generic 1. It is distinct from an
// ordinary error precisely so a pipeline can tell "the run was refused for
// safety" from "the run broke".
type guardError struct{ err error }

func (e *guardError) Error() string { return e.err.Error() }
func (e *guardError) Unwrap() error { return e.err }

// newGuardError wraps err as a guard violation.
func newGuardError(format string, args ...interface{}) error {
	return &guardError{fmt.Errorf(format, args...)}
}

// exitCodeFor turns a command error into the process exit code, honouring the
// guard-violation and changes-pending sentinels.
func exitCodeFor(err error) int {
	if err == nil {
		return exitCode // may have been set to 2 by --detailed-exitcode
	}
	var g *guardError
	if errors.As(err, &g) {
		return exitGuardViolation
	}
	var sg *client.SiteGuardError
	if errors.As(err, &sg) {
		return exitGuardViolation
	}
	return exitError
}

// logFormatFlag and noColorFlag back the persistent presentation flags.
var (
	logFormatFlag string
	noColorFlag   bool
)

// addPresentationFlags registers the presentation flags shared by every
// command on the root's persistent set.
func addPresentationFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&logFormatFlag, "log-format", "text",
		"Log format: 'text' or 'json' ($LOG_FORMAT)")
	cmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false,
		"Disable ANSI colour (also honoured: $NO_COLOR, and a non-TTY stdout)")
}

// applyPresentation resolves the presentation flags against the environment
// and configures the logger package. It runs from the root PersistentPreRunE,
// so it applies to every subcommand.
//
// The config file is read first (and only once per process — each command's own
// RunE calls loadConfigFile again, which is a no-op for variables already set),
// so that LOG_FORMAT and NO_COLOR obey the same flag > env > .env > default
// precedence as every other setting rather than silently ignoring the file.
func applyPresentation(cmd *cobra.Command) error {
	path := configFile
	if f := cmd.Flags().Lookup("config"); f != nil && f.Value.String() != "" {
		path = f.Value.String()
	}
	// A missing or malformed file is reported by the command's own
	// loadConfigFile, which runs later with a configured logger; here it must
	// not abort presentation setup.
	_ = loadConfigFile(path, false, utils.NewLogger(false))

	format := resolveStringFlag(cmd, "log-format", logFormatFlag, "LOG_FORMAT")
	switch format {
	case "text":
		utils.SetLogFormat(utils.FormatText)
	case "json":
		utils.SetLogFormat(utils.FormatJSON)
	default:
		return fmt.Errorf("invalid --log-format %q (supported: text, json)", format)
	}

	// --no-color, $NO_COLOR (any non-empty value), or a flag win over the
	// library's TTY auto-detection. Otherwise leave fatih/color's default.
	if noColorFlag || cmd.Flags().Changed("no-color") {
		utils.SetColor(false)
	} else if _, ok := os.LookupEnv("NO_COLOR"); ok {
		utils.SetColor(false)
	}
	return nil
}

// resolveStringFlag returns the flag value when it was set on the command
// line, else the environment variable when set, else the flag's current
// (default) value. This is the flag > env > .env > default precedence: the
// .env file is loaded into the environment before this runs, so it is folded
// into the env lookup.
func resolveStringFlag(cmd *cobra.Command, name, current, env string) string {
	if cmd.Flags().Changed(name) {
		return current
	}
	if v, ok := os.LookupEnv(env); ok {
		return v
	}
	return current
}

// applyEnvDefaults fills each flag from its environment variable when the flag
// was not given on the command line. Repeatable (slice) flags read a
// comma-separated list. It must run after the config file is loaded into the
// environment, so a value in .env is visible here.
//
// Precedence per flag: command line > environment (incl. .env) > default.
func applyEnvDefaults(cmd *cobra.Command, bindings map[string]string) error {
	for name, env := range bindings {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			return fmt.Errorf("internal: no flag %q to bind to %s", name, env)
		}
		if cmd.Flags().Changed(name) {
			continue
		}
		value, ok := os.LookupEnv(env)
		if !ok || value == "" {
			continue
		}
		if err := setFlagFromEnv(cmd, flag.Name, value); err != nil {
			return fmt.Errorf("%s=%q: %w", env, value, err)
		}
	}
	return nil
}

// setFlagFromEnv applies an environment value to a flag. A slice flag is set
// element by element so a comma-separated variable produces the same result as
// repeating the flag; every other flag type takes the value whole.
func setFlagFromEnv(cmd *cobra.Command, name, value string) error {
	flag := cmd.Flags().Lookup(name)
	if strings.HasPrefix(flag.Value.Type(), "stringSlice") {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				if err := flag.Value.Set(part); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return flag.Value.Set(value)
}
