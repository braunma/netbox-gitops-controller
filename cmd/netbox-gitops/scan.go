// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/collectors"
	"github.com/braunma/netbox-gitops-controller/pkg/facts"
	"github.com/braunma/netbox-gitops-controller/pkg/ingest"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

var (
	scanDryRun  bool
	scanSources []string
	scanStrict  bool
)

// newScanCommand builds the `scan` subcommand.
func newScanCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the BMCs of declared devices and write their hardware facts to NetBox",
		Long: `Scan the BMCs of declared devices and write their hardware facts to NetBox.

Each source of type "idrac" in collectors.yaml is resolved against the
inventory: the hosts it scans are the addresses the selected devices declare
on the source's interface. There is no host list to maintain and no range to
sweep, and a result always belongs to the device whose address answered.

What a scan reports is written straight to NetBox, as custom fields on the
device it was read from. This is the one route that does not pass through the
YAML, and it is narrow on purpose:

  - only custom fields under the scan's prefix (hw_) are written, and only the
    ones this repository declares in definitions/custom_fields for devices;
  - a custom field the YAML sets on a device is left alone, because the YAML
    owns it;
  - serial and asset_tag are never written. Where the BMC reports a different
    value than the YAML declares, the run says so, and a human decides.

A device NetBox does not hold yet is skipped: the sync creates devices, a scan
does not. A BMC that does not answer is reported by name and does not stop the
others.

Exit codes: 0 on success, 1 on error. With --strict, also 1 when a selected
host could not be scanned or is missing in NetBox.`,
		RunE:         runScan,
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&scanDryRun, "dry-run", false,
		"Scan and compare, but write nothing to NetBox")
	cmd.Flags().StringVar(&collectorsConfig, "collectors-config", "",
		fmt.Sprintf("Path to the source configuration (default: $COLLECTORS_CONFIG, else <data-dir>/%s)", collectors.DefaultConfigFile))
	cmd.Flags().StringSliceVar(&scanSources, "source", nil,
		"Restrict the run to named scan sources (comma-separated or repeated); default is every scan source")
	cmd.Flags().StringVar(&customFieldPrefix, "custom-field-prefix", ingest.DefaultCustomFieldPrefix,
		"Prefix of the custom fields a scan owns; nothing outside it is ever written")
	cmd.Flags().BoolVar(&scanStrict, "strict", false,
		"Exit with code 1 when a selected host could not be scanned or is missing in NetBox")
	return cmd
}

// scanTotals counts what a run did, across sources.
type scanTotals struct {
	updated, unchanged, missing, unreachable int
}

