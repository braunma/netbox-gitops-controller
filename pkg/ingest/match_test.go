// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// declaredDevice wraps a device model with plausible provenance.
func declaredDevice(file string, line int, d *models.DeviceConfig) loader.Declared[*models.DeviceConfig] {
	return loader.Declared[*models.DeviceConfig]{Object: d, Prov: provAt(file, line)}
}

func declaredVM(file string, line int, v *models.VMConfig) loader.Declared[*models.VMConfig] {
	return loader.Declared[*models.VMConfig]{Object: v, Prov: provAt(file, line)}
}

// provAt builds provenance pointing at a file and line, without a node: the
// matcher only reads Ref().
func provAt(file string, line int) loader.Provenance {
	return loader.Provenance{File: file, Node: &yaml.Node{Line: line}}
}

func withIP(name, address string) models.InterfaceConfig {
	return models.InterfaceConfig{Name: name, IP: &models.IPConfig{Address: address}}
}

func TestMatchDeviceRuleOrder(t *testing.T) {
	// One device carries the scanned management address; another carries the
	// same serial. The address wins, because it is how the scan reached this
	// machine rather than a claim the machine makes about itself.
	onTheWire := &models.DeviceConfig{Name: "srv-01", Interfaces: []models.InterfaceConfig{withIP("idrac", "10.0.1.11/24")}}
	sameSerial := &models.DeviceConfig{Name: "srv-02", Serial: "S1"}

	inv := loader.Inventory{Devices: []loader.Declared[*models.DeviceConfig]{
		declaredDevice("servers.yaml", 3, onTheWire),
		declaredDevice("servers.yaml", 9, sameSerial),
	}}

	snap := &discovery.Snapshot{Devices: []discovery.Device{{MgmtIP: "10.0.1.11", Serial: "S1"}}}
	got, err := Match(snap, inv)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.Devices[0].Declared.Object != onTheWire {
		t.Errorf("matched %q, want the device carrying the scanned address", got.Devices[0].Declared.Object.Name)
	}
	if got.Devices[0].Rule != "mgmt-ip" {
		t.Errorf("rule = %q, want mgmt-ip", got.Devices[0].Rule)
	}
}

func TestMatchDeviceFallbackRules(t *testing.T) {
	tests := []struct {
		name      string
		declared  *models.DeviceConfig
		found     discovery.Device
		wantRule  string
		wantMatch bool
	}{
		{
			"serial, case- and space-insensitively",
			&models.DeviceConfig{Name: "srv-01", Serial: " cn7792048k0042 "},
			discovery.Device{Serial: "CN7792048K0042"},
			"serial", true,
		},
		{
			"a service tag matches the declared asset tag",
			&models.DeviceConfig{Name: "srv-01", AssetTag: "7XKQ2M3"},
			discovery.Device{ServiceTag: "7XKQ2M3"},
			"service-tag", true,
		},
		{
			"name, last resort",
			&models.DeviceConfig{Name: "srv-01"},
			discovery.Device{Name: "srv-01"},
			"name", true,
		},
		{
			"a BMC's fully qualified name matches the short one in YAML",
			&models.DeviceConfig{Name: "srv-01"},
			discovery.Device{Name: "srv-01.berlin.example.com"},
			"name", true,
		},
		{
			"nothing in common is not a match",
			&models.DeviceConfig{Name: "srv-01", Serial: "S1"},
			discovery.Device{Name: "srv-99", Serial: "S99"},
			"", false,
		},
		{
			// Absence is not a value: two devices with no serial are not the
			// same device.
			"an empty discovered serial matches nothing",
			&models.DeviceConfig{Name: "srv-01", Serial: ""},
			discovery.Device{Serial: "", ServiceTag: "", Name: ""},
			"", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := loader.Inventory{Devices: []loader.Declared[*models.DeviceConfig]{declaredDevice("servers.yaml", 1, tt.declared)}}
			got, err := Match(&discovery.Snapshot{Devices: []discovery.Device{tt.found}}, inv)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			matched := got.Devices[0].Declared != nil
			if matched != tt.wantMatch {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatch)
			}
			if matched && got.Devices[0].Rule != tt.wantRule {
				t.Errorf("rule = %q, want %q", got.Devices[0].Rule, tt.wantRule)
			}
		})
	}
}

// When several declarations carry the scanned address, the one that carries it
// on a management port wins the tie.
func TestMgmtInterfacesWinTheAddressTie(t *testing.T) {
	server := &models.DeviceConfig{Name: "srv-01", Interfaces: []models.InterfaceConfig{withIP("idrac", "10.0.1.11/24")}}
	elsewhere := &models.DeviceConfig{Name: "sw-01", Interfaces: []models.InterfaceConfig{withIP("Eth1/0", "10.0.1.11/24")}}

	inv := loader.Inventory{Devices: []loader.Declared[*models.DeviceConfig]{
		declaredDevice("servers.yaml", 3, server),
		declaredDevice("switches.yaml", 3, elsewhere),
	}}

	got, err := Match(&discovery.Snapshot{Devices: []discovery.Device{{MgmtIP: "10.0.1.11"}}}, inv)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.Devices[0].Declared.Object != server {
		t.Errorf("matched %q, want the device holding the address on a management port", got.Devices[0].Declared.Object.Name)
	}
}

