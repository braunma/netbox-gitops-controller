// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// panelsDataset is a server and a switch in two racks with a patch panel in
// each, whose device type maps front n to rear n.
func panelsDataset() Dataset {
	pp := &models.DeviceType{Model: "PP-12", Slug: "pp-12", Manufacturer: "Generic", UHeight: 1,
		FrontPorts: []models.PortTemplate{{Name: "1", Type: "lc", RearPort: "1"}, {Name: "2", Type: "lc", RearPort: "2", RearPortPosition: 1}},
		RearPorts:  []models.PortTemplate{{Name: "1", Type: "lc", Positions: 1}, {Name: "2", Type: "lc", Positions: 1}},
	}
	panel := func(name string) *models.DeviceConfig {
		return &models.DeviceConfig{Name: name, SiteSlug: "s", RoleSlug: "patch-panel", DeviceTypeSlug: "pp-12"}
	}
	return Dataset{
		DeviceTypes: []*models.DeviceType{pp},
		Devices: []*models.DeviceConfig{
			{Name: "srv", SiteSlug: "s", RoleSlug: "server", DeviceTypeSlug: "r740",
				Interfaces: []models.InterfaceConfig{{Name: "eth1", Link: &models.LinkConfig{
					PeerDevice: "leaf", PeerPort: "Eth1/2", CableType: "smf-os2", Color: "yellow",
					Via: []models.ViaHop{{Device: "pp-a", Port: "2", Color: "purple"}, {Device: "pp-b", Port: "2"}},
				}}}},
			panel("pp-a"), panel("pp-b"),
			{Name: "leaf", SiteSlug: "s", RoleSlug: "switch", DeviceTypeSlug: "s5248"},
		},
	}
}

func linkOf(t *testing.T, ds Dataset, device string, kind, port string) *models.LinkConfig {
	t.Helper()
	for _, d := range ds.Devices {
		if d.Name != device {
			continue
		}
		switch kind {
		case "interface":
			for _, p := range d.Interfaces {
				if p.Name == port {
					return p.Link
				}
			}
		case "front":
			for _, p := range d.FrontPorts {
				if p.Name == port {
					return p.Link
				}
			}
		case "rear":
			for _, p := range d.RearPorts {
				if p.Name == port {
					return p.Link
				}
			}
		}
	}
	t.Fatalf("%s %s %s not found", device, kind, port)
	return nil
}

func TestExpandCablePathsTwoPanels(t *testing.T) {
	ds := panelsDataset()
	paths, err := ExpandCablePaths(&ds)
	if err != nil {
		t.Fatalf("ExpandCablePaths: %v", err)
	}
	want := "srv[eth1] > pp-a[front 2] > pp-a[rear 2] > pp-b[rear 2] > pp-b[front 2] > leaf[Eth1/2]"
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("paths = %v, want [%s]", paths, want)
	}

	// Cord in: the link itself, re-pointed at the first panel, strand colour.
	cord := linkOf(t, ds, "srv", "interface", "eth1")
	if cord.PeerDevice != "pp-a" || cord.PeerPort != "2" || cord.Color != "yellow" || cord.CableType != "smf-os2" || len(cord.Via) != 0 {
		t.Errorf("first cord = %+v", *cord)
	}
	// Backbone: on pp-a's rear 2, to pp-b's rear 2, the hop's own colour; the
	// link's type is for its cords and does not reach the backbone.
	backbone := linkOf(t, ds, "pp-a", "rear", "2")
	if backbone.PeerDevice != "pp-b" || backbone.PeerPort != "2" || backbone.Color != "purple" || backbone.CableType != "" {
		t.Errorf("backbone = %+v", *backbone)
	}
	// Cord out: on pp-b's front 2, to the far end, strand colour again.
	out := linkOf(t, ds, "pp-b", "front", "2")
	if out.PeerDevice != "leaf" || out.PeerPort != "Eth1/2" || out.Color != "yellow" {
		t.Errorf("last cord = %+v", *out)
	}
	for _, d := range ds.Devices {
		if d.Name == "pp-b" && (len(d.FrontPorts) != 1 || d.FrontPorts[0].RearPort != "2" || d.FrontPorts[0].RearPortPosition != 1) {
			t.Errorf("pp-b front port entry must carry the template's rear mapping: %+v", d.FrontPorts)
		}
	}
	// Idempotent: expanding again changes nothing and reports no conflict.
	if _, err := ExpandCablePaths(&ds); err != nil {
		t.Errorf("second expansion: %v", err)
	}
}

func TestExpandCablePathsOnePanel(t *testing.T) {
	ds := panelsDataset()
	ds.Devices[0].Interfaces[0].Link.Via = ds.Devices[0].Interfaces[0].Link.Via[:1]
	paths, err := ExpandCablePaths(&ds)
	if err != nil {
		t.Fatalf("ExpandCablePaths: %v", err)
	}
	if want := "srv[eth1] > pp-a[front 2] > pp-a[rear 2] > leaf[Eth1/2]"; paths[0] != want {
		t.Errorf("path = %q, want %q", paths[0], want)
	}
	if rear := linkOf(t, ds, "pp-a", "rear", "2"); rear.PeerDevice != "leaf" {
		t.Errorf("single panel: rear port must carry the cord to the far end, got %+v", *rear)
	}
}

