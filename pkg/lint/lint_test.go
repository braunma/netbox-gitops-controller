// SPDX-License-Identifier: Apache-2.0

package lint

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// baseDataset is a minimal but complete set of definitions: two racks at one
// site, a 2U server type with an iDRAC and two NICs, and a 1U switch type.
func baseDataset() Dataset {
	return Dataset{
		Sites: []*models.Site{{Name: "Berlin", Slug: "berlin"}},
		Racks: []*models.Rack{
			{Name: "Rack A01", Slug: "rack-a01", SiteSlug: "berlin"},
			{Name: "Rack A02", Slug: "rack-a02", SiteSlug: "berlin"},
		},
		Roles: []*models.Role{
			{Name: "Server", Slug: "server", Color: "4caf50"},
			{Name: "Leaf", Slug: "leaf", Color: "2196f3"},
		},
		VRFs:     []*models.VRF{{Name: "Production"}},
		VLANs:    []*models.VLAN{{Name: "Servers", VID: 20, SiteSlug: "berlin"}},
		Prefixes: []*models.Prefix{{Prefix: "10.0.0.0/16"}},
		DeviceTypes: []*models.DeviceType{
			{
				Model: "R740", Slug: "r740", Manufacturer: "Dell", UHeight: 2,
				Interfaces: []models.InterfaceTemplate{
					{Name: "idrac", Type: "1000base-t", MgmtOnly: true},
					{Name: "eth0", Type: "25gbase-x-sfp28"},
					{Name: "eth1", Type: "25gbase-x-sfp28"},
				},
				ModuleBays: []models.ModuleBayTemplate{{Name: "OCP-1"}},
			},
			{
				Model: "Leaf", Slug: "leaf-sw", Manufacturer: "Dell", UHeight: 1,
				Interfaces: []models.InterfaceTemplate{
					{Name: "Eth1/1", Type: "25gbase-x-sfp28"},
					{Name: "Eth1/2", Type: "25gbase-x-sfp28"},
				},
			},
		},
		ModuleTypes: []*models.ModuleType{
			{Model: "OCP 25G", Slug: "ocp-25g", Manufacturer: "Dell"},
		},
	}
}

func server(name string, position int) *models.DeviceConfig {
	return &models.DeviceConfig{
		Name: name, SiteSlug: "berlin", RoleSlug: "server",
		DeviceTypeSlug: "r740", RackSlug: "rack-a01", Position: position, Face: "front",
	}
}

// findingsFor returns the messages of every finding produced by the named check.
func findingsFor(findings []Finding, check string) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func assertCheck(t *testing.T, findings []Finding, check string, want int) []Finding {
	t.Helper()
	got := findingsFor(findings, check)
	if len(got) != want {
		t.Errorf("check %q: got %d findings, want %d\nall findings:\n%s",
			check, len(got), want, render(findings))
	}
	return got
}

func render(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("  " + f.String() + "\n")
	}
	if b.Len() == 0 {
		return "  (none)\n"
	}
	return b.String()
}

func TestCleanDatasetHasNoFindings(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "idrac", IP: &models.IPConfig{Address: "10.0.10.1/24"}},
		{
			Name:        "eth0",
			IP:          &models.IPConfig{Address: "10.0.20.1/24", VRF: "Production"},
			AddressRole: "primary",
			Link:        &models.LinkConfig{PeerDevice: "leaf-01", PeerPort: "Eth1/1"},
		},
	}
	leaf := &models.DeviceConfig{
		Name: "leaf-01", SiteSlug: "berlin", RoleSlug: "leaf",
		DeviceTypeSlug: "leaf-sw", RackSlug: "rack-a01", Position: 40, Face: "front",
		Interfaces: []models.InterfaceConfig{
			{Name: "Eth1/1", Mode: "access", UntaggedVLAN: "Servers"},
		},
	}
	ds.Devices = []*models.DeviceConfig{srv, leaf}

	if findings := Check(ds, Options{}); len(findings) != 0 {
		t.Errorf("expected a clean dataset to produce no findings, got:\n%s", render(findings))
	}
}

func TestDuplicateDeviceName(t *testing.T) {
	ds := baseDataset()
	ds.Devices = []*models.DeviceConfig{server("srv-01", 10), server("srv-01", 20)}

	assertCheck(t, Check(ds, Options{}), "duplicate-device", 1)
}

