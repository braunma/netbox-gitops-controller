package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// writeLibraryFile writes a library device type at <root>/<vendor>/<name>.
func writeLibraryFile(t *testing.T, root, vendor, name, content string) {
	t.Helper()
	dir := filepath.Join(root, vendor)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func testLoader(t *testing.T) (*DataLoader, string) {
	t.Helper()
	root := t.TempDir()
	return NewDataLoader(root, utils.NewLogger(false)), root
}

// A device type as the community library publishes it: hyphenated component
// keys, one document per file, defaults left out.
const libraryDeviceType = `---
manufacturer: Acme
model: Widget 4000
slug: acme-widget-4000
part_number: W4000
u_height: 2
airflow: front-to-rear
weight: 21.9
weight_unit: kg
description: A test device
comments: Imported from the library
console-ports:
  - name: console
    type: rj-45
console-server-ports:
  - name: console-server-1
    type: rj-45
power-ports:
  - name: PSU1
    type: iec-60320-c14
    maximum_draw: 750
    allocated_draw: 500
power-outlets:
  - name: outlet-1
    type: iec-60320-c13
    power_port: PSU1
    feed_leg: A
interfaces:
  - name: mgmt
    type: 1000base-t
    mgmt_only: true
  - name: eth0
    type: 25gbase-x-sfp28
rear-ports:
  - name: trunk-1
    type: mpo-12
    positions: 12
front-ports:
  - name: breakout-1
    type: lc
    rear_port: trunk-1
    rear_port_position: 3
module-bays:
  - name: slot-1
    position: "1"
device-bays:
  - name: bay-1
`

func TestLoadDeviceTypeLibrary(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Acme", "widget-4000.yaml", libraryDeviceType)

	got, err := dl.LoadDeviceTypeLibrary(root)
	if err != nil {
		t.Fatalf("LoadDeviceTypeLibrary() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d device types, want 1", len(got))
	}
	dt := got[0]

	if dt.Model != "Widget 4000" || dt.Slug != "acme-widget-4000" || dt.Manufacturer != "Acme" {
		t.Errorf("identity = %q/%q/%q, want Widget 4000/acme-widget-4000/Acme", dt.Model, dt.Slug, dt.Manufacturer)
	}
	if dt.PartNumber != "W4000" || dt.UHeight != 2 || dt.Airflow != "front-to-rear" {
		t.Errorf("part/height/airflow = %q/%v/%q", dt.PartNumber, dt.UHeight, dt.Airflow)
	}
	if dt.Weight != 21.9 || dt.WeightUnit != "kg" {
		t.Errorf("weight = %v %q, want 21.9 kg", dt.Weight, dt.WeightUnit)
	}
	if dt.Description != "A test device" || dt.Comments != "Imported from the library" {
		t.Errorf("description/comments = %q/%q", dt.Description, dt.Comments)
	}

	// Every hyphenated component list must survive the translation; dropping
	// one silently would import a device type missing real hardware.
	if len(dt.Interfaces) != 2 || dt.Interfaces[0].Name != "mgmt" || !dt.Interfaces[0].MgmtOnly {
		t.Errorf("interfaces = %+v", dt.Interfaces)
	}
	if len(dt.ConsolePorts) != 1 || dt.ConsolePorts[0].Type != "rj-45" {
		t.Errorf("console ports = %+v", dt.ConsolePorts)
	}
	if len(dt.ConsoleServerPorts) != 1 || dt.ConsoleServerPorts[0].Name != "console-server-1" {
		t.Errorf("console server ports = %+v", dt.ConsoleServerPorts)
	}
	if len(dt.PowerPorts) != 1 || dt.PowerPorts[0].MaximumDraw != 750 || dt.PowerPorts[0].AllocatedDraw != 500 {
		t.Errorf("power ports = %+v", dt.PowerPorts)
	}
	if len(dt.PowerOutlets) != 1 || dt.PowerOutlets[0].PowerPort != "PSU1" || dt.PowerOutlets[0].FeedLeg != "A" {
		t.Errorf("power outlets = %+v", dt.PowerOutlets)
	}
	if len(dt.RearPorts) != 1 || dt.RearPorts[0].Positions != 12 {
		t.Errorf("rear ports = %+v", dt.RearPorts)
	}
	if len(dt.FrontPorts) != 1 || dt.FrontPorts[0].RearPort != "trunk-1" || dt.FrontPorts[0].RearPortPosition != 3 {
		t.Errorf("front ports = %+v", dt.FrontPorts)
	}
	if len(dt.ModuleBays) != 1 || dt.ModuleBays[0].Position != "1" {
		t.Errorf("module bays = %+v", dt.ModuleBays)
	}
	if len(dt.DeviceBays) != 1 || dt.DeviceBays[0].Name != "bay-1" {
		t.Errorf("device bays = %+v", dt.DeviceBays)
	}
}

func TestLoadDeviceTypeLibraryAppliesDefaults(t *testing.T) {
	dl, root := testLoader(t)
	// u_height, is_full_depth and slug all omitted, as the library allows.
	writeLibraryFile(t, root, "Acme", "minimal.yaml", `---
manufacturer: Acme
model: Minimal Box
`)

	got, err := dl.LoadDeviceTypeLibrary(root)
	if err != nil {
		t.Fatalf("LoadDeviceTypeLibrary() error = %v", err)
	}
	dt := got[0]

	// Omitted means "NetBox default", not "zero": a device type defaulting to
	// 0U and half depth would misrepresent the hardware.
	if dt.UHeight != 1 {
		t.Errorf("UHeight = %v, want the NetBox default of 1", dt.UHeight)
	}
	if !dt.IsFullDepth {
		t.Errorf("IsFullDepth = false, want the NetBox default of true")
	}
	if dt.Slug != "minimal-box" {
		t.Errorf("Slug = %q, want it derived from the model", dt.Slug)
	}
}

func TestLoadDeviceTypeLibraryKeepsExplicitFalse(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Acme", "blade.yaml", `---
manufacturer: Acme
model: Blade
u_height: 0
is_full_depth: false
subdevice_role: child
`)

	got, err := dl.LoadDeviceTypeLibrary(root)
	if err != nil {
		t.Fatalf("LoadDeviceTypeLibrary() error = %v", err)
	}
	dt := got[0]

	// An explicit 0/false must not be mistaken for "omitted" and defaulted.
	if dt.UHeight != 0 {
		t.Errorf("UHeight = %v, want the explicit 0", dt.UHeight)
	}
	if dt.IsFullDepth {
		t.Errorf("IsFullDepth = true, want the explicit false")
	}
	if dt.SubdeviceRole != "child" {
		t.Errorf("SubdeviceRole = %q, want child", dt.SubdeviceRole)
	}
}

func TestLoadDeviceTypeLibraryHalfHeight(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Acme", "half.yaml", `---
manufacturer: Acme
model: Half Height
u_height: 0.5
`)

	got, err := dl.LoadDeviceTypeLibrary(root)
	if err != nil {
		t.Fatalf("LoadDeviceTypeLibrary() error = %v", err)
	}
	if got[0].UHeight != 0.5 {
		t.Errorf("UHeight = %v, want 0.5 preserved", got[0].UHeight)
	}
}

func TestLoadDeviceTypeLibraryWalksVendorDirectories(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Acme", "a.yaml", "manufacturer: Acme\nmodel: A\n")
	writeLibraryFile(t, root, "Globex", "b.yml", "manufacturer: Globex\nmodel: B\n")

	got, err := dl.LoadDeviceTypeLibrary(root)
	if err != nil {
		t.Fatalf("LoadDeviceTypeLibrary() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d device types across vendor directories, want 2", len(got))
	}
}

func TestLoadDeviceTypeLibraryRejectsUnknownFields(t *testing.T) {
	dl, root := testLoader(t)
	// A native-format file dropped into the library root: it is a list, and
	// its keys are underscored. Importing it as an empty device type would be
	// worse than refusing it.
	writeLibraryFile(t, root, "Acme", "native.yaml", `---
manufacturer: Acme
model: Native
console_ports:
  - name: console
`)

	_, err := dl.LoadDeviceTypeLibrary(root)
	if err == nil {
		t.Fatal("LoadDeviceTypeLibrary() = nil, want an error for an unrecognised field")
	}
	if !strings.Contains(err.Error(), "console_ports") {
		t.Errorf("error = %q, expected it to name the offending field", err)
	}
}

func TestLoadDeviceTypeLibraryRequiresIdentity(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Acme", "nameless.yaml", "manufacturer: Acme\n")

	_, err := dl.LoadDeviceTypeLibrary(root)
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("error = %v, want a missing-model error", err)
	}
}

func TestLoadDeviceTypeLibraryMissingRootIsNotAnError(t *testing.T) {
	dl, root := testLoader(t)

	got, err := dl.LoadDeviceTypeLibrary(filepath.Join(root, "absent"))
	if err != nil {
		t.Fatalf("LoadDeviceTypeLibrary() error = %v; an absent library is optional", err)
	}
	if len(got) != 0 {
		t.Errorf("loaded %d device types from an absent library, want 0", len(got))
	}

	// An unset library root is equally fine.
	if got, err := dl.LoadDeviceTypeLibrary(""); err != nil || len(got) != 0 {
		t.Errorf("LoadDeviceTypeLibrary(\"\") = %v, %v; want no types and no error", got, err)
	}
}

func TestMergeDeviceTypesLocalDefinitionWins(t *testing.T) {
	logger := utils.NewLogger(false)
	native := []*models.DeviceType{
		{Model: "Local Widget", Slug: "acme-widget-4000", Manufacturer: "Acme"},
	}
	library := []*models.DeviceType{
		{Model: "Widget 4000", Slug: "acme-widget-4000", Manufacturer: "Acme"},
		{Model: "Other", Slug: "acme-other", Manufacturer: "Acme"},
	}

	merged := MergeDeviceTypes(native, library, logger)
	if len(merged) != 2 {
		t.Fatalf("merged %d device types, want 2", len(merged))
	}
	if merged[0].Model != "Local Widget" {
		t.Errorf("merged[0] = %q, want the local definition to win on a slug clash", merged[0].Model)
	}
	if merged[1].Slug != "acme-other" {
		t.Errorf("merged[1] = %q, want the non-clashing library type kept", merged[1].Slug)
	}
}
