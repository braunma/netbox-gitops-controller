// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// import flag backing vars.
var (
	importForce       bool
	importDryRun      bool
	importDiff        string
	importOnly        []string
	importSites       []string
	importTags        []string
	importExcludeTags []string
	importManagedOnly bool
	importSplitBy     string
	importDefaults    bool
	importDefaultsMin int
	importReport      string
	importFailOnGaps  bool
	importOutput      string
)

// newImportCommand builds the `import` subcommand: the reverse of the sync. It
// reads a live NetBox and writes this repository's native YAML, so a populated
// instance can be adopted without retyping it. It makes no write request of any
// kind.
func newImportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Generate native YAML from a live NetBox (reverse sync)",
		Long: `Generate this repository's native YAML from a live NetBox instance.

This is the brownfield-adoption path and the reverse of a sync: it reads the
instance and writes definitions and inventory that, applied back, converge with
no changes. It makes no write request of any kind — GETs and OPTIONS only.

Shared fields are hoisted into a per-file ` + "`defaults:`" + ` block automatically. What
the schema cannot express — site-less VLANs, second IPs on an interface, whole
kinds like circuits or inventory items — is written to IMPORT-REPORT.md rather
than dropped, because pruning against a partial import would delete the managed
objects it omitted.

The first sync after an import will add the gitops tag to every adopted object;
that is adoption working, not drift. Use ` + "`--adopt`" + ` on that first sync so nothing
but the tag is written. See docs/IMPORT.md.

Exit codes: 0 success (or --diff found no drift), 1 error, 2 --diff found drift.`,
		RunE:         runImport,
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&importForce, "force", false, "Write into a non-empty directory ($IMPORT_FORCE)")
	cmd.Flags().BoolVar(&importDryRun, "dry-run", false, "List files and print the report, write nothing")
	cmd.Flags().StringVar(&importDiff, "diff", "", "Diff the import against an existing repo, write nothing, exit 2 on drift")
	cmd.Flags().StringSliceVar(&importOnly, "only", nil, fmt.Sprintf("Restrict to phases (comma-separated or repeated): %s ($IMPORT_ONLY)", strings.Join(importer.Phases, ", ")))
	cmd.Flags().StringSliceVar(&importSites, "site", nil, "Restrict site-scoped source objects to these slugs ($IMPORT_SITES)")
	cmd.Flags().StringSliceVar(&importTags, "tag", nil, "Only import objects carrying these tag slugs ($IMPORT_TAGS)")
	cmd.Flags().StringSliceVar(&importExcludeTags, "exclude-tag", nil, "Skip objects carrying these tag slugs ($IMPORT_EXCLUDE_TAGS)")
	cmd.Flags().BoolVar(&importManagedOnly, "managed-only", false, "Only import objects already carrying the gitops tag ($IMPORT_MANAGED_ONLY)")
	cmd.Flags().StringVar(&importSplitBy, "split-by", "site", "How to partition inventory into files: site, rack, role, none ($IMPORT_SPLIT_BY)")
	cmd.Flags().BoolVar(&importDefaults, "defaults", true, "Hoist fields shared across a file into a defaults block ($IMPORT_DEFAULTS)")
	cmd.Flags().IntVar(&importDefaultsMin, "defaults-min-items", 3, "Fewest items in a file before any key is hoisted ($IMPORT_DEFAULTS_MIN_ITEMS)")
	cmd.Flags().StringVar(&importReport, "report", "IMPORT-REPORT.md", `Where to write the coverage report; "-" for stderr only ($IMPORT_REPORT)`)
	cmd.Flags().BoolVar(&importFailOnGaps, "fail-on-gaps", false, "Exit non-zero if the report lists any skipped object ($IMPORT_FAIL_ON_GAPS)")
	cmd.Flags().StringVar(&importOutput, "output", "text", "Output format: text or json ($IMPORT_OUTPUT)")

	return cmd
}

// importEnvBindings maps every import flag to its environment variable. The
// rewrite flags are deliberately absent (they are flag-only; see import_rewrite.go).
var importEnvBindings = map[string]string{
	"force":              "IMPORT_FORCE",
	"only":               "IMPORT_ONLY",
	"site":               "IMPORT_SITES",
	"tag":                "IMPORT_TAGS",
	"exclude-tag":        "IMPORT_EXCLUDE_TAGS",
	"managed-only":       "IMPORT_MANAGED_ONLY",
	"split-by":           "IMPORT_SPLIT_BY",
	"defaults":           "IMPORT_DEFAULTS",
	"defaults-min-items": "IMPORT_DEFAULTS_MIN_ITEMS",
	"report":             "IMPORT_REPORT",
	"fail-on-gaps":       "IMPORT_FAIL_ON_GAPS",
	"output":             "IMPORT_OUTPUT",
}

