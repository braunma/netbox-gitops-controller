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

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/internal/dotenv"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/reconciler"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

var (
	dryRun           bool
	configFile       string
	dataDir          string
	outputFormat     string
	detailedExitcode bool
	onlyPhases       []string
	siteFilter       string
	deviceFilter     string
	vmFilter         string
	prune            bool

	deviceTypeLibrary   string
	moduleTypeLibrary   string
	ignoredFiles        []string
	includeIgnoredFiles bool
)

// validPhases are the values accepted by --only, in execution order.
var validPhases = loader.Phases

// Build metadata, injected at link time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always) \
//	                   -X main.commit=$(git rev-parse --short HEAD) \
//	                   -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// The defaults are what a plain `go build` produces, so an unstamped binary
// says so rather than claiming a version it does not have.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// exitCode is set by runSync (e.g. 2 for --detailed-exitcode with pending
// changes) and applied after cobra finishes.
var exitCode int

func main() {
	rootCmd := &cobra.Command{
		Use:   "netbox-gitops",
		Short: "NetBox GitOps Controller",
		Long:  `Declarative infrastructure management for NetBox using YAML definitions`,
		RunE:  runSync,
		// Usage is for misuse of the CLI. A failure from a sync is not that,
		// and printing the whole flag list after it buries the actual error.
		SilenceUsage: true,
		Version:      fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate),
	}

	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate changes without applying them")
	rootCmd.PersistentFlags().StringVar(&configFile, "config", defaultConfigFile, "KEY=value file read into the environment; values already exported win over it")
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", ".", "Base directory for definitions and inventory (e.g., 'example' for test data)")
	rootCmd.Flags().StringVar(&outputFormat, "output", "text", "Output format: 'text' or 'json' (json prints the plan to stdout and moves logs to stderr)")
	rootCmd.Flags().BoolVar(&detailedExitcode, "detailed-exitcode", false, "Exit with code 2 when changes are pending (dry-run) or were applied; 0 means in sync")
	rootCmd.Flags().StringSliceVar(&onlyPhases, "only", nil, fmt.Sprintf("Restrict the sync to specific phases (comma-separated or repeated): %s. \"device-types\" covers module types too; \"devices\" covers the modules installed in them", strings.Join(validPhases, ", ")))
	rootCmd.Flags().StringVar(&siteFilter, "site", "", "Restrict reconciliation to devices and VMs of a single site slug")
	rootCmd.Flags().StringVar(&deviceFilter, "device", "", "Restrict device reconciliation to a single device name")
	rootCmd.Flags().StringVar(&vmFilter, "vm", "", "Restrict virtual machine reconciliation to a single VM name")
	rootCmd.Flags().BoolVar(&prune, "prune", false, "Delete gitops-managed objects that are no longer declared in YAML (use with --dry-run to preview)")
	rootCmd.PersistentFlags().StringVar(&deviceTypeLibrary, "devicetype-library", "", "Path to a community-format device type library (default: $DEVICETYPE_LIBRARY, else <data-dir>/definitions/device_type_library)")
	rootCmd.PersistentFlags().StringVar(&moduleTypeLibrary, "moduletype-library", "", "Path to a community-format module type library (default: $MODULETYPE_LIBRARY, else <data-dir>/definitions/module_type_library)")
	rootCmd.PersistentFlags().StringSliceVar(&ignoredFiles, "ignore-file", nil, fmt.Sprintf("Filename globs to skip while loading (default: %s)", strings.Join(loader.DefaultIgnorePatterns, ", ")))
	rootCmd.PersistentFlags().BoolVar(&includeIgnoredFiles, "include-ignored-files", false, "Load files that an ignore pattern would otherwise skip")

	rootCmd.AddCommand(newValidateCommand())
	rootCmd.AddCommand(newCollectCommand())
	rootCmd.AddCommand(newIngestCommand())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(exitCode)
}