// Ambiguity is an error naming the candidates, never a guess: writing a
// discovered serial into the wrong device would be a quiet corruption.
func TestAmbiguousMatchIsAnErrorListingCandidates(t *testing.T) {
	inv := loader.Inventory{Devices: []loader.Declared[*models.DeviceConfig]{
		declaredDevice("a.yaml", 3, &models.DeviceConfig{Name: "srv-01", Serial: "S1"}),
		declaredDevice("b.yaml", 7, &models.DeviceConfig{Name: "srv-02", Serial: "S1"}),
	}}

	_, err := Match(&discovery.Snapshot{Devices: []discovery.Device{{Serial: "S1"}}}, inv)
	if err == nil {
		t.Fatal("expected an error for an ambiguous serial")
	}
	for _, want := range []string{"serial", `"srv-01" (a.yaml:3)`, `"srv-02" (b.yaml:7)`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// Two machines resolving to one block means the YAML cannot tell them apart.
func TestTwoDiscoveredDevicesCannotClaimOneDeclaration(t *testing.T) {
	inv := loader.Inventory{Devices: []loader.Declared[*models.DeviceConfig]{
		declaredDevice("servers.yaml", 3, &models.DeviceConfig{Name: "srv-01"}),
	}}

	_, err := Match(&discovery.Snapshot{Devices: []discovery.Device{
		{Name: "srv-01", Serial: "S1"},
		{Name: "srv-01", Serial: "S2"},
	}}, inv)
	if err == nil {
		t.Fatal("expected an error when two scanned machines resolve to one declaration")
	}
	if !strings.Contains(err.Error(), "srv-01") {
		t.Errorf("error = %q, want it to name the declaration both claimed", err)
	}
}

func TestMatchVM(t *testing.T) {
	prod := &models.VMConfig{Name: "web-01", Cluster: "berlin-prod-cluster"}
	lab := &models.VMConfig{Name: "web-01", Cluster: "lab-cluster"}
	inv := loader.Inventory{VMs: []loader.Declared[*models.VMConfig]{
		declaredVM("prod/web-01.yaml", 1, prod),
		declaredVM("lab/web-01.yaml", 1, lab),
	}}

	got, err := Match(&discovery.Snapshot{VMs: []discovery.VM{{Name: "web-01", Cluster: "lab-cluster"}}}, inv)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.VMs[0].Declared.Object != lab {
		t.Error("a VM name is only unique within its cluster; the cluster was ignored")
	}
	if got.VMs[0].Rule != "name+cluster" {
		t.Errorf("rule = %q, want name+cluster", got.VMs[0].Rule)
	}

	// The same name in two clusters, with no cluster on the discovered side.
	_, err = Match(&discovery.Snapshot{VMs: []discovery.VM{{Name: "web-01"}}}, inv)
	if err == nil {
		t.Fatal("expected an error for a name that is not unique")
	}
	if !strings.Contains(err.Error(), "declare the cluster") {
		t.Errorf("error = %q, want it to say how to resolve the ambiguity", err)
	}
}

// A unique name matches even when the discovered cluster is not declared on
// the VM: a site-only VM has no cluster to compare.
func TestMatchVMFallsBackToUniqueName(t *testing.T) {
	edge := &models.VMConfig{Name: "edge-01", SiteSlug: "test-lab"}
	inv := loader.Inventory{VMs: []loader.Declared[*models.VMConfig]{declaredVM("edge-01.yaml", 1, edge)}}

	got, err := Match(&discovery.Snapshot{VMs: []discovery.VM{{Name: "edge-01", Cluster: "lab-cluster"}}}, inv)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.VMs[0].Declared.Object != edge || got.VMs[0].Rule != "name" {
		t.Errorf("match = %+v, want the uniquely named VM matched by name", got.VMs[0])
	}
}

func TestUnknownObjectsAreReportedNotMatched(t *testing.T) {
	got, err := Match(&discovery.Snapshot{
		Devices: []discovery.Device{{Serial: "S9", Name: "srv-99"}},
		VMs:     []discovery.VM{{Name: "brand-new"}},
	}, loader.Inventory{})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got.Devices) != 1 || got.Devices[0].Declared != nil {
		t.Error("an undeclared device should come back unmatched, not dropped")
	}
	if len(got.VMs) != 1 || got.VMs[0].Declared != nil {
		t.Error("an undeclared VM should come back unmatched, not dropped")
	}
}
