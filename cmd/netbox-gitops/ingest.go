// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/collectors"
	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/ingest"
	"github.com/braunma/netbox-gitops-controller/pkg/ingestfmt/idracjson"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Flags shared by `collect` and `ingest`. They are the same pipeline behind
// two front doors — one that goes and fetches a snapshot, one that is handed
// one — so they take the same options and print the same summary.
var (
	ingestDryRun           bool
	ingestOutput           string
	ingestDetailedExitcode bool
	customFieldPrefix      string

	collectorsConfig string
	collectSources   []string

	ingestFormat string
	ingestInput  string
	ingestSource string
)

// ingestFormats are the external document formats `ingest` understands.
var ingestFormats = []string{"idrac-json"}

// newCollectCommand builds the `collect` subcommand.
func newCollectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Read the configured sources and write what they report into the YAML",
		Long: `Read the configured sources and write what they report into the YAML.

Each source in collectors.yaml is scanned read-only, and what it reports is
resolved against the inventory this repository declares:

  - a machine the repository already declares has its facts written into the
    file that declares it, at its own block, touching only the keys a collector
    owns;
  - a virtual machine the repository has never heard of is written as new,
    appliable YAML under ` + ingest.DiscoveredVMDir + `/<source>/;
  - a physical machine the repository has never heard of is written as a parked
    skeleton, because a scan cannot know where a machine stands.

Nothing here writes to NetBox. The changes land in this repository, are
reviewed as a merge request diff, and become NetBox truth through a normal
sync — the same route every hand-written line takes.

Exit codes: 0 on success, 1 on error, and with --detailed-exitcode, 2 when the
repository would change.`,
		RunE:         runCollect,
		SilenceUsage: true,
	}

	addIngestFlags(cmd)
	cmd.Flags().StringVar(&collectorsConfig, "collectors-config", "",
		fmt.Sprintf("Path to the collector source configuration (default: $COLLECTORS_CONFIG, else <data-dir>/%s)", collectors.DefaultConfigFile))
	cmd.Flags().StringSliceVar(&collectSources, "source", nil,
		"Restrict the run to named sources (comma-separated or repeated); default is every configured source")
	return cmd
}

// newIngestCommand builds the `ingest` subcommand.
func newIngestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Read a scan document produced elsewhere and write it into the YAML",
		Long: `Read a scan document produced elsewhere and write it into the YAML.

Where ` + "`collect`" + ` fetches a snapshot itself, this is handed one: the JSON that
the sibling idrac-netbox-importer writes with ` + "`-output json`" + `. A CI job runs the
importer container, keeps its output as an artifact, and passes it here.

Everything after that is identical to ` + "`collect`" + `, including the rule that matters
most: this writes YAML and nothing else. The importer has its own
direct-to-NetBox mode; this project does not use it, because a fact that
reaches NetBox without passing a merge request has not been reviewed.

Exit codes: 0 on success, 1 on error, and with --detailed-exitcode, 2 when the
repository would change.`,
		RunE:         runIngest,
		SilenceUsage: true,
	}

	addIngestFlags(cmd)
	cmd.Flags().StringVar(&ingestFormat, "format", "",
		fmt.Sprintf("Format of the input document (required): %s", strings.Join(ingestFormats, ", ")))
	cmd.Flags().StringVar(&ingestInput, "input", "",
		"Path to the input document (required; '-' reads standard input)")
	cmd.Flags().StringVar(&ingestSource, "source", "",
		"Source name to record on the snapshot; it names the directory generated files land in (default: the format's own)")
	return cmd
}

// addIngestFlags registers what both commands share.
func addIngestFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&ingestDryRun, "dry-run", false,
		"Print the file diffs and write nothing")
	cmd.Flags().StringVar(&ingestOutput, "output", "text",
		"Output format: 'text' or 'json' (json prints the change list to stdout and moves logs to stderr)")
	cmd.Flags().BoolVar(&ingestDetailedExitcode, "detailed-exitcode", false,
		"Exit with code 2 when the repository would change, so CI can decide to open a merge request")
	cmd.Flags().StringVar(&customFieldPrefix, "custom-field-prefix", ingest.DefaultCustomFieldPrefix,
		"Prefix of the custom fields a scan owns; nothing outside it is ever written")
}