func TestUnknownReferences(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.SiteSlug = "berln"
	srv.RoleSlug = "srvr"
	srv.RackSlug = "rack-z99"
	srv.DeviceTypeSlug = "r470"
	ds.Devices = []*models.DeviceConfig{srv}

	findings := Check(ds, Options{})
	for _, check := range []string{"unknown-site", "unknown-role", "unknown-rack", "unknown-device-type"} {
		got := assertCheck(t, findings, check, 1)
		if len(got) == 1 && got[0].Severity != Error {
			t.Errorf("check %q: got severity %v, want error", check, got[0].Severity)
		}
	}
}

func TestUnknownReferencesDemotedByOption(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.SiteSlug = "berln"
	ds.Devices = []*models.DeviceConfig{srv}

	got := assertCheck(t, Check(ds, Options{AllowUndeclaredRefs: true}), "unknown-site", 1)
	if len(got) == 1 && got[0].Severity != Warning {
		t.Errorf("got severity %v, want warning with AllowUndeclaredRefs", got[0].Severity)
	}
}

// A repository that declares no sites at all does not manage sites, so a
// site reference points at an object created elsewhere and is not a finding.
func TestUndeclaredKindIsNotReported(t *testing.T) {
	ds := baseDataset()
	ds.Sites = nil
	srv := server("srv-01", 10)
	srv.SiteSlug = "somewhere-else"
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "unknown-site", 0)
}

func TestRackCollision(t *testing.T) {
	ds := baseDataset()
	// The R740 is 2U: a device at U10 fills U10 and U11.
	ds.Devices = []*models.DeviceConfig{server("srv-01", 10), server("srv-02", 11)}

	got := assertCheck(t, Check(ds, Options{}), "rack-collision", 1)
	if len(got) == 1 && !strings.Contains(got[0].Message, "srv-01") {
		t.Errorf("collision message should name the other device, got %q", got[0].Message)
	}
}

func TestRackCollisionIgnoresOppositeFaceAndOtherRacks(t *testing.T) {
	ds := baseDataset()
	rear := server("srv-02", 10)
	rear.Face = "rear"
	other := server("srv-03", 10)
	other.RackSlug = "rack-a02"
	ds.Devices = []*models.DeviceConfig{server("srv-01", 10), rear, other}

	assertCheck(t, Check(ds, Options{}), "rack-collision", 0)
}

func TestRackCollisionSkipsChildDevices(t *testing.T) {
	ds := baseDataset()
	ds.DeviceTypes = append(ds.DeviceTypes, &models.DeviceType{
		Model: "Node", Slug: "node", Manufacturer: "Dell", UHeight: 0, SubdeviceRole: "child",
	})
	chassis := server("chassis-01", 10)
	node := server("node-01", 10)
	node.DeviceTypeSlug = "node"
	ds.Devices = []*models.DeviceConfig{chassis, node}

	assertCheck(t, Check(ds, Options{}), "rack-collision", 0)
}

func TestDuplicateIP(t *testing.T) {
	ds := baseDataset()
	a := server("srv-01", 10)
	a.Interfaces = []models.InterfaceConfig{{Name: "idrac", IP: &models.IPConfig{Address: "10.0.10.1/24"}}}
	b := server("srv-02", 20)
	b.Interfaces = []models.InterfaceConfig{{Name: "idrac", IP: &models.IPConfig{Address: "10.0.10.1/24"}}}
	ds.Devices = []*models.DeviceConfig{a, b}

	got := assertCheck(t, Check(ds, Options{}), "duplicate-ip", 1)
	if len(got) == 1 && !strings.Contains(got[0].Message, "srv-01") {
		t.Errorf("duplicate message should name the first owner, got %q", got[0].Message)
	}
}

// NetBox scopes address uniqueness per VRF, so the same address in two VRFs is
// legitimate.
func TestDuplicateIPAcrossVRFsIsAllowed(t *testing.T) {
	ds := baseDataset()
	ds.VRFs = append(ds.VRFs, &models.VRF{Name: "Lab"})
	a := server("srv-01", 10)
	a.Interfaces = []models.InterfaceConfig{{Name: "eth0", IP: &models.IPConfig{Address: "10.0.10.1/24", VRF: "Production"}}}
	b := server("srv-02", 20)
	b.Interfaces = []models.InterfaceConfig{{Name: "eth0", IP: &models.IPConfig{Address: "10.0.10.1/24", VRF: "Lab"}}}
	ds.Devices = []*models.DeviceConfig{a, b}

	assertCheck(t, Check(ds, Options{}), "duplicate-ip", 0)
}

