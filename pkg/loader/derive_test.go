// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func deriveDataset() Dataset {
	return Dataset{
		Sites:      []*models.Site{{Name: "Berlin", Slug: "berlin"}},
		Racks:      []*models.Rack{{Name: "A01", Slug: "rack-a01", SiteSlug: "berlin", UHeight: 42}},
		VRFs:       []*models.VRF{{Name: "Management"}},
		VLANGroups: []*models.VLANGroup{{Name: "Berlin", Slug: "berlin-vlans", SiteSlug: "berlin"}},
		VLANs:      []*models.VLAN{{Name: "mgmt", VID: 10, SiteSlug: "berlin"}},
		Prefixes:   []*models.Prefix{{Prefix: "10.0.10.0/24", VRFName: "Management", VLANName: "mgmt"}, {Prefix: "10.0.0.0/8"}},
	}
}

func TestDeriveDefaults(t *testing.T) {
	ds := deriveDataset()
	chassis := &models.DeviceConfig{Name: "chassis", RoleSlug: "server", DeviceTypeSlug: "c", RackSlug: "rack-a01", Position: 10}
	node := &models.DeviceConfig{Name: "node", RoleSlug: "server", DeviceTypeSlug: "n", ParentDevice: "chassis", DeviceBay: "1",
		Interfaces: []models.InterfaceConfig{
			{Name: "idrac", IP: &models.IPConfig{Address: "10.0.10.5/24"}},
			{Name: "eth1", Mode: "tagged", IP: &models.IPConfig{Address: "10.0.10.6/24", VRF: "Other"}},
			{Name: "eth2", IP: &models.IPConfig{Address: "10.9.9.9/24"}},
		}}
	// Declared child before parent: the parent's site must still reach it.
	ds.Devices = []*models.DeviceConfig{node, chassis}

	lines, err := DeriveDefaults(&ds)
	if err != nil {
		t.Fatalf("DeriveDefaults: %v", err)
	}
	if chassis.SiteSlug != "berlin" || node.SiteSlug != "berlin" {
		t.Errorf("sites = %q (chassis), %q (node); want berlin from the rack and the parent", chassis.SiteSlug, node.SiteSlug)
	}
	idrac := node.Interfaces[0]
	if idrac.IP.VRF != "Management" || idrac.Mode != "" || idrac.UntaggedVLAN != "" {
		t.Errorf("idrac = %+v ip %+v; want the VRF from prefix 10.0.10.0/24 and the layer 2 untouched", idrac, idrac.IP)
	}
	if eth1 := node.Interfaces[1]; eth1.IP.VRF != "Other" {
		t.Errorf("eth1 = %+v: a written VRF stays", eth1)
	}
	if eth2 := node.Interfaces[2]; eth2.IP.VRF != "" {
		t.Errorf("eth2 = %+v: the enclosing /8 has no VRF, so nothing is derived", eth2)
	}
	if ds.Prefixes[0].SiteSlug != "berlin" {
		t.Errorf("prefix site = %q, want berlin from its VLAN", ds.Prefixes[0].SiteSlug)
	}
	if ds.VLANs[0].GroupSlug != "berlin-vlans" {
		t.Errorf("VLAN group = %q, want the one group at berlin", ds.VLANs[0].GroupSlug)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		`Derived: device chassis: site "berlin" (from rack rack-a01)`,
		`Derived: device node: site "berlin" (from parent device chassis)`,
		`Derived: device node interface idrac: vrf "Management" (from prefix 10.0.10.0/24)`,
		`Derived: prefix 10.0.10.0/24: site "berlin" (from VLAN mgmt)`,
		`Derived: VLAN mgmt (10): group "berlin-vlans" (the one VLAN group at site berlin)`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing line %q in:\n%s", want, joined)
		}
	}
}

func TestDeriveDefaultsSiteErrors(t *testing.T) {
	ds := deriveDataset()
	ds.Devices = []*models.DeviceConfig{
		{Name: "a", RoleSlug: "r", DeviceTypeSlug: "t", RackSlug: "rack-x"},
		{Name: "b", RoleSlug: "r", DeviceTypeSlug: "t", ParentDevice: "ghost", DeviceBay: "1"},
	}
	_, err := DeriveDefaults(&ds)
	if err == nil {
		t.Fatal("want errors for sites nothing can supply")
	}
	for _, want := range []string{`device "a": site is required; rack "rack-x" is not declared`, `device "b": site is required; parent device "ghost" is not declared`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// Through the loader: a device file that names only the rack loads, and the
// run reports the derived site.
func TestLoadDatasetDerivesSite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "definitions/sites", "s.yaml", "- { name: Berlin, slug: berlin }\n")
	writeFile(t, dir, "definitions/racks", "r.yaml", "- { name: A01, slug: rack-a01, site: berlin, u_height: 42 }\n")
	writeFile(t, dir, "inventory/hardware/active", "d.yaml", "- { name: srv, rack: rack-a01, position: 1, role: server, device_type: dt }\n")
	ds, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDataset(DatasetOptions{})
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if ds.Devices[0].SiteSlug != "berlin" || len(ds.Derivations) != 1 {
		t.Errorf("site = %q, derivations = %v", ds.Devices[0].SiteSlug, ds.Derivations)
	}
}