func runCollect(cmd *cobra.Command, args []string) error {
	logger, err := prepareIngestRun(cmd)
	if err != nil {
		return err
	}

	cfg, err := collectors.LoadConfig(resolveCollectorsConfig())
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no collector configuration at %s — copy collectors.yaml.example and name your sources there",
				resolveCollectorsConfig())
		}
		logger.Error("Failed to read the collector configuration", err)
		return err
	}

	sources, err := cfg.Select(collectSources)
	if err != nil {
		logger.Error("Unknown source", err)
		return err
	}
	if len(sources) == 0 {
		logger.Warning("The collector configuration declares no sources; nothing to do")
		return nil
	}

	// Each source is planned and applied on its own, so one unreachable
	// source's failure does not discard what the others found. The exit code
	// still reports the failure.
	var runErrs []error
	changed := false
	for _, src := range sources {
		collector, err := collectors.New(src, logger)
		if err != nil {
			logger.Error("Failed to build collector "+src.Name, err)
			runErrs = append(runErrs, err)
			continue
		}

		logger.Info("Collecting from %s (%s)...", src.Name, src.URL)
		snap, err := collector.Collect(cmd.Context())
		if err != nil {
			logger.Error("Failed to collect from "+src.Name, err)
			runErrs = append(runErrs, err)
			continue
		}

		didChange, err := processSnapshot(snap, logger)
		if err != nil {
			logger.Error("Failed to write what "+src.Name+" reported", err)
			runErrs = append(runErrs, err)
			continue
		}
		changed = changed || didChange
	}

	if len(runErrs) > 0 {
		return fmt.Errorf("%d of %d source(s) failed; see the errors above", len(runErrs), len(sources))
	}
	if ingestDetailedExitcode && changed {
		exitCode = 2
	}
	return nil
}

func runIngest(cmd *cobra.Command, args []string) error {
	if ingestFormat == "" {
		return fmt.Errorf("--format is required (supported: %s)", strings.Join(ingestFormats, ", "))
	}
	if ingestFormat != "idrac-json" {
		return fmt.Errorf("unsupported --format %q (supported: %s)", ingestFormat, strings.Join(ingestFormats, ", "))
	}
	if ingestInput == "" {
		return fmt.Errorf("--input is required (a document written by `idrac-inventory -output json`, or '-' for standard input)")
	}

	logger, err := prepareIngestRun(cmd)
	if err != nil {
		return err
	}

	reader := io.Reader(os.Stdin)
	if ingestInput != "-" {
		f, err := os.Open(ingestInput) // #nosec G304 -- the path is the operator's argument
		if err != nil {
			logger.Error("Failed to open the input document", err)
			return err
		}
		defer f.Close() //nolint:errcheck // read-only
		reader = f
	}

	snap, err := idracjson.Parse(reader, idracjson.Options{
		Source: ingestSource,
		Warnf:  func(format string, args ...interface{}) { logger.Warning(format, args...) },
	})
	if err != nil {
		logger.Error("Failed to read the input document", err)
		return err
	}
	logger.Info("Read %d server(s) from %s", len(snap.Devices), describeInput())

	changed, err := processSnapshot(snap, logger)
	if err != nil {
		logger.Error("Failed to write what the scan reported", err)
		return err
	}
	if ingestDetailedExitcode && changed {
		exitCode = 2
	}
	return nil
}

// describeInput names the input for a log line.
func describeInput() string {
	if ingestInput == "-" {
		return "standard input"
	}
	return ingestInput
}

// prepareIngestRun does what both commands need before they touch anything:
// validate the shared flags, read the config file, resolve the data directory
// and build a logger.
func prepareIngestRun(cmd *cobra.Command) (*utils.Logger, error) {
	if ingestOutput != "text" && ingestOutput != "json" {
		return nil, fmt.Errorf("invalid --output format %q (supported: text, json)", ingestOutput)
	}
	if ingestOutput == "json" {
		// Reserve stdout for the change list.
		utils.SetDefaultOutput(os.Stderr)
	}

	logger := utils.NewLogger(ingestDryRun)

	if err := loadConfigFile(configFile, cmd.Flags().Changed("config"), logger); err != nil {
		logger.Error("Failed to read the configuration file", err)
		return nil, err
	}

	resolvedDir, err := resolveDataDir(dataDir, cmd.Flags().Changed("data-dir"), logger)
	if err != nil {
		logger.Error("Failed to resolve data directory", err)
		return nil, err
	}
	dataDir = resolvedDir

	return logger, nil
}