func TestIPShapeFindings(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "idrac", IP: &models.IPConfig{Address: "10.0.10.0/24"}},
		{Name: "eth0", IP: &models.IPConfig{Address: "10.0.10.255/24"}},
		{Name: "eth1", IP: &models.IPConfig{Address: "not-an-ip"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "network-address", 1)
	assertCheck(t, findings, "broadcast-address", 1)
	assertCheck(t, findings, "invalid-ip", 1)
}

// A /31 point-to-point link (RFC 3021) uses both of its addresses.
func TestPointToPointAddressesAreNotNetworkOrBroadcast(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", IP: &models.IPConfig{Address: "10.0.0.0/31"}},
		{Name: "eth1", IP: &models.IPConfig{Address: "10.0.0.1/31"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "network-address", 0)
	assertCheck(t, findings, "broadcast-address", 0)
}

func TestIPOutsideDeclaredPrefix(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{{Name: "idrac", IP: &models.IPConfig{Address: "192.168.5.1/24"}}}
	ds.Devices = []*models.DeviceConfig{srv}

	got := assertCheck(t, Check(ds, Options{}), "ip-outside-prefix", 1)
	if len(got) == 1 && got[0].Severity != Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
}

func TestDuplicatePrimaryIP(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", IP: &models.IPConfig{Address: "10.0.20.1/24"}, AddressRole: "primary"},
		{Name: "eth1", IP: &models.IPConfig{Address: "10.0.20.2/24", AddressRole: "primary"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "duplicate-primary-ip", 1)
}

func TestUntypedInterfaceNotInTemplate(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth9"},               // not a template, no type -> error
		{Name: "bond0", Type: "lag"}, // not a template, typed -> fine
		{Name: "eth0", Type: "25gbase-x-sfp28", Members: []string{"eth1"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	findings := Check(ds, Options{})
	got := assertCheck(t, findings, "untyped-interface", 1)
	if len(got) == 1 && !strings.Contains(got[0].Object, "eth9") {
		t.Errorf("expected the finding on eth9, got %q", got[0].Object)
	}
	assertCheck(t, findings, "redundant-interface-type", 1)
	assertCheck(t, findings, "unknown-lag-member", 0)
}

func TestRedundantInterfaceFields(t *testing.T) {
	ds := baseDataset()
	enabled := true
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Type: "25gbase-x-sfp28", Enabled: &enabled},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "redundant-interface-type", 1)
	assertCheck(t, findings, "redundant-enabled", 1)
}

func TestInterfaceTypeOverrideIsReported(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0", Type: "1000base-t"}}
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "interface-type-override", 1)
}

func TestDuplicateInterfaceName(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0"}, {Name: "eth0"}}
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "duplicate-interface", 1)
}

func TestUnknownVLANIsSiteScoped(t *testing.T) {
	ds := baseDataset()
	ds.Sites = append(ds.Sites, &models.Site{Name: "Munich", Slug: "munich"})
	ds.VLANs = append(ds.VLANs, &models.VLAN{Name: "Lab", VID: 100, SiteSlug: "munich"})
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", UntaggedVLAN: "Servers"}, // declared at berlin
		{Name: "eth1", UntaggedVLAN: "Lab"},     // declared at munich only
		{Name: "bond0", Type: "lag", TaggedVLANs: []string{"Nope"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "unknown-vlan", 2)
}

func TestUnknownVRF(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", IP: &models.IPConfig{Address: "10.0.20.1/24", VRF: "Prodction"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "unknown-vrf", 1)
}

func TestModuleChecks(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Modules = []models.ModuleConfig{
		{Name: "OCP-1", ModuleTypeSlug: "ocp-25g"},  // valid
		{Name: "OCP-2", ModuleTypeSlug: "ocp-25g"},  // no such bay
		{Name: "OCP-1", ModuleTypeSlug: "ocp-100g"}, // no such module type
	}
	ds.Devices = []*models.DeviceConfig{srv}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "unknown-module-bay", 1)
	assertCheck(t, findings, "unknown-module-type", 1)
}

