// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/lint"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
	"github.com/braunma/netbox-gitops-controller/pkg/validate"
)

// skipReferences is the --skip-references flag of the validate subcommand.
var skipReferences bool

// newValidateCommand builds the `validate` subcommand.
//
// It sits between `yamlcheck`, which knows nothing of the server, and
// `--dry-run`, which plans the whole sync: it asks the instance that will
// receive the data which values it accepts and which references resolve, and
// reports everything wrong at once instead of stopping at the first rejection.
// It makes no write requests of any kind.
func newValidateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the YAML against a live NetBox without writing to it",
		Long: `Check the YAML against a live NetBox without writing to it.

Every value the YAML sets for a NetBox choice field — interface and cable
types, statuses, faces, modes, airflow — is checked against what the target
instance actually offers in its OPTIONS response, which is the only authority
on the matter: the choices depend on the release and its plugins. String
lengths are checked against the same source.

References the repository does not declare itself are looked up in NetBox, so
a site or device type that exists on the server is accepted and one that exists
nowhere is reported.

Exit codes: 0 when everything checks out, 1 when something does not.`,
		RunE:         runValidate,
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&skipReferences, "skip-references", false,
		"Check values only, making no existence queries")
	return cmd
}

func runValidate(cmd *cobra.Command, args []string) error {
	logger := utils.NewLogger(false)

	if err := loadConfigFile(configFile, cmd.Flags().Changed("config"), logger); err != nil {
		logger.Error("Failed to read the configuration file", err)
		return err
	}

	resolvedDir, err := resolveDataDir(dataDir, cmd.Flags().Changed("data-dir"), logger)
	if err != nil {
		logger.Error("Failed to resolve data directory", err)
		return err
	}
	dataDir = resolvedDir

	netboxURL := os.Getenv("NETBOX_URL")
	netboxToken := os.Getenv("NETBOX_TOKEN")
	if netboxURL == "" || netboxToken == "" {
		logger.Error(fmt.Sprintf(
			"NETBOX_URL and NETBOX_TOKEN must be set — export them, or put them in %s (copy .env.example)",
			configFile), nil)
		return fmt.Errorf("missing required configuration: NETBOX_URL and NETBOX_TOKEN")
	}

	// Dry-run mode, so that a mistake in this path cannot write: every request
	// it makes is a read, and the client refuses the rest.
	c, err := client.NewClient(netboxURL, netboxToken, true)
	if err != nil {
		logger.Error("Failed to initialize NetBox client", err)
		return err
	}
	if err := c.CheckVersion(); err != nil {
		logger.Error("Unsupported NetBox version", err)
		return err
	}

	patterns, err := resolveIgnorePatterns()
	if err != nil {
		return err
	}
	dl := loader.NewDataLoader(dataDir, logger)
	dl.SetIgnorePatterns(patterns)

	dataset, err := dl.LoadDataset(loader.DatasetOptions{
		DeviceTypeLibrary: resolveDeviceTypeLibrary(),
		ModuleTypeLibrary: resolveModuleTypeLibrary(),
	})
	if err != nil {
		logger.Error("Failed to load the data directory", err)
		return err
	}

	logger.Info("Validating against %s...", netboxURL)
	findings, err := validate.Check(dataset, c, c, validate.Options{SkipReferences: skipReferences})
	if err != nil {
		logger.Error("Validation could not run", err)
		return err
	}

	return reportFindings(findings, logger)
}

// reportFindings prints the findings and turns them into an exit code.
func reportFindings(findings []lint.Finding, logger *utils.Logger) error {
	var errors, warnings int
	for _, f := range findings {
		switch f.Severity {
		case lint.Error:
			errors++
			logger.Error(fmt.Sprintf("%s: %s (%s)", f.Object, f.Message, f.Check), nil)
		default:
			warnings++
			logger.Warning("%s: %s (%s)", f.Object, f.Message, f.Check)
		}
	}

	if errors == 0 {
		if warnings == 0 {
			logger.Success("Everything the YAML declares is accepted by this NetBox")
		} else {
			logger.Success("No errors (%d warning(s))", warnings)
		}
		return nil
	}

	return fmt.Errorf("%d value(s) or reference(s) this NetBox will not accept", errors)
}
