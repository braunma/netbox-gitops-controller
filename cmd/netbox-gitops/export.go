// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/export"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

var (
	exportOut    string
	exportSites  []string
	exportForce  bool
	exportDryRun bool
	exportDetail bool
)

// newExportCommand builds the `export` subcommand.
func newExportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write what NetBox already holds as YAML, so it can be brought under GitOps",
		Long: `Write what NetBox already holds as YAML, so it can be brought under GitOps.

Every object the repository does not declare yet — whether it was created by
hand, by another tool, or by this controller and since dropped from the YAML —
is written into definitions/ and inventory/ under the identity it already has.
Merging the files makes the next sync adopt those objects: it finds each one,
stamps the gitops tag on it, and manages it from then on. For an object
exported unchanged, the sync changes nothing else; run --dry-run to see that.

Files are named imported*.yaml. An existing file is never overwritten unless
--force is given. Nothing is written to NetBox: the export only reads.

Everything NetBox holds that the YAML cannot express is reported at the end.
Read those lines before merging.`,
		RunE:         runExport,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&exportOut, "out", "", "Directory to write into (default: the data directory)")
	cmd.Flags().StringSliceVar(&exportSites, "site", nil, "Only export objects at these site slugs (comma-separated or repeated); shared definitions are always exported")
	cmd.Flags().BoolVar(&exportForce, "force", false, "Overwrite files that already exist")
	cmd.Flags().BoolVar(&exportDryRun, "dry-run", false, "List the files that would be written, and write nothing")
	cmd.Flags().BoolVar(&exportDetail, "detailed-exitcode", false, "Exit with code 2 when there was something to export; 0 means the repository already declares everything")
	return cmd
}

func runExport(cmd *cobra.Command, args []string) error {
	logger := utils.NewLogger(false)

	if err := loadConfigFile(configFile, cmd.Flags().Changed("config"), logger); err != nil {
		logger.Error("Failed to read the configuration file", err)
		return err
	}

	netboxURL := os.Getenv("NETBOX_URL")
	netboxToken := os.Getenv("NETBOX_TOKEN")
	if netboxURL == "" || netboxToken == "" {
		logger.Error(fmt.Sprintf(
			"NETBOX_URL and NETBOX_TOKEN must be set — export them, or put them in %s (copy .env.example)",
			configFile), nil)
		return fmt.Errorf("missing required configuration: NETBOX_URL and NETBOX_TOKEN")
	}

	// What the repository already declares is not exported again. Unlike the
	// sync, an export does not fall back to example/: it would write there.
	// Parked files count as declared, too: exporting their objects again
	// would declare them twice the moment the file is unparked.
	dl := loader.NewDataLoader(dataDir, logger)
	dl.SetIgnorePatterns(nil)
	declared, err := dl.LoadDataset(loader.DatasetOptions{
		DeviceTypeLibrary: resolveDeviceTypeLibrary(),
		ModuleTypeLibrary: resolveModuleTypeLibrary(),
	})
	if err != nil {
		return reportLoadProblems(logger, err, "nothing was exported: the export needs to know what the repository already declares")
	}

	// Dry-run mode, so that this path cannot write to NetBox.
	c, err := client.NewClient(netboxURL, netboxToken, true)
	if err != nil {
		logger.Error("Failed to initialize NetBox client", err)
		return err
	}
	if err := c.CheckVersion(); err != nil {
		logger.Error("Unsupported NetBox version", err)
		return err
	}

	res, err := export.Export(c, export.Options{
		Declared: &declared,
		Sites:    exportSites,
		Progress: logger.Info,
	})
	if err != nil {
		logger.Error("Export failed", err)
		return err
	}

	out := exportOut
	if out == "" {
		out = dataDir
	}
	if err := writeExport(os.Stdout, out, res, exportForce, exportDryRun); err != nil {
		logger.Error("Nothing was written", err)
		return err
	}
	if exportDetail && len(res.Files) > 0 {
		exitCode = 2
	}
	return nil
}

// writeExport writes the files of an export under dir and prints what it
// did. It checks every file before writing any, so a refusal leaves no
// half-written export behind.
func writeExport(w io.Writer, dir string, res *export.Result, force, dryRun bool) error {
	var conflicts []string
	for _, f := range res.Files {
		if _, err := os.Stat(filepath.Join(dir, f.Path)); err == nil && !force {
			conflicts = append(conflicts, f.Path)
		}
	}
	if len(conflicts) > 0 && !dryRun {
		return fmt.Errorf("%d file(s) already exist, pass --force to overwrite them or --out to write elsewhere:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "))
	}

	verb := "Wrote"
	if dryRun {
		verb = "Would write"
	}
	if len(res.Files) == 0 {
		fmt.Fprintln(w, "Nothing to export: the repository already declares everything this NetBox holds.")
	} else {
		fmt.Fprintf(w, "%s %d file(s) under %s:\n", verb, len(res.Files), dir)
	}
	for _, f := range res.Files {
		p := filepath.Join(dir, f.Path)
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				return err
			}
			if err := os.WriteFile(p, f.Data, 0o600); err != nil {
				return err
			}
		}
		fmt.Fprintf(w, "  %s (%d %s)\n", f.Path, f.Objects, f.Kind)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Objects exported (already declared in the repository):")
	for _, kind := range export.Kinds {
		n, d := res.Exported[kind], res.AlreadyDeclared[kind]
		if n == 0 && d == 0 {
			continue
		}
		fmt.Fprintf(w, "  %-16s %5d  (%d)\n", kind, n, d)
	}

	if len(res.Warnings) > 0 {
		warnings := append([]string(nil), res.Warnings...)
		sort.Strings(warnings)
		fmt.Fprintf(w, "\n%d thing(s) NetBox holds that the YAML does not carry — read before merging:\n", len(warnings))
		for _, warning := range warnings {
			fmt.Fprintf(w, "  - %s\n", warning)
		}
	}

	if len(res.Files) > 0 {
		fmt.Fprintln(w, "\nNext: review the files, run `make check` and `./netbox-gitops --dry-run`,")
		fmt.Fprintln(w, "and expect the plan to add the gitops tag and little else. Then open the merge request.")
	}
	return nil
}