// Two manufacturers claiming one slug means the bare slug does not resolve;
// the qualified form does.
func TestAmbiguousModuleTypeSlug(t *testing.T) {
	ds := baseDataset()
	ds.ModuleTypes = []*models.ModuleType{
		{Model: "SFP-10G-SR", Slug: "sfp-10g-sr", Manufacturer: "Dell"},
		{Model: "SFP-10G-SR", Slug: "sfp-10g-sr", Manufacturer: "Cisco"},
	}
	bare := server("srv-01", 10)
	bare.Modules = []models.ModuleConfig{{Name: "OCP-1", ModuleTypeSlug: "sfp-10g-sr"}}
	qualified := server("srv-02", 20)
	qualified.Modules = []models.ModuleConfig{{Name: "OCP-1", ModuleTypeSlug: "dell/sfp-10g-sr"}}
	ds.Devices = []*models.DeviceConfig{bare, qualified}

	got := assertCheck(t, Check(ds, Options{}), "unknown-module-type", 1)
	if len(got) == 1 && !strings.Contains(got[0].Object, "srv-01") {
		t.Errorf("expected the ambiguous bare reference to be reported, got %q", got[0].Object)
	}
}

func TestCableConflict(t *testing.T) {
	ds := baseDataset()
	a := server("srv-01", 10)
	a.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Link: &models.LinkConfig{PeerDevice: "leaf-01", PeerPort: "Eth1/1"}},
	}
	b := server("srv-02", 20)
	b.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Link: &models.LinkConfig{PeerDevice: "leaf-01", PeerPort: "Eth1/1"}},
	}
	leaf := &models.DeviceConfig{
		Name: "leaf-01", SiteSlug: "berlin", RoleSlug: "leaf",
		DeviceTypeSlug: "leaf-sw", RackSlug: "rack-a02", Position: 40,
	}
	ds.Devices = []*models.DeviceConfig{a, b, leaf}

	got := assertCheck(t, Check(ds, Options{}), "cable-conflict", 1)
	if len(got) == 1 && !strings.Contains(got[0].Object, "Eth1/1") {
		t.Errorf("expected the conflict on the shared port, got %q", got[0].Object)
	}
}

func TestCableDeclaredFromBothEndsIsOneCable(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Link: &models.LinkConfig{PeerDevice: "leaf-01", PeerPort: "Eth1/1"}},
	}
	leaf := &models.DeviceConfig{
		Name: "leaf-01", SiteSlug: "berlin", RoleSlug: "leaf",
		DeviceTypeSlug: "leaf-sw", RackSlug: "rack-a02", Position: 40,
		Interfaces: []models.InterfaceConfig{
			{Name: "Eth1/1", Link: &models.LinkConfig{PeerDevice: "srv-01", PeerPort: "eth0"}},
		},
	}
	ds.Devices = []*models.DeviceConfig{srv, leaf}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "cable-conflict", 0)
	got := assertCheck(t, findings, "cable-declared-twice", 1)
	if len(got) == 1 && got[0].Severity != Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
}

func TestUnknownPeerPort(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Link: &models.LinkConfig{PeerDevice: "leaf-01", PeerPort: "Eth1/99"}},
	}
	leaf := &models.DeviceConfig{
		Name: "leaf-01", SiteSlug: "berlin", RoleSlug: "leaf",
		DeviceTypeSlug: "leaf-sw", RackSlug: "rack-a02", Position: 40,
	}
	ds.Devices = []*models.DeviceConfig{srv, leaf}

	assertCheck(t, Check(ds, Options{}), "unknown-peer-port", 1)
}

func TestUndeclaredPeerDeviceIsAWarning(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Link: &models.LinkConfig{PeerDevice: "core-01", PeerPort: "Eth1/1"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	got := assertCheck(t, Check(ds, Options{}), "undeclared-peer-device", 1)
	if len(got) == 1 && got[0].Severity != Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
}

func TestPatchPanelPortsAreValidCableEnds(t *testing.T) {
	ds := baseDataset()
	ds.DeviceTypes = append(ds.DeviceTypes, &models.DeviceType{
		Model: "Panel", Slug: "panel", Manufacturer: "Generic", UHeight: 1,
		FrontPorts: []models.PortTemplate{{Name: "1", Type: "lc", RearPort: "1"}},
		RearPorts:  []models.PortTemplate{{Name: "1", Type: "lc"}},
	})
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Link: &models.LinkConfig{PeerDevice: "pp-01", PeerPort: "1"}},
	}
	panel := &models.DeviceConfig{
		Name: "pp-01", SiteSlug: "berlin", RoleSlug: "server",
		DeviceTypeSlug: "panel", RackSlug: "rack-a02", Position: 42,
	}
	ds.Devices = []*models.DeviceConfig{srv, panel}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "unknown-peer-port", 0)
	assertCheck(t, findings, "undeclared-peer-device", 0)
}