func runSync(cmd *cobra.Command, args []string) error {
	if outputFormat != "text" && outputFormat != "json" {
		return fmt.Errorf("invalid --output format %q (supported: text, json)", outputFormat)
	}
	if prune && (siteFilter != "" || deviceFilter != "" || vmFilter != "") {
		// Pruning scoped to a single site/device/VM would delete the
		// out-of-scope objects the filter excluded, so refuse the combination.
		return fmt.Errorf("--prune cannot be combined with --site, --device or --vm")
	}
	if outputFormat == "json" {
		// Reserve stdout for the JSON plan; every logger created from here
		// on (including the client's internal one) writes to stderr.
		utils.SetDefaultOutput(os.Stderr)
	}

	phases, err := parseOnly(onlyPhases)
	if err != nil {
		return err
	}

	logger := utils.NewLogger(dryRun)

	// Read the config file before anything looks at the environment, so every
	// setting the controller takes from it (NETBOX_URL, NETBOX_TOKEN,
	// IGNORE_SSL_ERRORS, the library paths, IGNORED_FILES) can come from there.
	if err := loadConfigFile(configFile, cmd.Flags().Changed("config"), logger); err != nil {
		logger.Error("Failed to read the configuration file", err)
		return err
	}

	// Auto-detect and validate data directory; the phase helpers below read
	// the package-level dataDir, so update it in place.
	resolvedDir, err := resolveDataDir(dataDir, cmd.Flags().Changed("data-dir"), logger)
	if err != nil {
		logger.Error("Failed to resolve data directory", err)
		return err
	}
	dataDir = resolvedDir

	// Read and check everything the selected phases will apply before
	// anything talks to NetBox. The phases used to load their files one kind
	// at a time, between writes, so a typo in racks.yaml surfaced only after
	// the sites, roles and tags had already been applied, and only that one
	// file's problems were shown. The phases work from this one load.
	ds, dataLoader, err := preflight(logger, phases)
	if err != nil {
		return err
	}

	// Load environment variables
	netboxURL := os.Getenv("NETBOX_URL")
	netboxToken := os.Getenv("NETBOX_TOKEN")

	if netboxURL == "" || netboxToken == "" {
		logger.Error(fmt.Sprintf(
			"NETBOX_URL and NETBOX_TOKEN must be set — export them, or put them in %s (copy .env.example)",
			configFile), nil)
		return fmt.Errorf("missing required configuration: NETBOX_URL and NETBOX_TOKEN")
	}

	// Initialize NetBox client
	logger.Info("Initializing NetBox client...")
	c, err := client.NewClient(netboxURL, netboxToken, dryRun)
	if err != nil {
		logger.Error("Failed to initialize NetBox client", err)
		return err
	}

	// Fail fast and legibly on a NetBox too old for the API fields used below.
	if err := c.CheckVersion(); err != nil {
		logger.Error("Unsupported NetBox version", err)
		return err
	}

	// =========================================================================
	// LOAD GLOBAL CACHES (MUST BE BEFORE PHASE 1)
	// =========================================================================
	// Must happen before device reconciliation - and even earlier here,
	// because the foundation reconciler needs it too
	logger.Info("Loading global caches...")
	if err := c.Cache().LoadGlobal(); err != nil {
		logger.Error("Failed to load global caches", err)
		return err
	}

	// =========================================================================
	// PHASE 1: FOUNDATION
	// =========================================================================
	if phases["foundation"] {
		logger.Info("═══════════════════════════════════════════════════════")
		logger.Info("Phase 1: Foundation")
		logger.Info("═══════════════════════════════════════════════════════")

		if err := runFoundation(c, ds, logger); err != nil {
			return err
		}
	} else {
		logger.Info("Skipping phase 1 (foundation): not selected via --only")
	}

	// =========================================================================
	// PHASE 2: NETWORK & TYPES
	// =========================================================================
	if phases["network"] || phases["device-types"] {
		logger.Info("═══════════════════════════════════════════════════════")
		logger.Info("Phase 2: Network & Types")
		logger.Info("═══════════════════════════════════════════════════════")

		if phases["network"] {
			if err := runNetwork(c, ds, logger); err != nil {
				return err
			}
		}
		if phases["device-types"] {
			if err := runDeviceTypes(c, ds, logger); err != nil {
				return err
			}
		}
	} else {
		logger.Info("Skipping phase 2 (network & types): not selected via --only")
	}

	// =========================================================================
	// PHASE 3: DEVICES
	// =========================================================================
	if phases["devices"] {
		logger.Info("═══════════════════════════════════════════════════════")
		logger.Info("Phase 3: Devices")
		logger.Info("═══════════════════════════════════════════════════════")

		if err := runDevices(c, ds, logger); err != nil {
			return err
		}
	} else {
		logger.Info("Skipping phase 3 (devices): not selected via --only")
	}

	// =========================================================================
	// PHASE 4: VIRTUALIZATION
	// =========================================================================
	if phases["virtualization"] {
		logger.Info("═══════════════════════════════════════════════════════")
		logger.Info("Phase 4: Virtualization")
		logger.Info("═══════════════════════════════════════════════════════")

		if err := runVirtualization(c, ds, logger); err != nil {
			return err
		}
	} else {
		logger.Info("Skipping phase 4 (virtualization): not selected via --only")
	}

	// =========================================================================
	// PRUNE: delete gitops-managed orphans no longer declared in YAML
	// =========================================================================
	if prune {
		logger.Info("═══════════════════════════════════════════════════════")
		logger.Info("Prune: removing orphaned gitops-managed objects")
		logger.Info("═══════════════════════════════════════════════════════")

		// A parked file's objects were never loaded, so they are not in the
		// set of objects seen this run. Any of them this controller previously
		// created still carries the managed tag and would be pruned as an
		// orphan; objects owned by another system are untagged and are safe.
		// The operator is the only one who can tell those apart.
		if ignored := dataLoader.IgnoredFiles(); len(ignored) > 0 {
			logger.Warning("Pruning with %d ignored file(s) skipped: any managed object declared only in them will be deleted as an orphan", len(ignored))
			for _, f := range ignored {
				logger.Warning("  ignored: %s", f)
			}
			logger.Warning("  re-run with --include-ignored-files if those objects should be kept")
		}

		if err := c.Prune(pruneTargets(phases)); err != nil {
			logger.Error("Failed to prune orphaned objects", err)
			return err
		}
	}

	// =========================================================================
	// SUMMARY
	// =========================================================================
	summary := c.Recorder().Summary()

	logger.Info("═══════════════════════════════════════════════════════")
	if dryRun {
		logger.Warning("DRY RUN COMPLETE: No changes applied")
		logger.Info("Plan: %d to create, %d to update, %d to delete, %d unchanged",
			summary.Create, summary.Update, summary.Delete, summary.Unchanged)
	} else {
		logger.Success("SYNC COMPLETE: Changes applied successfully")
		logger.Info("Summary: %d created, %d updated, %d deleted, %d unchanged",
			summary.Create, summary.Update, summary.Delete, summary.Unchanged)
	}
	logger.Info("═══════════════════════════════════════════════════════")

	if outputFormat == "json" {
		if err := writePlanJSON(os.Stdout, dryRun, summary, c.Recorder().Changes()); err != nil {
			logger.Error("Failed to write JSON plan", err)
			return err
		}
	}

	if detailedExitcode && summary.HasChanges() {
		exitCode = 2
	}

	return nil
}