func runScan(cmd *cobra.Command, args []string) error {
	logger := utils.NewLogger(scanDryRun)

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

	cfg, err := collectors.LoadConfig(resolveCollectorsConfig())
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no source configuration at %s — copy collectors.yaml.example and name your sources there",
				resolveCollectorsConfig())
		}
		logger.Error("Failed to read the source configuration", err)
		return err
	}

	// Every scan source takes part in the assignment, selected or not: the
	// overlap check is across all of them.
	var allScan []collectors.Source
	for _, src := range cfg.Sources {
		if src.IsScan() {
			allScan = append(allScan, src)
		}
	}
	if len(allScan) == 0 {
		logger.Warning("The source configuration declares no scan source (type: %s); nothing to do", collectors.TypeIDRAC)
		return nil
	}
	selected, err := selectScanSources(cfg, allScan)
	if err != nil {
		logger.Error("Unusable --source", err)
		return err
	}

	// Everything that can fail without a network is checked before the first
	// request: the YAML, the assignment, the credentials.
	dataset, err := loadScanDataset(logger)
	if err != nil {
		return err
	}
	assignment, err := collectors.AssignTargets(allScan, dataset.Devices, dataset.DeviceTypes)
	if err != nil {
		logger.Error("The scan sources do not divide the inventory cleanly", err)
		return err
	}
	if len(assignment.Unassigned) > 0 {
		logger.Warning("%d device(s) declare a BMC address that no scan source selects, and will not be scanned: %s",
			len(assignment.Unassigned), strings.Join(assignment.Unassigned, ", "))
	}

	scanners := make([]*collectors.IDRACScanner, 0, len(selected))
	var buildErrs []error
	for _, src := range selected {
		scanner, err := collectors.NewIDRACScanner(src, logger)
		if err != nil {
			buildErrs = append(buildErrs, err)
			continue
		}
		scanners = append(scanners, scanner)
	}
	if err := errors.Join(buildErrs...); err != nil {
		logger.Error("A scan source is not usable", err)
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
	c, err := client.NewClient(netboxURL, netboxToken, scanDryRun)
	if err != nil {
		logger.Error("Failed to initialize NetBox client", err)
		return err
	}

	writer := facts.NewWriter(c, dataset.CustomFields, customFieldPrefix)
	if len(writer.DeclaredFields()) == 0 {
		err := fmt.Errorf("no custom field with the prefix %q is declared for devices in %s/definitions/custom_fields — a scan writes only declared fields; copy the hw_ definitions from example/definitions/custom_fields/custom_fields.yaml",
			customFieldPrefix, dataDir)
		logger.Error("Nothing a scan could write", err)
		return err
	}

	devices := make(map[string]*models.DeviceConfig, len(dataset.Devices))
	for _, dev := range dataset.Devices {
		devices[dev.Name] = dev
	}

	var totals scanTotals
	var runErrs []error
	for _, scanner := range scanners {
		name := scanner.Name()
		targets := assignment.Targets[name]
		for _, dev := range assignment.NoAddress[name] {
			logger.Warning("%s: selected by source %s and has the interface it scans, but declares no address on it; not scanned", dev, name)
		}
		if len(targets) == 0 {
			logger.Warning("Source %s selects no device with an address to scan", name)
			continue
		}

		logger.Info("Scanning %d host(s) for source %s...", len(targets), name)
		for _, result := range scanner.Scan(cmd.Context(), targets) {
			if err := writeScanResult(result, devices[result.Target.Device], writer, &totals, logger); err != nil {
				runErrs = append(runErrs, err)
			}
		}
	}

	reportScan(totals, writer, logger)

	if len(runErrs) > 0 {
		return fmt.Errorf("%d device(s) could not be written; see the errors above", len(runErrs))
	}
	if scanStrict && (totals.unreachable > 0 || totals.missing > 0) {
		return fmt.Errorf("--strict: %d host(s) could not be scanned and %d device(s) are missing in NetBox", totals.unreachable, totals.missing)
	}
	return nil
}

// selectScanSources returns the scan sources a run reads: the ones --source
// names, or all of them. Naming a `collect` source here is an error rather
// than a run that quietly scans nothing.
func selectScanSources(cfg *collectors.Config, allScan []collectors.Source) ([]collectors.Source, error) {
	if len(scanSources) == 0 {
		return allScan, nil
	}
	selected, err := cfg.Select(scanSources)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, src := range selected {
		if !src.IsScan() {
			errs = append(errs, fmt.Errorf("source %q is of type %q, which `collect` reads, not `scan`", src.Name, src.Type))
		}
	}
	return selected, errors.Join(errs...)
}

// loadScanDataset reads the YAML the way the sync does, so the addresses a
// scan reaches are the ones the sync puts in NetBox.
func loadScanDataset(logger *utils.Logger) (loader.Dataset, error) {
	patterns, err := resolveIgnorePatterns()
	if err != nil {
		return loader.Dataset{}, err
	}
	dl := loader.NewDataLoader(dataDir, logger)
	dl.SetIgnorePatterns(patterns)

	dataset, err := dl.LoadDataset(loader.DatasetOptions{
		DeviceTypeLibrary: resolveDeviceTypeLibrary(),
		ModuleTypeLibrary: resolveModuleTypeLibrary(),
	})
	if err != nil {
		return loader.Dataset{}, reportLoadProblems(logger, err, "nothing was scanned")
	}
	return dataset, nil
}