func TestDeviceBayChecks(t *testing.T) {
	ds := baseDataset()
	ds.DeviceTypes = append(ds.DeviceTypes,
		&models.DeviceType{
			Model: "Chassis", Slug: "chassis", Manufacturer: "Dell", UHeight: 4,
			SubdeviceRole: "parent",
			DeviceBays:    []models.DeviceBayTemplate{{Name: "Node 1"}},
		},
		&models.DeviceType{Model: "Node", Slug: "node", Manufacturer: "Dell", UHeight: 0, SubdeviceRole: "child"},
	)
	chassis := server("chassis-01", 20)
	chassis.DeviceTypeSlug = "chassis"

	good := server("node-01", 0)
	good.DeviceTypeSlug = "node"
	good.RackSlug = ""
	good.ParentDevice = "chassis-01"
	good.DeviceBay = "Node 1"

	badBay := server("node-02", 0)
	badBay.DeviceTypeSlug = "node"
	badBay.RackSlug = ""
	badBay.ParentDevice = "chassis-01"
	badBay.DeviceBay = "Node 9"

	noBay := server("node-03", 0)
	noBay.DeviceTypeSlug = "node"
	noBay.RackSlug = ""
	noBay.ParentDevice = "chassis-01"

	missingParent := server("node-04", 0)
	missingParent.DeviceTypeSlug = "node"
	missingParent.RackSlug = ""
	missingParent.ParentDevice = "chassis-99"
	missingParent.DeviceBay = "Node 1"

	ds.Devices = []*models.DeviceConfig{chassis, good, badBay, noBay, missingParent}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "unknown-device-bay", 1)
	assertCheck(t, findings, "missing-device-bay", 1)
	assertCheck(t, findings, "unknown-parent-device", 1)
}

func TestUnrackedDeviceWarning(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 0)
	ds.Devices = []*models.DeviceConfig{srv}

	got := assertCheck(t, Check(ds, Options{}), "unracked-device", 1)
	if len(got) == 1 && got[0].Severity != Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
}

func TestVMChecks(t *testing.T) {
	ds := baseDataset()
	ds.Clusters = []*models.Cluster{{Name: "pve-cluster", TypeSlug: "proxmox"}}
	ds.Platforms = []*models.Platform{{Name: "Debian 12", Slug: "debian-12"}}
	ds.Tenants = []*models.Tenant{{Name: "Platform", Slug: "platform"}}
	ds.VMs = []*models.VMConfig{
		{
			Name: "web-01", Cluster: "pve-cluster", Platform: "debian-12", Tenant: "platform",
			RoleSlug: "server", SiteSlug: "berlin",
			Interfaces: []models.VMInterfaceConfig{
				{Name: "eth0", IP: &models.IPConfig{Address: "10.0.30.1/24", VRF: "Production"}},
			},
		},
		{
			Name: "web-01", Cluster: "pve-clstr", Platform: "debian-11", Tenant: "platfrm",
			RoleSlug: "srvr", SiteSlug: "berln",
			Interfaces: []models.VMInterfaceConfig{
				{Name: "eth0", IP: &models.IPConfig{Address: "10.0.30.1/24", VRF: "Production"}},
				{Name: "eth0"},
			},
		},
	}

	findings := Check(ds, Options{})
	assertCheck(t, findings, "duplicate-vm", 1)
	assertCheck(t, findings, "unknown-cluster", 1)
	assertCheck(t, findings, "unknown-platform", 1)
	assertCheck(t, findings, "unknown-tenant", 1)
	assertCheck(t, findings, "unknown-role", 1)
	assertCheck(t, findings, "unknown-site", 1)
	assertCheck(t, findings, "duplicate-interface", 1)
	assertCheck(t, findings, "duplicate-ip", 1)
}

func TestFindingsAreOrderedErrorsFirst(t *testing.T) {
	ds := baseDataset()
	enabled := true
	a := server("srv-01", 10)
	a.Interfaces = []models.InterfaceConfig{{Name: "eth0", Enabled: &enabled}}
	b := server("srv-01", 20)
	ds.Devices = []*models.DeviceConfig{a, b}

	findings := Check(ds, Options{})
	if len(findings) < 2 {
		t.Fatalf("expected at least two findings, got:\n%s", render(findings))
	}
	if findings[0].Severity != Error {
		t.Errorf("expected errors to sort first, got:\n%s", render(findings))
	}
}