// runFoundation reconciles tags, roles, sites and racks.
func runFoundation(c *client.NetBoxClient, ds loader.Dataset, logger *utils.Logger) error {
	foundationReconciler := reconciler.NewFoundationReconciler(c)

	if err := foundationReconciler.ReconcileTags(ds.Tags); err != nil {
		logger.Error("Failed to reconcile tags", err)
		return err
	}

	if err := foundationReconciler.ReconcileRoles(ds.Roles); err != nil {
		logger.Error("Failed to reconcile roles", err)
		return err
	}

	// Custom fields must exist before any object sets a value into one (e.g. the
	// `vmid` field consumed by virtual machines in the virtualization phase).
	if err := foundationReconciler.ReconcileCustomFields(ds.CustomFields); err != nil {
		logger.Error("Failed to reconcile custom fields", err)
		return err
	}

	if err := foundationReconciler.ReconcilePlatforms(ds.Platforms); err != nil {
		logger.Error("Failed to reconcile platforms", err)
		return err
	}

	// Tenant groups before tenants: a tenant may reference a group created here.
	if err := foundationReconciler.ReconcileTenantGroups(ds.TenantGroups); err != nil {
		logger.Error("Failed to reconcile tenant groups", err)
		return err
	}

	if err := foundationReconciler.ReconcileTenants(ds.Tenants); err != nil {
		logger.Error("Failed to reconcile tenants", err)
		return err
	}

	if err := foundationReconciler.ReconcileSites(ds.Sites); err != nil {
		logger.Error("Failed to reconcile sites", err)
		return err
	}

	if err := foundationReconciler.ReconcileRacks(ds.Racks); err != nil {
		logger.Error("Failed to reconcile racks", err)
		return err
	}

	return nil
}