// processSnapshot resolves one snapshot against the declared inventory, prints
// what it would do and, unless this is a dry run, writes it.
func processSnapshot(snap *discovery.Snapshot, logger *utils.Logger) (bool, error) {
	dl := loader.NewDataLoader(dataDir, logger)
	patterns, err := resolveIgnorePatterns()
	if err != nil {
		return false, err
	}
	dl.SetIgnorePatterns(patterns)

	// Parked files are loaded here on purpose: an object declared in one is
	// still declared, and generating a second copy of it would be worse than
	// useless. LoadInventoryWithProvenance handles that itself.
	inv, err := dl.LoadInventoryWithProvenance()
	if err != nil {
		return false, err
	}

	plan, err := ingest.BuildPlan(snap, inv, ingest.Options{
		DataDir:           dataDir,
		CustomFieldPrefix: customFieldPrefix,
	})
	if err != nil {
		return false, err
	}

	if ingestDryRun {
		for _, change := range plan.Changes {
			fmt.Fprint(utils.DefaultOutput(), change.Diff())
		}
	} else if err := ingest.Apply(plan); err != nil {
		return false, err
	}

	reportIngestPlan(plan, logger)

	if ingestOutput == "json" {
		if err := writeIngestJSON(os.Stdout, snap, plan, ingestDryRun); err != nil {
			return false, err
		}
	}

	return plan.Summary.HasChanges(), nil
}

// reportIngestPlan prints the run summary in the same shape as the sync's plan
// line, so both commands read the same way in a pipeline log.
func reportIngestPlan(plan *ingest.Plan, logger *utils.Logger) {
	s := plan.Summary

	logger.Info("═══════════════════════════════════════════════════════")
	if ingestDryRun {
		logger.Warning("DRY RUN COMPLETE: No files written")
	} else if s.HasChanges() {
		logger.Success("INGEST COMPLETE: %d file(s) written", len(plan.Changes))
	} else {
		logger.Success("INGEST COMPLETE: the repository already says what the scan found")
	}
	logger.Info("Facts: %d updated, %d new VMs, %d parked devices, %d unchanged",
		s.FactsUpdated, s.NewVMs, s.ParkedDevices, s.Unchanged)
	if s.Adopted > 0 {
		logger.Info("Adopted: %d object(s) removed from a generated file because you now declare them yourself", s.Adopted)
	}
	logger.Info("═══════════════════════════════════════════════════════")

	// A parked file nobody notices is a parked file nobody finishes, so they
	// are named rather than counted.
	if len(plan.Parked) > 0 {
		logger.Warning("%d scanned machine(s) are not declared in this repository and were parked for completion:", len(plan.Parked))
		for _, path := range plan.Parked {
			logger.Warning("  %s", path)
		}
		logger.Warning("  Fill in the TODO fields, drop the leading underscore, and run `go run ./cmd/yamlcheck`.")
	}

	if !ingestDryRun && s.HasChanges() {
		logger.Info("Commit these files and open a merge request; the sync applies them once it is merged.")
	}
}

// writeIngestJSON emits the change list for a pipeline to act on.
func writeIngestJSON(w io.Writer, snap *discovery.Snapshot, plan *ingest.Plan, dryRun bool) error {
	out := struct {
		DryRun  bool            `json:"dry_run"`
		Source  string          `json:"source"`
		Summary ingest.Summary  `json:"summary"`
		Changes []ingest.Change `json:"changes"`
		Parked  []string        `json:"parked,omitempty"`
	}{dryRun, snap.Source, plan.Summary, plan.Changes, plan.Parked}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// resolveCollectorsConfig returns the collector configuration path: the
// --collectors-config flag, else COLLECTORS_CONFIG, else the conventional path
// inside the data directory.
func resolveCollectorsConfig() string {
	if collectorsConfig != "" {
		return collectorsConfig
	}
	if env := os.Getenv("COLLECTORS_CONFIG"); env != "" {
		return env
	}
	return filepath.Join(dataDir, collectors.DefaultConfigFile)
}