func runImport(cmd *cobra.Command, args []string) error {
	logger := utils.NewLogger(false)

	if err := loadConfigFile(configFile, cmd.Flags().Changed("config"), logger); err != nil {
		logger.Error("Failed to read the configuration file", err)
		return err
	}
	if err := applyEnvDefaults(cmd, importEnvBindings); err != nil {
		return err
	}
	if importOutput != "text" && importOutput != "json" {
		return fmt.Errorf("invalid --output %q (supported: text, json)", importOutput)
	}
	if importOutput == "json" {
		utils.SetDefaultOutput(os.Stderr)
	}
	switch importSplitBy {
	case "site", "rack", "role", "none":
	default:
		return fmt.Errorf("invalid --split-by %q (supported: site, rack, role, none)", importSplitBy)
	}

	resolvedDir, err := resolveDataDir(dataDir, cmd.Flags().Changed("data-dir"), logger)
	if err != nil {
		// A fresh import into a not-yet-existing directory is normal, so a
		// missing data dir is not fatal here.
		resolvedDir = dataDir
	}
	dataDir = resolvedDir

	netboxURL := os.Getenv("NETBOX_URL")
	netboxToken := os.Getenv("NETBOX_TOKEN")
	if netboxURL == "" || netboxToken == "" {
		logger.Error("NETBOX_URL and NETBOX_TOKEN must be set — export them, or put them in "+configFile, nil)
		return fmt.Errorf("missing required configuration: NETBOX_URL and NETBOX_TOKEN")
	}

	// Dry-run client: every request it can make is a read, and the client
	// refuses writes. Import issues none regardless; this is belt and braces.
	c, err := client.NewClient(netboxURL, netboxToken, true)
	if err != nil {
		logger.Error("Failed to initialize NetBox client", err)
		return err
	}
	if err := c.CheckVersion(); err != nil {
		logger.Error("Unsupported NetBox version", err)
		return err
	}

	opts, err := importOptions(cmd, logger)
	if err != nil {
		return err
	}

	logger.Info("Importing from %s...", netboxURL)
	result, err := importer.Import(c, opts)
	if err != nil {
		logger.Error("Import failed", err)
		return err
	}

	return finishImport(cmd, result, logger)
}

// importOptions assembles importer.Options from the resolved flags. Rewrite
// options are layered on by applyRewriteOptions (import_rewrite.go).
func importOptions(cmd *cobra.Command, logger *utils.Logger) (importer.Options, error) {
	phases := map[string]bool{}
	if len(importOnly) == 0 {
		for _, p := range importer.Phases {
			phases[p] = true
		}
	} else {
		valid := map[string]bool{}
		for _, p := range importer.Phases {
			valid[p] = true
		}
		for _, p := range importOnly {
			if !valid[p] {
				return importer.Options{}, fmt.Errorf("unknown phase %q (valid: %s)", p, strings.Join(importer.Phases, ", "))
			}
			phases[p] = true
		}
	}

	opts := importer.Options{
		Phases:      phases,
		Sites:       importSites,
		Tags:        importTags,
		ExcludeTags: importExcludeTags,
		ManagedOnly: importManagedOnly,
		SplitBy:     importSplitBy,
		Defaults:    importer.DefaultsOptions(importDefaults, importDefaultsMin),
		Logger:      logger,
	}
	if err := applyRewriteOptions(cmd, &opts, logger); err != nil {
		return importer.Options{}, err
	}
	return opts, nil
}

// finishImport writes, diffs, or previews the result and emits the report.
func finishImport(cmd *cobra.Command, result *importer.Result, logger *utils.Logger) error {
	writeReport(result, logger)

	switch {
	case importDiff != "":
		changed, err := diffResult(result, importDiff)
		if err != nil {
			logger.Error("Diff failed", err)
			return err
		}
		if changed {
			exitCode = exitChangesPending
		}
	case importDryRun:
		if importOutput == "json" {
			if err := printImportJSON(result); err != nil {
				return err
			}
		} else {
			logger.Info("Dry run — %d file(s) would be written:", len(result.Files))
			for _, f := range result.Files {
				logger.Info("  %s", f.Path)
			}
		}
	default:
		if err := result.Write(dataDir, importForce); err != nil {
			logger.Error("Failed to write the import", err)
			return err
		}
		logger.Success("Wrote %d file(s) under %s", len(result.Files), dataDir)
		if importOutput == "json" {
			if err := printImportJSON(result); err != nil {
				return err
			}
		}
	}

	logger.Info("%s", result.Report.Summary())
	if importFailOnGaps && result.Report.HasSkips() {
		return fmt.Errorf("import left %s (see the report); --fail-on-gaps set", result.Report.Summary())
	}
	return nil
}

// writeReport writes IMPORT-REPORT.md unless the report path is "-".
func writeReport(result *importer.Result, logger *utils.Logger) {
	md := result.Report.Markdown()
	if importReport == "-" {
		fmt.Fprintln(utils.DefaultOutput(), md)
		return
	}
	path := importReport
	if !filepath.IsAbs(path) {
		path = filepath.Join(dataDir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Warning("Could not create the report directory: %v", err)
		return
	}
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		logger.Warning("Could not write the report: %v", err)
		return
	}
	logger.Info("Wrote coverage report to %s", path)
}

// printImportJSON prints the file list and report summary as JSON on stdout.
func printImportJSON(result *importer.Result) error {
	paths := make([]string, 0, len(result.Files))
	for _, f := range result.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	payload := map[string]interface{}{
		"files":   paths,
		"summary": result.Report.Summary(),
		"skipped": result.Report.HasSkips(),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