// runNetwork reconciles VRFs, VLAN groups, VLANs and prefixes.
func runNetwork(c *client.NetBoxClient, ds loader.Dataset, logger *utils.Logger) error {
	networkReconciler := reconciler.NewNetworkReconciler(c)

	if err := networkReconciler.ReconcileVRFs(ds.VRFs); err != nil {
		logger.Error("Failed to reconcile VRFs", err)
		return err
	}

	if err := networkReconciler.ReconcileVLANGroups(ds.VLANGroups); err != nil {
		logger.Error("Failed to reconcile VLAN groups", err)
		return err
	}

	if err := networkReconciler.ReconcileVLANs(ds.VLANs); err != nil {
		logger.Error("Failed to reconcile VLANs", err)
		return err
	}

	if err := networkReconciler.ReconcilePrefixes(ds.Prefixes); err != nil {
		logger.Error("Failed to reconcile prefixes", err)
		return err
	}

	return nil
}

// runDeviceTypes reconciles module types and device types.
func runDeviceTypes(c *client.NetBoxClient, ds loader.Dataset, logger *utils.Logger) error {
	deviceTypeReconciler := reconciler.NewDeviceTypeReconciler(c)

	// Native definitions merged with the libraries, exactly as the loader
	// hands them to every checker.
	if err := deviceTypeReconciler.ReconcileModuleTypes(ds.ModuleTypes); err != nil {
		logger.Error("Failed to reconcile module types", err)
		return err
	}

	if err := deviceTypeReconciler.ReconcileDeviceTypes(ds.DeviceTypes); err != nil {
		logger.Error("Failed to reconcile device types", err)
		return err
	}

	return nil
}

// defaultConfigFile is where --config looks when it is not given a path. A
// missing file there is ordinary — the environment is the other way to
// configure the controller, and CI uses it.
const defaultConfigFile = ".env"

// loadConfigFile reads a KEY=value file into the environment. A missing file
// is only an error when the path was asked for explicitly: `--config
// prod.env` naming a file that is not there is a mistake worth stopping for,
// while the default `.env` is absent on every CI runner.
func loadConfigFile(path string, explicit bool, logger *utils.Logger) error {
	applied, err := dotenv.Load(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return nil
		}
		return err
	}

	if len(applied) == 0 {
		// Every variable it declares was already exported, so the file changed
		// nothing — worth saying, since it looks like it did.
		logger.Debug("Read %s; every variable it declares was already set in the environment", path)
		return nil
	}
	logger.Info("Read %s (%s)", path, strings.Join(applied, ", "))
	return nil
}

// resolvePath returns the first configured value for a path setting: the
// flag's value, else the named environment variable, else the conventional
// default path.
func resolvePath(flagValue, envName, defaultPath string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv(envName); env != "" {
		return env
	}
	return defaultPath
}

// resolveModuleTypeLibrary returns the root of the community-format module
// type library: the --moduletype-library flag, else MODULETYPE_LIBRARY, else
// the conventional path inside the data directory. In a library checkout this
// is the module-types/ directory, the sibling of device-types/.
func resolveModuleTypeLibrary() string {
	return resolvePath(moduleTypeLibrary, "MODULETYPE_LIBRARY",
		filepath.Join(dataDir, "definitions", "module_type_library"))
}

// preflight loads every definition and inventory folder the selected phases
// apply — with the ignore patterns and libraries configured for the run — and
// reports every problem at once, one per line. It writes nothing and needs
// no NetBox. The dataset it returns is what the phases reconcile; a phase
// not selected has nothing loaded for it. The loader comes back too, for the
// files it skipped.
func preflight(logger *utils.Logger, phases map[string]bool) (loader.Dataset, *loader.DataLoader, error) {
	patterns, err := resolveIgnorePatterns()
	if err != nil {
		logger.Error("Invalid ignore pattern", err)
		return loader.Dataset{}, nil, err
	}
	dl := loader.NewDataLoader(dataDir, logger)
	dl.SetIgnorePatterns(patterns)
	ds, err := dl.LoadDataset(loader.DatasetOptions{
		DeviceTypeLibrary: resolveDeviceTypeLibrary(),
		ModuleTypeLibrary: resolveModuleTypeLibrary(),
		Phases:            phases,
	})
	if err := reportLoadProblems(logger, err, "nothing was sent to NetBox"); err != nil {
		return loader.Dataset{}, nil, err
	}
	return ds, dl, nil
}

