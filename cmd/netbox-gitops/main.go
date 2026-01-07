package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/reconciler"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

var (
	dryRun     bool
	configFile string
	dataDir    string
)

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

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runSync(cmd *cobra.Command, args []string) error {
	logger := utils.NewLogger(dryRun)

	// Auto-detect and validate data directory
	dataDir, err := resolveDataDir(dataDir, logger)
	if err != nil {
		logger.Error("Failed to resolve data directory", err)
		return err
	}

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
	// PHASE 1: FOUNDATION
	// =========================================================================
	logger.Info("═══════════════════════════════════════════════════════")
	logger.Info("Phase 1: Foundation")
	logger.Info("═══════════════════════════════════════════════════════")

	foundationReconciler := reconciler.NewFoundationReconciler(c)

	// Load and reconcile tags
	tags, err := dataLoader.LoadTags(buildPath(dataDir, "definitions/extras"))
	if err != nil {
		logger.Error("Failed to load tags", err)
		return err
	}
	if err := foundationReconciler.ReconcileTags(tags); err != nil {
		logger.Error("Failed to reconcile tags", err)
		return err
	}

	// Load and reconcile roles
	roles, err := dataLoader.LoadRoles(buildPath(dataDir, "definitions/roles"))
	if err != nil {
		logger.Error("Failed to load roles", err)
		return err
	}
	if err := foundationReconciler.ReconcileRoles(roles); err != nil {
		logger.Error("Failed to reconcile roles", err)
		return err
	}

	// Load and reconcile sites
	sites, err := dataLoader.LoadSites(buildPath(dataDir, "definitions/sites"))
	if err != nil {
		logger.Error("Failed to load sites", err)
		return err
	}
	if err := foundationReconciler.ReconcileSites(sites); err != nil {
		logger.Error("Failed to reconcile sites", err)
		return err
	}

	// Load and reconcile racks
	racks, err := dataLoader.LoadRacks(buildPath(dataDir, "definitions/racks"))
	if err != nil {
		logger.Error("Failed to load racks", err)
		return err
	}
	if err := foundationReconciler.ReconcileRacks(racks); err != nil {
		logger.Error("Failed to reconcile racks", err)
		return err
	}

	// =========================================================================
	// PHASE 2: NETWORK & TYPES
	// =========================================================================
	logger.Info("═══════════════════════════════════════════════════════")
	logger.Info("Phase 2: Network & Types")
	logger.Info("═══════════════════════════════════════════════════════")

	networkReconciler := reconciler.NewNetworkReconciler(c)

	// Load and reconcile VRFs
	vrfs, err := dataLoader.LoadVRFs(buildPath(dataDir, "definitions/vrfs"))
	if err != nil {
		logger.Error("Failed to load VRFs", err)
		return err
	}
	if err := networkReconciler.ReconcileVRFs(vrfs); err != nil {
		logger.Error("Failed to reconcile VRFs", err)
		return err
	}

	// Load and reconcile VLAN groups
	vlanGroups, err := dataLoader.LoadVLANGroups(buildPath(dataDir, "definitions/vlan_groups"))
	if err != nil {
		logger.Error("Failed to load VLAN groups", err)
		return err
	}
	if err := networkReconciler.ReconcileVLANGroups(vlanGroups); err != nil {
		logger.Error("Failed to reconcile VLAN groups", err)
		return err
	}

	// Load and reconcile VLANs
	vlans, err := dataLoader.LoadVLANs(buildPath(dataDir, "definitions/vlans"))
	if err != nil {
		logger.Error("Failed to load VLANs", err)
		return err
	}
	if err := networkReconciler.ReconcileVLANs(vlans); err != nil {
		logger.Error("Failed to reconcile VLANs", err)
		return err
	}

	// Load and reconcile prefixes
	prefixes, err := dataLoader.LoadPrefixes(buildPath(dataDir, "definitions/prefixes"))
	if err != nil {
		logger.Error("Failed to load prefixes", err)
		return err
	}
	if err := networkReconciler.ReconcilePrefixes(prefixes); err != nil {
		logger.Error("Failed to reconcile prefixes", err)
		return err
	}

	// Device types
	deviceTypeReconciler := reconciler.NewDeviceTypeReconciler(c)

	// Load and reconcile module types
	moduleTypes, err := dataLoader.LoadModuleTypes(buildPath(dataDir, "definitions/module_types"))
	if err != nil {
		logger.Error("Failed to load module types", err)
		return err
	}
	if err := deviceTypeReconciler.ReconcileModuleTypes(moduleTypes); err != nil {
		logger.Error("Failed to reconcile module types", err)
		return err
	}

	// Load and reconcile device types
	deviceTypes, err := dataLoader.LoadDeviceTypes(buildPath(dataDir, "definitions/device_types"))
	if err != nil {
		logger.Error("Failed to load device types", err)
		return err
	}
	if err := deviceTypeReconciler.ReconcileDeviceTypes(deviceTypes); err != nil {
		logger.Error("Failed to reconcile device types", err)
		return err
	}

	// =========================================================================
	// PHASE 3: DEVICES
	// =========================================================================
	logger.Info("═══════════════════════════════════════════════════════")
	logger.Info("Phase 3: Devices")
	logger.Info("═══════════════════════════════════════════════════════")

	// Load global caches
	logger.Info("Loading global caches...")
	if err := c.Cache().LoadGlobal(); err != nil {
		logger.Error("Failed to load global caches", err)
		return err
	}

	// Load devices from inventory
	activeDevices, err := dataLoader.LoadDevices(buildPath(dataDir, "inventory/hardware/active"))
	if err != nil {
		logger.Error("Failed to load active devices", err)
		return err
	}

	passiveDevices, err := dataLoader.LoadDevices(buildPath(dataDir, "inventory/hardware/passive"))
	if err != nil {
		logger.Error("Failed to load passive devices", err)
		return err
	}

	allDevices := append(activeDevices, passiveDevices...)
	logger.Info("Loaded %d devices from inventory", len(allDevices))

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

	// Reconcile devices
	deviceReconciler := reconciler.NewDeviceReconciler(c)
	if err := deviceReconciler.ReconcileDevices(allDevices); err != nil {
		logger.Error("Failed to reconcile devices", err)
		return err
	}

	// =========================================================================
	// SUMMARY
	// =========================================================================
	logger.Info("═══════════════════════════════════════════════════════")
	if dryRun {
		logger.Warning("DRY RUN COMPLETE: No changes applied")
	} else {
		logger.Success("SYNC COMPLETE: Changes applied successfully")
	}
	logger.Info("═══════════════════════════════════════════════════════")

	return nil
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

// buildPath constructs a path relative to the data directory
func buildPath(dataDir, subPath string) string {
	if dataDir == "." {
		return subPath
	}
	return fmt.Sprintf("%s/%s", dataDir, subPath)
}