// writeScanResult handles one host: it reports a failed scan, compares the
// identity the BMC reported with the declared one, and writes the facts.
func writeScanResult(result collectors.ScanResult, dev *models.DeviceConfig, writer *facts.Writer, totals *scanTotals, logger *utils.Logger) error {
	target := result.Target
	if result.Err != nil {
		totals.unreachable++
		logger.Warning("%s (%s): not scanned: %v", target.Device, target.Address, result.Err)
		return nil
	}
	if len(result.Incomplete) > 0 {
		logger.Warning("%s (%s): the BMC did not answer for %s; those facts keep the values NetBox already holds",
			target.Device, target.Address, strings.Join(result.Incomplete, ", "))
	}
	reportIdentity(dev, result, logger)

	report, err := writer.Write(dev, ingest.HardwareFieldValues(result.Device.HW, customFieldPrefix))
	if err != nil {
		logger.Error("Failed to write hardware facts", err)
		return err
	}
	if len(report.YAMLOwned) > 0 {
		logger.Warning("%s: %s set in the YAML, which owns them; the scanned values were not written. Remove them from the device's custom_fields to let the scan maintain them.",
			target.Device, strings.Join(report.YAMLOwned, ", "))
	}

	switch report.Outcome {
	case facts.Missing:
		totals.missing++
		logger.Warning("%s: not in NetBox yet; run the sync first, then scan again", target.Device)
	case facts.Unchanged:
		totals.unchanged++
		logger.Debug("%s: unchanged", target.Device)
	case facts.Updated:
		totals.updated++
		parts := make([]string, 0, len(report.Changes))
		for _, change := range report.Changes {
			parts = append(parts, fmt.Sprintf("%s %s → %v", change.Field, renderOld(change.Old), change.New))
		}
		if scanDryRun {
			logger.DryRun("UPDATE", "%s: %s", target.Device, strings.Join(parts, "; "))
		} else {
			logger.Success("%s: %s", target.Device, strings.Join(parts, "; "))
		}
	}
	return nil
}

// reportIdentity compares the serial and the service tag the BMC reported
// with what the YAML declares. Neither is ever written by a scan: a mismatch
// is a typo or swapped hardware, and which of the two is for a human to say.
func reportIdentity(dev *models.DeviceConfig, result collectors.ScanResult, logger *utils.Logger) {
	checks := []struct {
		key, declared, scanned string
	}{
		{ingest.KeyDeviceSerial, dev.Serial, result.Device.Serial},
		{ingest.KeyDeviceAssetTag, dev.AssetTag, result.Device.ServiceTag},
	}
	for _, check := range checks {
		switch {
		case check.scanned == "":
		case check.declared == "":
			logger.Info("%s: the BMC reports %s %q; the YAML declares none", dev.Name, check.key, check.scanned)
		case !strings.EqualFold(check.declared, check.scanned):
			logger.Warning("%s: %s MISMATCH — the YAML declares %q, the BMC at %s reports %q. Nothing was changed; correct the YAML or check the machine.",
				dev.Name, check.key, check.declared, result.Target.Address, check.scanned)
		}
	}
}

// renderOld prints the value NetBox held, with an unset field shown as such.
func renderOld(v interface{}) string {
	if v == nil {
		return "(unset)"
	}
	return fmt.Sprintf("%v", v)
}

// reportScan prints the run summary.
func reportScan(totals scanTotals, writer *facts.Writer, logger *utils.Logger) {
	logger.Info("═══════════════════════════════════════════════════════")
	if scanDryRun {
		logger.Warning("DRY RUN COMPLETE: nothing written to NetBox")
	} else {
		logger.Success("SCAN COMPLETE")
	}
	logger.Info("Devices: %d updated, %d unchanged, %d not in NetBox, %d not scanned",
		totals.updated, totals.unchanged, totals.missing, totals.unreachable)
	if undeclared := writer.Undeclared(); len(undeclared) > 0 {
		logger.Warning("Reported but not written, because definitions/custom_fields does not declare them for devices: %s",
			strings.Join(undeclared, ", "))
	}
	logger.Info("═══════════════════════════════════════════════════════")
}