// reportLoadProblems prints every problem a load returned on its own line,
// then one line saying how many and what was therefore not done. It returns
// nil for a nil error, so callers can pass a load's result straight through.
func reportLoadProblems(logger *utils.Logger, err error, consequence string) error {
	if err == nil {
		return nil
	}
	problems := loader.Problems(err)
	for _, problem := range problems {
		logger.Error("%s", nil, problem)
	}
	logger.Error("%d problem(s) in %s; %s. `yamlcheck` reports the same list.", nil, len(problems), dataDir, consequence)
	return fmt.Errorf("the YAML in %s has %d problem(s)", dataDir, len(problems))
}

// resolveIgnorePatterns returns the filename globs to skip while loading:
// none when --include-ignored-files is set, else --ignore-file, else
// IGNORED_FILES (comma-separated), else the defaults.
func resolveIgnorePatterns() ([]string, error) {
	if includeIgnoredFiles {
		return nil, nil
	}

	patterns := ignoredFiles
	if len(patterns) == 0 {
		if env := os.Getenv("IGNORED_FILES"); env != "" {
			for _, p := range strings.Split(env, ",") {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					patterns = append(patterns, trimmed)
				}
			}
		}
	}
	if len(patterns) == 0 {
		patterns = loader.DefaultIgnorePatterns
	}

	if err := loader.ValidateIgnorePatterns(patterns); err != nil {
		return nil, err
	}
	return patterns, nil
}

// resolveDeviceTypeLibrary returns the root of the community-format device
// type library: the --devicetype-library flag, else DEVICETYPE_LIBRARY, else
// the conventional path inside the data directory. The default is optional —
// LoadDeviceTypeLibrary skips a root that does not exist.
func resolveDeviceTypeLibrary() string {
	return resolvePath(deviceTypeLibrary, "DEVICETYPE_LIBRARY",
		filepath.Join(dataDir, "definitions", "device_type_library"))
}

// runDevices loads the device inventory, applies the --site/--device
// filters, loads the required site caches and reconciles the devices.
func runDevices(c *client.NetBoxClient, ds loader.Dataset, logger *utils.Logger) error {
	allDevices := ds.Devices
	logger.Info("Loaded %d devices from inventory", len(allDevices))

	if siteFilter != "" || deviceFilter != "" {
		filtered := filterDevices(allDevices, siteFilter, deviceFilter)
		logger.Info("Device filter (site=%q, device=%q) matched %d of %d devices",
			siteFilter, deviceFilter, len(filtered), len(allDevices))
		if len(filtered) == 0 {
			logger.Warning("No devices match the given --site/--device filter")
			return nil
		}
		allDevices = filtered
	}

	// Load site-specific caches
	uniqueSites := make(map[string]bool)
	for _, device := range allDevices {
		uniqueSites[device.SiteSlug] = true
	}

	logger.Info("Loading site caches for: %v", getKeys(uniqueSites))
	for siteSlug := range uniqueSites {
		if err := c.Cache().LoadSite(siteSlug); err != nil {
			logger.Error("Failed to load site cache for "+siteSlug, err)
			return err
		}
	}

	deviceReconciler := reconciler.NewDeviceReconciler(c)
	deviceReconciler.SetDeviceTypes(ds.DeviceTypes)
	if err := deviceReconciler.ReconcileDevices(allDevices); err != nil {
		logger.Error("Failed to reconcile devices", err)
		return err
	}

	return nil
}