func TestExpandCablePathsSharedBackboneAndConflicts(t *testing.T) {
	// A declared backbone to the same far end is reused, not duplicated.
	ds := panelsDataset()
	ds.Devices[1].RearPorts = []models.RearPortConfig{{Name: "2", Link: &models.LinkConfig{PeerDevice: "pp-b", PeerPort: "2", Color: "purple"}}}
	if _, err := ExpandCablePaths(&ds); err != nil {
		t.Fatalf("shared backbone: %v", err)
	}
	if len(ds.Devices[1].RearPorts) != 1 {
		t.Errorf("rear ports = %+v, want the one declared entry reused", ds.Devices[1].RearPorts)
	}

	cases := []struct {
		name   string
		mutate func(ds *Dataset)
		want   string
	}{
		{"backbone cabled elsewhere", func(ds *Dataset) {
			ds.Devices[1].RearPorts = []models.RearPortConfig{{Name: "2", Link: &models.LinkConfig{PeerDevice: "pp-b", PeerPort: "1"}}}
		}, "already cabled to pp-b[1]"},
		{"undeclared panel", func(ds *Dataset) { ds.Devices[0].Interfaces[0].Link.Via[1].Device = "pp-x" }, `patch panel "pp-x" is not declared`},
		{"panel without the role", func(ds *Dataset) { ds.Devices[2].RoleSlug = "server" }, `has role "server"`},
		{"front port the type does not have", func(ds *Dataset) { ds.Devices[0].Interfaces[0].Link.Via[0].Port = "9" }, `has no front port "9"`},
		{"far end is a panel", func(ds *Dataset) { ds.Devices[0].Interfaces[0].Link.PeerDevice = "pp-b" }, "is a patch panel"},
		{"three panels", func(ds *Dataset) {
			ds.Devices[0].Interfaces[0].Link.Via = append(ds.Devices[0].Interfaces[0].Link.Via, models.ViaHop{Device: "pp-a", Port: "1"})
		}, "at most two patch panels"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := panelsDataset()
			tc.mutate(&ds)
			_, err := ExpandCablePaths(&ds)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "srv[eth1] >") {
				t.Errorf("error should quote the path as written: %v", err)
			}
		})
	}
}

// A panel's cable_defaults fill the segments of a path at that panel, with the
// hop and the link taking precedence; a cable declared on its own is left as
// written, because it may be a different cable altogether.
func TestExpandCablePathsPanelDefaults(t *testing.T) {
	ds := panelsDataset()
	defaults := &models.CableDefaults{
		Front: models.CableAttrs{CableType: "smf-os2", Color: "yellow"},
		Rear:  models.CableAttrs{CableType: "smf-os2", Color: "purple"},
	}
	ds.Devices[1].CableDefaults, ds.Devices[2].CableDefaults = defaults, defaults
	// The path says where it goes and that its cords are blue; the backbone
	// is the panels' business.
	ds.Devices[0].Interfaces[0].Link = &models.LinkConfig{PeerDevice: "leaf", PeerPort: "Eth1/2", Color: "blue",
		Via: []models.ViaHop{{Device: "pp-a", Port: "2"}, {Device: "pp-b", Port: "2"}}}
	// A cable declared on its own on pp-a's rear 1, with no type or colour.
	ds.Devices[1].RearPorts = []models.RearPortConfig{{Name: "1", Link: &models.LinkConfig{PeerDevice: "pp-b", PeerPort: "1"}}}

	if _, err := ExpandCablePaths(&ds); err != nil {
		t.Fatalf("ExpandCablePaths: %v", err)
	}
	if l := linkOf(t, ds, "srv", "interface", "eth1"); l.CableType != "smf-os2" || l.Color != "blue" {
		t.Errorf("first cord: type from pp-a's front defaults, colour from the link; got %+v", *l)
	}
	if l := linkOf(t, ds, "pp-a", "rear", "2"); l.CableType != "smf-os2" || l.Color != "purple" {
		t.Errorf("backbone should take pp-a's rear defaults, untouched by the link's colour; got %+v", *l)
	}
	if l := linkOf(t, ds, "pp-b", "front", "2"); l.CableType != "smf-os2" || l.Color != "blue" {
		t.Errorf("last cord: type from pp-b's front defaults, colour from the link; got %+v", *l)
	}
	if l := linkOf(t, ds, "pp-a", "rear", "1"); l.CableType != "" || l.Color != "" {
		t.Errorf("a cable declared on its own must not inherit panel defaults, got %+v", *l)
	}
}
