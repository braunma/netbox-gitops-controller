package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func TestDataLoaderInitialization(t *testing.T) {
	logger := utils.NewLogger(true)
	loader := NewDataLoader("/test/path", logger)

	if loader == nil {
		t.Fatal("NewDataLoader() returned nil")
	}

	if loader.logger == nil {
		t.Error("DataLoader logger is nil")
	}
}

// TestLoadDevicesGroupedDefaults verifies the grouped `defaults` + `devices`
// file form: defaults are applied to every device, per-device values win,
// and tags are unioned instead of replaced.
func TestLoadDevicesGroupedDefaults(t *testing.T) {
	dir := t.TempDir()
	deviceDir := filepath.Join(dir, "devices")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `
defaults:
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server-r100"
  rack_slug: "rack-a01"
  status: "active"
  tags: ["gitops", "production"]

devices:
  - name: "web-01"
    position: 10
    interfaces:
      - name: "eth0"
        ip:
          address: "10.0.0.1/24"

  - name: "web-02"
    position: 12
    role_slug: "storage"
    tags: ["lab", "gitops"]
`
	if err := os.WriteFile(filepath.Join(deviceDir, "servers.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDataLoader(dir, utils.NewLogger(false))
	devices, err := loader.LoadDevices("devices")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("LoadDevices() returned %d devices, expected 2", len(devices))
	}

	web01 := devices[0]
	if web01.Name != "web-01" || web01.SiteSlug != "berlin-dc" || web01.RoleSlug != "server" ||
		web01.DeviceTypeSlug != "example-server-r100" || web01.RackSlug != "rack-a01" ||
		web01.Status != "active" || web01.Position != 10 {
		t.Errorf("web-01 defaults not applied correctly: %+v", web01)
	}
	if len(web01.Interfaces) != 1 || web01.Interfaces[0].IP == nil || web01.Interfaces[0].IP.Address != "10.0.0.1/24" {
		t.Errorf("web-01 interfaces not preserved: %+v", web01.Interfaces)
	}
	if len(web01.Tags) != 2 || web01.Tags[0] != "gitops" || web01.Tags[1] != "production" {
		t.Errorf("web-01 tags = %v, expected [gitops production]", web01.Tags)
	}

	web02 := devices[1]
	if web02.RoleSlug != "storage" {
		t.Errorf("web-02 role_slug = %q, device value should win over default", web02.RoleSlug)
	}
	expectedTags := []string{"gitops", "production", "lab"}
	if len(web02.Tags) != len(expectedTags) {
		t.Fatalf("web-02 tags = %v, expected %v", web02.Tags, expectedTags)
	}
	for i, tag := range expectedTags {
		if web02.Tags[i] != tag {
			t.Errorf("web-02 tags = %v, expected %v", web02.Tags, expectedTags)
			break
		}
	}
}

// TestLoadDevicesGroupedFormRejectsStrayKeys verifies that a top-level key
// next to `devices` fails loudly instead of being silently dropped.
func TestLoadDevicesGroupedFormRejectsStrayKeys(t *testing.T) {
	dir := t.TempDir()
	deviceDir := filepath.Join(dir, "devices")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `
defaults:
  site_slug: "berlin-dc"
name: "orphaned-device"
devices:
  - name: "web-01"
    role_slug: "server"
    device_type_slug: "example-server-r100"
`
	if err := os.WriteFile(filepath.Join(deviceDir, "servers.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDataLoader(dir, utils.NewLogger(false))
	if _, err := loader.LoadDevices("devices"); err == nil {
		t.Error("LoadDevices() should reject stray top-level keys in grouped form")
	}
}

// TestInterfaceEnabledDefault verifies that interfaces are enabled unless
// explicitly disabled, so `enabled: true` boilerplate is not required.
func TestInterfaceEnabledDefault(t *testing.T) {
	dir := t.TempDir()
	deviceDir := filepath.Join(dir, "devices")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `
- name: "web-01"
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server-r100"
  interfaces:
    - name: "eth0"
    - name: "eth1"
      enabled: false
    - name: "eth2"
      enabled: true
`
	if err := os.WriteFile(filepath.Join(deviceDir, "servers.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDataLoader(dir, utils.NewLogger(false))
	devices, err := loader.LoadDevices("devices")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	if len(devices) != 1 || len(devices[0].Interfaces) != 3 {
		t.Fatalf("unexpected devices/interfaces: %+v", devices)
	}

	ifaces := devices[0].Interfaces
	if !ifaces[0].IsEnabled() {
		t.Error("eth0 (enabled omitted) should default to enabled")
	}
	if ifaces[1].IsEnabled() {
		t.Error("eth1 (enabled: false) should be disabled")
	}
	if !ifaces[2].IsEnabled() {
		t.Error("eth2 (enabled: true) should be enabled")
	}
}

// TestIPShorthand verifies that `ip:` accepts a plain CIDR string as a
// shorthand for the full mapping form.
func TestIPShorthand(t *testing.T) {
	dir := t.TempDir()
	deviceDir := filepath.Join(dir, "devices")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `
- name: "web-01"
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server-r100"
  interfaces:
    - name: "eth0"
      ip: "10.0.0.1/24"
    - name: "eth1"
      ip:
        address: "10.0.1.1/24"
        vrf: "Production"
`
	if err := os.WriteFile(filepath.Join(deviceDir, "servers.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDataLoader(dir, utils.NewLogger(false))
	devices, err := loader.LoadDevices("devices")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	if len(devices) != 1 || len(devices[0].Interfaces) != 2 {
		t.Fatalf("unexpected devices/interfaces: %+v", devices)
	}

	eth0 := devices[0].Interfaces[0]
	if eth0.IP == nil || eth0.IP.Address != "10.0.0.1/24" {
		t.Errorf("eth0 IP shorthand not parsed: %+v", eth0.IP)
	}
	eth1 := devices[0].Interfaces[1]
	if eth1.IP == nil || eth1.IP.Address != "10.0.1.1/24" || eth1.IP.VRF != "Production" {
		t.Errorf("eth1 IP mapping form not parsed: %+v", eth1.IP)
	}
}

// Integration tests for example YAML files
func TestLoadDefinitionFiles(t *testing.T) {
	// Skip if not in project directory
	if _, err := os.Stat("../../example/definitions"); os.IsNotExist(err) {
		t.Skip("Skipping integration test - example definitions directory not found")
	}

	logger := utils.NewLogger(true)
	loader := NewDataLoader("../../example", logger)

	t.Run("Load Tags", func(t *testing.T) {
		tags, err := loader.LoadTags("definitions/extras")
		if err != nil {
			t.Errorf("LoadTags() error = %v", err)
		}
		if len(tags) == 0 {
			t.Error("LoadTags() returned 0 tags")
		}

		// Verify gitops tag exists
		foundGitOps := false
		for _, tag := range tags {
			if tag.Slug == "gitops" {
				foundGitOps = true
				if tag.Name != "GitOps Managed" {
					t.Errorf("GitOps tag name = %q, expected %q", tag.Name, "GitOps Managed")
				}
			}
		}
		if !foundGitOps {
			t.Error("GitOps tag not found in loaded tags")
		}
	})

	t.Run("Load Roles", func(t *testing.T) {
		roles, err := loader.LoadRoles("definitions/roles")
		if err != nil {
			t.Errorf("LoadRoles() error = %v", err)
		}
		if len(roles) == 0 {
			t.Error("LoadRoles() returned 0 roles")
		}

		// Verify server role exists
		foundServer := false
		for _, role := range roles {
			if role.Slug == "server" {
				foundServer = true
				if role.VMRole {
					t.Error("Server role should not be a VM role")
				}
			}
		}
		if !foundServer {
			t.Error("Server role not found in loaded roles")
		}
	})

	t.Run("Load Sites", func(t *testing.T) {
		sites, err := loader.LoadSites("definitions/sites")
		if err != nil {
			t.Errorf("LoadSites() error = %v", err)
		}
		if len(sites) == 0 {
			t.Error("LoadSites() returned 0 sites")
		}

		// Verify Berlin DC exists
		foundBerlin := false
		for _, site := range sites {
			if site.Slug == "berlin-dc" {
				foundBerlin = true
				if site.Status != "active" {
					t.Errorf("Berlin DC status = %q, expected %q", site.Status, "active")
				}
				if site.TimeZone != "Europe/Berlin" {
					t.Errorf("Berlin DC timezone = %q, expected %q", site.TimeZone, "Europe/Berlin")
				}
			}
		}
		if !foundBerlin {
			t.Error("Berlin DC not found in loaded sites")
		}
	})

	t.Run("Load Racks", func(t *testing.T) {
		racks, err := loader.LoadRacks("definitions/racks")
		if err != nil {
			t.Errorf("LoadRacks() error = %v", err)
		}
		if len(racks) == 0 {
			t.Error("LoadRacks() returned 0 racks")
		}

		// Verify rack has required fields
		for _, rack := range racks {
			if rack.Name == "" {
				t.Error("Rack has empty name")
			}
			if rack.Slug == "" {
				t.Error("Rack has empty slug")
			}
			if rack.SiteSlug == "" {
				t.Error("Rack has empty site_slug")
			}
		}
	})

	t.Run("Load VRFs", func(t *testing.T) {
		vrfs, err := loader.LoadVRFs("definitions/vrfs")
		if err != nil {
			t.Errorf("LoadVRFs() error = %v", err)
		}
		if len(vrfs) == 0 {
			t.Error("LoadVRFs() returned 0 VRFs")
		}

		// Verify Management VRF exists
		foundMgmt := false
		for _, vrf := range vrfs {
			if vrf.Name == "Management" {
				foundMgmt = true
				if vrf.RD != "65000:10" {
					t.Errorf("Management VRF RD = %q, expected %q", vrf.RD, "65000:10")
				}
				if !vrf.EnforceUnique {
					t.Error("Management VRF should enforce unique")
				}
			}
		}
		if !foundMgmt {
			t.Error("Management VRF not found")
		}
	})

	t.Run("Load VLAN Groups", func(t *testing.T) {
		groups, err := loader.LoadVLANGroups("definitions/vlan_groups")
		if err != nil {
			t.Errorf("LoadVLANGroups() error = %v", err)
		}
		if len(groups) == 0 {
			t.Error("LoadVLANGroups() returned 0 VLAN groups")
		}

		// Verify group has valid VID range
		for _, group := range groups {
			if group.MinVID < 1 || group.MinVID > 4094 {
				t.Errorf("VLAN group %s has invalid MinVID: %d", group.Name, group.MinVID)
			}
			if group.MaxVID < 1 || group.MaxVID > 4094 {
				t.Errorf("VLAN group %s has invalid MaxVID: %d", group.Name, group.MaxVID)
			}
			if group.MinVID > group.MaxVID {
				t.Errorf("VLAN group %s has MinVID > MaxVID", group.Name)
			}
		}
	})

	t.Run("Load VLANs", func(t *testing.T) {
		vlans, err := loader.LoadVLANs("definitions/vlans")
		if err != nil {
			t.Errorf("LoadVLANs() error = %v", err)
		}
		if len(vlans) == 0 {
			t.Error("LoadVLANs() returned 0 VLANs")
		}

		// Verify VLAN structure
		for _, vlan := range vlans {
			if vlan.VID < 1 || vlan.VID > 4094 {
				t.Errorf("VLAN %s has invalid VID: %d", vlan.Name, vlan.VID)
			}
			if vlan.SiteSlug == "" {
				t.Errorf("VLAN %s has no site_slug", vlan.Name)
			}
			if vlan.Status == "" {
				t.Errorf("VLAN %s has no status", vlan.Name)
			}
		}
	})

	t.Run("Load Prefixes", func(t *testing.T) {
		prefixes, err := loader.LoadPrefixes("definitions/prefixes")
		if err != nil {
			t.Errorf("LoadPrefixes() error = %v", err)
		}
		if len(prefixes) == 0 {
			t.Error("LoadPrefixes() returned 0 prefixes")
		}

		// Verify prefix format
		for _, prefix := range prefixes {
			if prefix.Prefix == "" {
				t.Error("Prefix has empty prefix field")
			}
			if prefix.Status == "" {
				t.Error("Prefix has no status")
			}
		}
	})

	t.Run("Load Device Types", func(t *testing.T) {
		deviceTypes, err := loader.LoadDeviceTypes("definitions/device_types")
		// Device type files may be single objects or arrays; both are supported
		if err != nil {
			t.Errorf("LoadDeviceTypes() error = %v", err)
		}
		if len(deviceTypes) == 0 {
			t.Error("LoadDeviceTypes() returned no device types")
		}

		// Verify device type structure for what we got
		for _, dt := range deviceTypes {
			if dt.Model == "" {
				t.Error("DeviceType has empty model")
			}
			if dt.Slug == "" {
				t.Error("DeviceType has empty slug")
			}
			if dt.Manufacturer == "" {
				t.Error("DeviceType has empty manufacturer")
			}
			// Child device types (blades) must have u_height 0
			if dt.UHeight < 0 {
				t.Errorf("DeviceType %s has invalid UHeight: %d", dt.Model, dt.UHeight)
			}
		}
	})

	t.Run("Load Module Types", func(t *testing.T) {
		moduleTypes, err := loader.LoadModuleTypes("definitions/module_types")
		if err != nil {
			t.Errorf("LoadModuleTypes() error = %v", err)
		}
		// Module types may be optional, so don't error if 0
		if moduleTypes == nil {
			t.Error("LoadModuleTypes() returned nil")
		}
	})

	t.Run("Load Platforms", func(t *testing.T) {
		platforms, err := loader.LoadPlatforms("definitions/platforms")
		if err != nil {
			t.Errorf("LoadPlatforms() error = %v", err)
		}
		if len(platforms) == 0 {
			t.Error("LoadPlatforms() returned 0 platforms")
		}
		for _, p := range platforms {
			if p.Name == "" || p.Slug == "" {
				t.Errorf("Platform has empty name/slug: %+v", p)
			}
		}
	})

	t.Run("Load Tenant Groups and Tenants", func(t *testing.T) {
		groups, err := loader.LoadTenantGroups("definitions/tenant_groups")
		if err != nil {
			t.Errorf("LoadTenantGroups() error = %v", err)
		}
		if len(groups) == 0 {
			t.Error("LoadTenantGroups() returned 0 groups")
		}
		tenants, err := loader.LoadTenants("definitions/tenants")
		if err != nil {
			t.Errorf("LoadTenants() error = %v", err)
		}
		if len(tenants) == 0 {
			t.Error("LoadTenants() returned 0 tenants")
		}
	})

	t.Run("Load Clusters", func(t *testing.T) {
		clusterTypes, err := loader.LoadClusterTypes("definitions/virtualization/cluster_types")
		if err != nil {
			t.Errorf("LoadClusterTypes() error = %v", err)
		}
		if len(clusterTypes) == 0 {
			t.Error("LoadClusterTypes() returned 0 cluster types")
		}

		if _, err := loader.LoadClusterGroups("definitions/virtualization/cluster_groups"); err != nil {
			t.Errorf("LoadClusterGroups() error = %v", err)
		}

		clusters, err := loader.LoadClusters("definitions/virtualization/clusters")
		if err != nil {
			t.Errorf("LoadClusters() error = %v", err)
		}
		if len(clusters) == 0 {
			t.Error("LoadClusters() returned 0 clusters")
		}
		for _, cl := range clusters {
			if cl.Name == "" || cl.TypeSlug == "" {
				t.Errorf("Cluster has empty name/type_slug: %+v", cl)
			}
		}
	})
}

func TestLoadInventoryFiles(t *testing.T) {
	// Skip if not in project directory
	if _, err := os.Stat("../../example/inventory"); os.IsNotExist(err) {
		t.Skip("Skipping integration test - example inventory directory not found")
	}

	logger := utils.NewLogger(true)
	loader := NewDataLoader("../../example", logger)

	t.Run("Load Active Devices", func(t *testing.T) {
		devices, err := loader.LoadDevices("inventory/hardware/active")
		if err != nil {
			t.Errorf("LoadDevices(active) error = %v", err)
		}
		if len(devices) == 0 {
			t.Error("LoadDevices(active) returned 0 devices")
		}

		// Verify device structure
		for _, device := range devices {
			if device.Name == "" {
				t.Error("Device has empty name")
			}
			if device.SiteSlug == "" {
				t.Errorf("Device %s has no site_slug", device.Name)
			}
			if device.DeviceTypeSlug == "" {
				t.Errorf("Device %s has no device_type_slug", device.Name)
			}
			if device.RoleSlug == "" {
				t.Errorf("Device %s has no role_slug", device.Name)
			}

			// Verify cable links if present
			for _, iface := range device.Interfaces {
				if iface.Link != nil {
					if iface.Link.PeerDevice == "" {
						t.Errorf("Interface %s on %s has link with empty peer_device", iface.Name, device.Name)
					}
					if iface.Link.PeerPort == "" {
						t.Errorf("Interface %s on %s has link with empty peer_port", iface.Name, device.Name)
					}
				}
			}
		}
	})

	t.Run("Load Passive Devices", func(t *testing.T) {
		devices, err := loader.LoadDevices("inventory/hardware/passive")
		if err != nil {
			t.Errorf("LoadDevices(passive) error = %v", err)
		}
		if len(devices) == 0 {
			t.Error("LoadDevices(passive) returned 0 devices")
		}

		// Verify passive device structure (patch panels)
		for _, device := range devices {
			// Patch panels should have front ports and rear ports
			hasAnyPorts := len(device.FrontPorts) > 0 || len(device.RearPorts) > 0
			if !hasAnyPorts {
				t.Logf("Warning: Passive device %s has no front or rear ports", device.Name)
			}

			// Verify front ports link to rear ports
			for _, fp := range device.FrontPorts {
				if fp.RearPort == "" {
					t.Errorf("Front port %s on %s has no rear_port reference", fp.Name, device.Name)
				}
			}
		}
	})

	t.Run("Load Virtual Machines", func(t *testing.T) {
		vms, err := loader.LoadVMs("inventory/virtual")
		if err != nil {
			t.Errorf("LoadVMs() error = %v", err)
		}
		if len(vms) == 0 {
			t.Error("LoadVMs() returned 0 virtual machines")
		}
		for _, vm := range vms {
			if vm.Name == "" {
				t.Error("VM has empty name")
			}
			// Each VM must belong to a cluster or a site (validated on load).
			if vm.Cluster == "" && vm.SiteSlug == "" {
				t.Errorf("VM %s has neither cluster nor site_slug", vm.Name)
			}
			// Verify nested interface IPs round-trip.
			for _, iface := range vm.Interfaces {
				if iface.IP != nil && iface.IP.Address == "" {
					t.Errorf("VM %s interface %s has an IP block with no address", vm.Name, iface.Name)
				}
			}
		}
	})
}

func TestYAMLFileValidation(t *testing.T) {
	// Skip if not in project directory
	if _, err := os.Stat("../../example/definitions"); os.IsNotExist(err) {
		t.Skip("Skipping integration test - example definitions directory not found")
	}

	// Find all YAML files
	yamlFiles := []string{}

	definitionsRoot := "../../example/definitions"
	err := filepath.Walk(definitionsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			yamlFiles = append(yamlFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to walk example definitions directory: %v", err)
	}

	inventoryRoot := "../../example/inventory"
	err = filepath.Walk(inventoryRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			yamlFiles = append(yamlFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to walk example inventory directory: %v", err)
	}

	if len(yamlFiles) == 0 {
		t.Fatal("No YAML files found")
	}

	t.Logf("Found %d YAML files to validate", len(yamlFiles))

	// Try to read each file
	for _, yamlFile := range yamlFiles {
		t.Run(yamlFile, func(t *testing.T) {
			data, err := os.ReadFile(yamlFile)
			if err != nil {
				t.Errorf("Failed to read %s: %v", yamlFile, err)
				return
			}

			if len(data) == 0 {
				t.Errorf("File %s is empty", yamlFile)
			}
		})
	}
}