// runVirtualization reconciles cluster types, cluster groups and clusters.
// (Virtual machines are reconciled in a later step.)
func runVirtualization(c *client.NetBoxClient, ds loader.Dataset, logger *utils.Logger) error {
	virtReconciler := reconciler.NewVirtualizationReconciler(c)

	if err := virtReconciler.ReconcileClusterTypes(ds.ClusterTypes); err != nil {
		logger.Error("Failed to reconcile cluster types", err)
		return err
	}

	if err := virtReconciler.ReconcileClusterGroups(ds.ClusterGroups); err != nil {
		logger.Error("Failed to reconcile cluster groups", err)
		return err
	}

	if err := virtReconciler.ReconcileClusters(ds.Clusters); err != nil {
		logger.Error("Failed to reconcile clusters", err)
		return err
	}

	vms := ds.VMs

	if siteFilter != "" || vmFilter != "" {
		filtered := filterVMs(vms, siteFilter, vmFilter)
		logger.Info("VM filter (site=%q, vm=%q) matched %d of %d VMs",
			siteFilter, vmFilter, len(filtered), len(vms))
		if len(filtered) == 0 {
			logger.Warning("No VMs match the given --site/--vm filter")
			return nil
		}
		vms = filtered
	}

	// Load site caches so VM interfaces can resolve VLANs. A clustered VM
	// inherits its cluster's site, so warm caches for sites referenced by both
	// clusters and VMs.
	uniqueSites := make(map[string]bool)
	for _, cl := range ds.Clusters {
		if cl.SiteSlug != "" {
			uniqueSites[cl.SiteSlug] = true
		}
	}
	for _, vm := range vms {
		if vm.SiteSlug != "" {
			uniqueSites[vm.SiteSlug] = true
		}
	}
	for siteSlug := range uniqueSites {
		if err := c.Cache().LoadSite(siteSlug); err != nil {
			logger.Error("Failed to load site cache for "+siteSlug, err)
			return err
		}
	}

	if err := virtReconciler.ReconcileVMs(vms); err != nil {
		logger.Error("Failed to reconcile virtual machines", err)
		return err
	}

	return nil
}

// filterVMs returns the VMs matching the given site slug and/or VM name.
// Empty filter values match everything.
func filterVMs(vms []*models.VMConfig, siteSlug, vmName string) []*models.VMConfig {
	var filtered []*models.VMConfig
	for _, vm := range vms {
		if siteSlug != "" && vm.SiteSlug != siteSlug {
			continue
		}
		if vmName != "" && vm.Name != vmName {
			continue
		}
		filtered = append(filtered, vm)
	}
	return filtered
}

// filterDevices returns the devices matching the given site slug and/or
// device name. Empty filter values match everything.
func filterDevices(devices []*models.DeviceConfig, siteSlug, deviceName string) []*models.DeviceConfig {
	var filtered []*models.DeviceConfig
	for _, d := range devices {
		if siteSlug != "" && d.SiteSlug != siteSlug {
			continue
		}
		if deviceName != "" && d.Name != deviceName {
			continue
		}
		filtered = append(filtered, d)
	}
	return filtered
}

// parseOnly turns the --only values into a phase set. An empty flag selects
// all phases.
func parseOnly(values []string) (map[string]bool, error) {
	phases := make(map[string]bool)
	if len(values) == 0 {
		for _, p := range validPhases {
			phases[p] = true
		}
		return phases, nil
	}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		valid := false
		for _, p := range validPhases {
			if v == p {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("invalid --only value %q (supported: %s)", v, strings.Join(validPhases, ", "))
		}
		phases[v] = true
	}
	return phases, nil
}

