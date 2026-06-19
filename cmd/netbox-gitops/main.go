package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
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
)

// validPhases are the values accepted by --only, in execution order.
var validPhases = []string{"foundation", "network", "device-types", "devices", "virtualization"}

// exitCode is set by runSync (e.g. 2 for --detailed-exitcode with pending
// changes) and applied after cobra finishes.
var exitCode int

func main() {
	rootCmd := &cobra.Command{
		Use:   "netbox-gitops",
		Short: "NetBox GitOps Controller",
		Long:  `Declarative infrastructure management for NetBox using YAML definitions`,
		RunE:  runSync,
	}

	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate changes without applying them")
	rootCmd.Flags().StringVar(&configFile, "config", ".env", "Configuration file path")
	rootCmd.Flags().StringVar(&dataDir, "data-dir", ".", "Base directory for definitions and inventory (e.g., 'example' for test data)")
	rootCmd.Flags().StringVar(&outputFormat, "output", "text", "Output format: 'text' or 'json' (json prints the plan to stdout and moves logs to stderr)")
	rootCmd.Flags().BoolVar(&detailedExitcode, "detailed-exitcode", false, "Exit with code 2 when changes are pending (dry-run) or were applied; 0 means in sync")
	rootCmd.Flags().StringSliceVar(&onlyPhases, "only", nil, fmt.Sprintf("Restrict the sync to specific phases (comma-separated or repeated): %s", strings.Join(validPhases, ", ")))
	rootCmd.Flags().StringVar(&siteFilter, "site", "", "Restrict reconciliation to devices and VMs of a single site slug")
	rootCmd.Flags().StringVar(&deviceFilter, "device", "", "Restrict device reconciliation to a single device name")
	rootCmd.Flags().StringVar(&vmFilter, "vm", "", "Restrict virtual machine reconciliation to a single VM name")
	rootCmd.Flags().BoolVar(&prune, "prune", false, "Delete gitops-managed objects that are no longer declared in YAML (use with --dry-run to preview)")

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

	// Auto-detect and validate data directory; the phase helpers below read
	// the package-level dataDir, so update it in place.
	resolvedDir, err := resolveDataDir(dataDir, logger)
	if err != nil {
		logger.Error("Failed to resolve data directory", err)
		return err
	}
	dataDir = resolvedDir

	// Load environment variables
	netboxURL := os.Getenv("NETBOX_URL")
	netboxToken := os.Getenv("NETBOX_TOKEN")

	if netboxURL == "" || netboxToken == "" {
		logger.Error("NETBOX_URL and NETBOX_TOKEN environment variables must be set", nil)
		return fmt.Errorf("missing required environment variables")
	}

	// Initialize NetBox client
	logger.Info("Initializing NetBox client...")
	c, err := client.NewClient(netboxURL, netboxToken, dryRun)
	if err != nil {
		logger.Error("Failed to initialize NetBox client", err)
		return err
	}

	// Initialize data loader
	dataLoader := loader.NewDataLoader(dataDir, logger)

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

		if err := runFoundation(c, dataLoader, logger); err != nil {
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
			if err := runNetwork(c, dataLoader, logger); err != nil {
				return err
			}
		}
		if phases["device-types"] {
			if err := runDeviceTypes(c, dataLoader, logger); err != nil {
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

		if err := runDevices(c, dataLoader, logger); err != nil {
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

		if err := runVirtualization(c, dataLoader, logger); err != nil {
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
func runFoundation(c *client.NetBoxClient, dataLoader *loader.DataLoader, logger *utils.Logger) error {
	foundationReconciler := reconciler.NewFoundationReconciler(c)

	tags, err := dataLoader.LoadTags("definitions/extras")
	if err != nil {
		logger.Error("Failed to load tags", err)
		return err
	}
	if err := foundationReconciler.ReconcileTags(tags); err != nil {
		logger.Error("Failed to reconcile tags", err)
		return err
	}

	roles, err := dataLoader.LoadRoles("definitions/roles")
	if err != nil {
		logger.Error("Failed to load roles", err)
		return err
	}
	if err := foundationReconciler.ReconcileRoles(roles); err != nil {
		logger.Error("Failed to reconcile roles", err)
		return err
	}

	// Custom fields must exist before any object sets a value into one (e.g. the
	// `vmid` field consumed by virtual machines in the virtualization phase).
	customFields, err := dataLoader.LoadCustomFields("definitions/custom_fields")
	if err != nil {
		logger.Error("Failed to load custom fields", err)
		return err
	}
	if err := foundationReconciler.ReconcileCustomFields(customFields); err != nil {
		logger.Error("Failed to reconcile custom fields", err)
		return err
	}

	platforms, err := dataLoader.LoadPlatforms("definitions/platforms")
	if err != nil {
		logger.Error("Failed to load platforms", err)
		return err
	}
	if err := foundationReconciler.ReconcilePlatforms(platforms); err != nil {
		logger.Error("Failed to reconcile platforms", err)
		return err
	}

	// Tenant groups before tenants: a tenant may reference a group created here.
	tenantGroups, err := dataLoader.LoadTenantGroups("definitions/tenant_groups")
	if err != nil {
		logger.Error("Failed to load tenant groups", err)
		return err
	}
	if err := foundationReconciler.ReconcileTenantGroups(tenantGroups); err != nil {
		logger.Error("Failed to reconcile tenant groups", err)
		return err
	}

	tenants, err := dataLoader.LoadTenants("definitions/tenants")
	if err != nil {
		logger.Error("Failed to load tenants", err)
		return err
	}
	if err := foundationReconciler.ReconcileTenants(tenants); err != nil {
		logger.Error("Failed to reconcile tenants", err)
		return err
	}

	sites, err := dataLoader.LoadSites("definitions/sites")
	if err != nil {
		logger.Error("Failed to load sites", err)
		return err
	}
	if err := foundationReconciler.ReconcileSites(sites); err != nil {
		logger.Error("Failed to reconcile sites", err)
		return err
	}

	racks, err := dataLoader.LoadRacks("definitions/racks")
	if err != nil {
		logger.Error("Failed to load racks", err)
		return err
	}
	if err := foundationReconciler.ReconcileRacks(racks); err != nil {
		logger.Error("Failed to reconcile racks", err)
		return err
	}

	return nil
}

// runNetwork reconciles VRFs, VLAN groups, VLANs and prefixes.
func runNetwork(c *client.NetBoxClient, dataLoader *loader.DataLoader, logger *utils.Logger) error {
	networkReconciler := reconciler.NewNetworkReconciler(c)

	vrfs, err := dataLoader.LoadVRFs("definitions/vrfs")
	if err != nil {
		logger.Error("Failed to load VRFs", err)
		return err
	}
	if err := networkReconciler.ReconcileVRFs(vrfs); err != nil {
		logger.Error("Failed to reconcile VRFs", err)
		return err
	}

	vlanGroups, err := dataLoader.LoadVLANGroups("definitions/vlan_groups")
	if err != nil {
		logger.Error("Failed to load VLAN groups", err)
		return err
	}
	if err := networkReconciler.ReconcileVLANGroups(vlanGroups); err != nil {
		logger.Error("Failed to reconcile VLAN groups", err)
		return err
	}

	vlans, err := dataLoader.LoadVLANs("definitions/vlans")
	if err != nil {
		logger.Error("Failed to load VLANs", err)
		return err
	}
	if err := networkReconciler.ReconcileVLANs(vlans); err != nil {
		logger.Error("Failed to reconcile VLANs", err)
		return err
	}

	prefixes, err := dataLoader.LoadPrefixes("definitions/prefixes")
	if err != nil {
		logger.Error("Failed to load prefixes", err)
		return err
	}
	if err := networkReconciler.ReconcilePrefixes(prefixes); err != nil {
		logger.Error("Failed to reconcile prefixes", err)
		return err
	}

	return nil
}

// runDeviceTypes reconciles module types and device types.
func runDeviceTypes(c *client.NetBoxClient, dataLoader *loader.DataLoader, logger *utils.Logger) error {
	deviceTypeReconciler := reconciler.NewDeviceTypeReconciler(c)

	moduleTypes, err := dataLoader.LoadModuleTypes("definitions/module_types")
	if err != nil {
		logger.Error("Failed to load module types", err)
		return err
	}
	if err := deviceTypeReconciler.ReconcileModuleTypes(moduleTypes); err != nil {
		logger.Error("Failed to reconcile module types", err)
		return err
	}

	deviceTypes, err := dataLoader.LoadDeviceTypes("definitions/device_types")
	if err != nil {
		logger.Error("Failed to load device types", err)
		return err
	}
	if err := deviceTypeReconciler.ReconcileDeviceTypes(deviceTypes); err != nil {
		logger.Error("Failed to reconcile device types", err)
		return err
	}

	return nil
}

// runDevices loads the device inventory, applies the --site/--device
// filters, loads the required site caches and reconciles the devices.
func runDevices(c *client.NetBoxClient, dataLoader *loader.DataLoader, logger *utils.Logger) error {
	activeDevices, err := dataLoader.LoadDevices("inventory/hardware/active")
	if err != nil {
		logger.Error("Failed to load active devices", err)
		return err
	}

	passiveDevices, err := dataLoader.LoadDevices("inventory/hardware/passive")
	if err != nil {
		logger.Error("Failed to load passive devices", err)
		return err
	}

	allDevices := append(activeDevices, passiveDevices...)
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
	if err := deviceReconciler.ReconcileDevices(allDevices); err != nil {
		logger.Error("Failed to reconcile devices", err)
		return err
	}

	return nil
}

// runVirtualization reconciles cluster types, cluster groups and clusters.
// (Virtual machines are reconciled in a later step.)
func runVirtualization(c *client.NetBoxClient, dataLoader *loader.DataLoader, logger *utils.Logger) error {
	virtReconciler := reconciler.NewVirtualizationReconciler(c)

	clusterTypes, err := dataLoader.LoadClusterTypes("definitions/virtualization/cluster_types")
	if err != nil {
		logger.Error("Failed to load cluster types", err)
		return err
	}
	if err := virtReconciler.ReconcileClusterTypes(clusterTypes); err != nil {
		logger.Error("Failed to reconcile cluster types", err)
		return err
	}

	clusterGroups, err := dataLoader.LoadClusterGroups("definitions/virtualization/cluster_groups")
	if err != nil {
		logger.Error("Failed to load cluster groups", err)
		return err
	}
	if err := virtReconciler.ReconcileClusterGroups(clusterGroups); err != nil {
		logger.Error("Failed to reconcile cluster groups", err)
		return err
	}

	clusters, err := dataLoader.LoadClusters("definitions/virtualization/clusters")
	if err != nil {
		logger.Error("Failed to load clusters", err)
		return err
	}
	if err := virtReconciler.ReconcileClusters(clusters); err != nil {
		logger.Error("Failed to reconcile clusters", err)
		return err
	}

	vms, err := dataLoader.LoadVMs("inventory/virtual")
	if err != nil {
		logger.Error("Failed to load virtual machines", err)
		return err
	}

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
	for _, cl := range clusters {
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
// Cables are intentionally excluded: the tool does not tag them, so the
// managed-tag query would never return them, and NetBox already
// cascade-deletes a port's cables when the port or device is removed.
func pruneTargets(phases map[string]bool) []client.PruneTarget {
	var targets []client.PruneTarget

	// IP addresses reference both device interfaces and VM interfaces, so they
	// are pruned once up front (before any interface is removed) whenever
	// either owning phase ran.
	if phases["devices"] || phases["virtualization"] {
		targets = append(targets, client.PruneTarget{App: "ipam", Endpoint: "ip-addresses"})
	}

	if phases["devices"] {
		// Device children first (reverse dependency: front ports reference rear
		// ports, all components reference the device), then the devices
		// themselves. Deleting an orphaned device cascades any remaining
		// managed children in NetBox.
		targets = append(targets,
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

// resolveDataDir determines the correct data directory to use
// It implements auto-detection: if definitions/ doesn't exist in the specified directory,
// it falls back to the example/ directory
func resolveDataDir(dir string, logger *utils.Logger) (string, error) {
	// Check if definitions directory exists in the specified directory
	definitionsPath := fmt.Sprintf("%s/definitions", dir)
	if _, err := os.Stat(definitionsPath); err == nil {
		logger.Info("Using data directory: %s", dir)
		return dir, nil
	}

	// If not in current directory, check if example/ directory exists
	examplePath := "example"
	exampleDefinitionsPath := fmt.Sprintf("%s/definitions", examplePath)
	if _, err := os.Stat(exampleDefinitionsPath); err == nil {
		logger.Warning("definitions/ not found in '%s', falling back to '%s'", dir, examplePath)
		return examplePath, nil
	}

	return "", fmt.Errorf("no valid data directory found: checked '%s' and '%s'", dir, examplePath)
}