func TestHasErrors(t *testing.T) {
	warnOnly := []Finding{{Severity: Warning, Check: "redundant-enabled"}}
	if HasErrors(warnOnly, false) {
		t.Error("warnings alone must not fail a non-strict run")
	}
	if !HasErrors(warnOnly, true) {
		t.Error("warnings must fail a strict run")
	}
	if HasErrors(nil, true) {
		t.Error("no findings must never fail")
	}
	if !HasErrors([]Finding{{Severity: Error}}, false) {
		t.Error("an error must fail any run")
	}
}

func TestModuleTypeWithoutSlug(t *testing.T) {
	ds := baseDataset()
	ds.ModuleTypes = []*models.ModuleType{{Model: "OCP 25G", Manufacturer: "Dell"}}

	got := assertCheck(t, Check(ds, Options{}), "module-type-without-slug", 1)
	if len(got) == 1 && got[0].Severity != Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
}

func TestInterfaceNotInTemplate(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth9", Type: "25gbase-x-sfp28"}, // physical port the model lacks
		{Name: "bond0", Type: "lag"},            // not physical, expected to be extra
	}
	ds.Devices = []*models.DeviceConfig{srv}

	got := assertCheck(t, Check(ds, Options{}), "interface-not-in-template", 1)
	if len(got) == 1 && !strings.Contains(got[0].Object, "eth9") {
		t.Errorf("expected the finding on eth9, got %q", got[0].Object)
	}
}

// Interfaces a module contributes are not in the device type template, so a
// device with modules installed is exempt from that check.
func TestInterfaceNotInTemplateSkippedWithModules(t *testing.T) {
	ds := baseDataset()
	srv := server("srv-01", 10)
	srv.Modules = []models.ModuleConfig{{Name: "OCP-1", ModuleTypeSlug: "ocp-25g"}}
	srv.Interfaces = []models.InterfaceConfig{{Name: "1-25G-1", Type: "25gbase-x-sfp28"}}
	ds.Devices = []*models.DeviceConfig{srv}

	assertCheck(t, Check(ds, Options{}), "interface-not-in-template", 0)
}

// A device with no face is front-mounted as far as NetBox is concerned, so it
// still collides with a device that says so explicitly.
func TestRackCollisionTreatsAnUnsetFaceAsFront(t *testing.T) {
	ds := baseDataset()
	explicit := server("srv-01", 10)
	implicit := server("srv-02", 11)
	implicit.Face = ""
	ds.Devices = []*models.DeviceConfig{explicit, implicit}

	assertCheck(t, Check(ds, Options{}), "rack-collision", 1)
}

// NetBox answers 400 "Must specify rack face when defining rack position", so
// a positioned device without a face is never created at all.
func TestPositionWithoutFace(t *testing.T) {
	ds := baseDataset()
	faceless := server("srv-01", 10)
	faceless.Face = ""
	ds.Devices = []*models.DeviceConfig{faceless}

	got := assertCheck(t, Check(ds, Options{}), "position-without-face", 1)
	if len(got) == 1 && got[0].Severity != Error {
		t.Errorf("got severity %v, want error", got[0].Severity)
	}
}

// A child device is installed into a bay, not placed at a rack unit, so
// neither position nor face applies to it.
func TestPositionWithoutFaceSkipsChildDevices(t *testing.T) {
	ds := baseDataset()
	ds.DeviceTypes = append(ds.DeviceTypes, &models.DeviceType{
		Model: "Chassis", Slug: "chassis", Manufacturer: "Dell", UHeight: 4,
		DeviceBays: []models.DeviceBayTemplate{{Name: "Node 1"}},
	})
	chassis := server("chassis-01", 20)
	chassis.DeviceTypeSlug = "chassis"
	node := server("node-01", 0)
	node.Face = ""
	node.RackSlug = ""
	node.ParentDevice = "chassis-01"
	node.DeviceBay = "Node 1"
	ds.Devices = []*models.DeviceConfig{chassis, node}

	assertCheck(t, Check(ds, Options{}), "position-without-face", 0)
}

// Position and face are only sent for a racked device, so a device with a
// position but no rack has nothing to reject.
func TestPositionWithoutFaceNeedsARack(t *testing.T) {
	ds := baseDataset()
	unracked := server("srv-01", 10)
	unracked.Face = ""
	unracked.RackSlug = ""
	ds.Devices = []*models.DeviceConfig{unracked}

	assertCheck(t, Check(ds, Options{}), "position-without-face", 0)
}