// pruneTargets returns the endpoints to prune for the selected phases, in
// reverse dependency order (children before parents) so that deletions do not
// violate NetBox foreign-key constraints. Only endpoints belonging to a phase
// that actually ran are included, so --only keeps prune in scope.
//
// Cables come first in the devices phase: a managed cable the run did not
// claim is an orphan like any other object. They come once more at the very
// end, for what the run's own deletions leave behind: NetBox removes a
// deleted port's terminations but keeps the cable object, with no ends, and
// such a cable can never be reached again, so it is swept whether or not it
// carries the tag.
func pruneTargets(phases map[string]bool) []client.PruneTarget {
	var targets []client.PruneTarget
	unterminated := map[string]interface{}{"unterminated": "true"}
	const shell = "has no terminations left (its port or device was deleted)"

	// IP addresses reference both device interfaces and VM interfaces, so they
	// are pruned once up front (before any interface is removed) whenever
	// either owning phase ran.
	if phases["devices"] || phases["virtualization"] {
		targets = append(targets, client.PruneTarget{App: "ipam", Endpoint: "ip-addresses"})
	}

	if phases["devices"] {
		// Cables before the ports they join; then device children (reverse
		// dependency: front ports reference rear ports, all components
		// reference the device), then the devices themselves. Deleting an
		// orphaned device cascades any remaining managed children in NetBox.
		targets = append(targets,
			client.PruneTarget{App: "dcim", Endpoint: "cables", Also: unterminated, AlsoReason: shell},
			client.PruneTarget{App: "dcim", Endpoint: "front-ports"},
			client.PruneTarget{App: "dcim", Endpoint: "interfaces"},
			client.PruneTarget{App: "dcim", Endpoint: "rear-ports"},
			client.PruneTarget{App: "dcim", Endpoint: "modules"},
			client.PruneTarget{App: "dcim", Endpoint: "devices"},
		)
	}
	if phases["virtualization"] {
		// VM interfaces before VMs; clusters after the VMs that reference them,
		// then cluster groups and types. Ordered before network/foundation so
		// the VLANs, sites and tenants these point at are still present.
		targets = append(targets,
			client.PruneTarget{App: "virtualization", Endpoint: "interfaces"},
			client.PruneTarget{App: "virtualization", Endpoint: "virtual-machines"},
			client.PruneTarget{App: "virtualization", Endpoint: "clusters"},
			client.PruneTarget{App: "virtualization", Endpoint: "cluster-groups"},
			client.PruneTarget{App: "virtualization", Endpoint: "cluster-types"},
		)
	}
	if phases["device-types"] {
		targets = append(targets,
			client.PruneTarget{App: "dcim", Endpoint: "device-types"},
			client.PruneTarget{App: "dcim", Endpoint: "module-types"},
		)
	}
	if phases["network"] {
		targets = append(targets,
			client.PruneTarget{App: "ipam", Endpoint: "prefixes"},
			client.PruneTarget{App: "ipam", Endpoint: "vlans"},
			client.PruneTarget{App: "ipam", Endpoint: "vlan-groups"},
			client.PruneTarget{App: "ipam", Endpoint: "vrfs"},
		)
	}
	if phases["foundation"] {
		// Sites, roles, platforms and tenants are referenced by devices, VMs
		// and clusters, so they come after those phases. Tenants before tenant
		// groups (a tenant references its group).
		targets = append(targets,
			client.PruneTarget{App: "dcim", Endpoint: "racks"},
			client.PruneTarget{App: "dcim", Endpoint: "device-roles"},
			client.PruneTarget{App: "dcim", Endpoint: "platforms"},
			client.PruneTarget{App: "dcim", Endpoint: "sites"},
			client.PruneTarget{App: "tenancy", Endpoint: "tenants"},
			client.PruneTarget{App: "tenancy", Endpoint: "tenant-groups"},
			// The managed tag carries its own slug; never prune it.
			client.PruneTarget{App: "extras", Endpoint: "tags", KeepSlugs: []string{constants.ManagedTagSlug}},
		)
	}

	if phases["devices"] {
		// The deletions above leave cable shells; sweep them in the same run.
		targets = append(targets,
			client.PruneTarget{App: "dcim", Endpoint: "cables", Also: unterminated, AlsoReason: shell, OnlyAlso: true})
	}

	return targets
}

// writePlanJSON emits the collected changes as a machine-readable plan,
// e.g. for posting a terraform-plan-style comment on a merge request.
func writePlanJSON(w io.Writer, dryRun bool, summary client.ChangeSummary, changes []client.ChangeRecord) error {
	plan := struct {
		DryRun  bool                  `json:"dry_run"`
		Summary client.ChangeSummary  `json:"summary"`
		Changes []client.ChangeRecord `json:"changes"`
	}{dryRun, summary, changes}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

// getKeys returns the keys of a map as a slice
func getKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// resolveDataDir returns the directory to load definitions and inventory from.
//
// When the caller did not name one, a checkout with no private data falls back
// to example/ so that a first run has something to show. An explicit
// --data-dir never falls back: a typo there would otherwise apply the example
// dataset to whatever NetBox the token points at, and with --prune delete
// every managed object the example does not declare.
func resolveDataDir(dir string, explicit bool, logger *utils.Logger) (string, error) {
	definitionsPath := filepath.Join(dir, "definitions")
	if _, err := os.Stat(definitionsPath); err == nil {
		logger.Info("Using data directory: %s", dir)
		return dir, nil
	}

	if explicit {
		return "", fmt.Errorf("--data-dir %s holds no definitions/ directory", dir)
	}

	const examplePath = "example"
	if _, err := os.Stat(filepath.Join(examplePath, "definitions")); err == nil {
		logger.Warning("definitions/ not found in '%s', falling back to '%s'", dir, examplePath)
		return examplePath, nil
	}

	return "", fmt.Errorf("no valid data directory found: checked '%s' and '%s'", dir, examplePath)
}
