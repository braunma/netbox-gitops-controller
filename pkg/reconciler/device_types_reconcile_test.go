// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func TestReconcileModuleTypesAutoCreatesManufacturer(t *testing.T) {
	f, c := newFakeNetBox(t)
	dtr := NewDeviceTypeReconciler(c)

	moduleTypes := []*models.ModuleType{{
		Model:        "H200",
		Slug:         "h200",
		Manufacturer: "NVIDIA",
		Description:  "GPU module",
	}}
	if err := dtr.ReconcileModuleTypes(moduleTypes); err != nil {
		t.Fatalf("ReconcileModuleTypes() error = %v", err)
	}

	mfgs := f.objects("dcim", "manufacturers")
	if len(mfgs) != 1 {
		t.Fatalf("expected manufacturer to be auto-created, got %d", len(mfgs))
	}
	if mfgs[0]["slug"] != "nvidia" {
		t.Errorf("manufacturer slug = %v, expected slugified \"nvidia\"", mfgs[0]["slug"])
	}

	mts := f.objects("dcim", "module-types")
	if len(mts) != 1 {
		t.Fatalf("expected 1 module type, got %d", len(mts))
	}
	if got := utils.GetIDFromObject(mts[0]["manufacturer"]); got != utils.GetIDFromObject(mfgs[0]) {
		t.Errorf("module type manufacturer = %d, expected auto-created manufacturer ID %d",
			got, utils.GetIDFromObject(mfgs[0]))
	}

	// Second run resolves the existing manufacturer and changes nothing.
	f.resetMutations()
	if err := dtr.ReconcileModuleTypes(moduleTypes); err != nil {
		t.Fatalf("ReconcileModuleTypes() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileDeviceTypesWithTemplates(t *testing.T) {
	f, c := newFakeNetBox(t)
	dtr := NewDeviceTypeReconciler(c)

	deviceTypes := []*models.DeviceType{{
		Model:        "PP-24",
		Slug:         "pp-24",
		Manufacturer: "Generic",
		UHeight:      1,
		RearPorts:    []models.PortTemplate{{Name: "rp1", Type: "8p8c"}},
		FrontPorts:   []models.PortTemplate{{Name: "fp1", Type: "8p8c", RearPort: "rp1"}},
		Interfaces:   []models.InterfaceTemplate{{Name: "mgmt0", Type: "1000base-t", MgmtOnly: true}},
		ModuleBays:   []models.ModuleBayTemplate{{Name: "slot-1", Position: "1"}},
		DeviceBays:   []models.DeviceBayTemplate{{Name: "bay-1", Label: "Bay 1"}},
	}}
	if err := dtr.ReconcileDeviceTypes(deviceTypes); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	// Rear port templates must be created before front port templates,
	// because front ports reference rear ports by ID.
	rearIdx, frontIdx := -1, -1
	for i, m := range f.mutationLog() {
		if m.method != "POST" {
			continue
		}
		if strings.Contains(m.path, "rear-port-templates") && rearIdx == -1 {
			rearIdx = i
		}
		if strings.Contains(m.path, "front-port-templates") && frontIdx == -1 {
			frontIdx = i
		}
	}
	if rearIdx == -1 || frontIdx == -1 || rearIdx > frontIdx {
		t.Errorf("rear port template POST (index %d) must precede front port template POST (index %d)", rearIdx, frontIdx)
	}

	rearTemplates := f.objects("dcim", "rear-port-templates")
	if len(rearTemplates) != 1 {
		t.Fatalf("expected 1 rear port template, got %d", len(rearTemplates))
	}
	frontTemplates := f.objects("dcim", "front-port-templates")
	if len(frontTemplates) != 1 {
		t.Fatalf("expected 1 front port template, got %d", len(frontTemplates))
	}
	if got := utils.GetIDFromObject(frontTemplates[0]["rear_port"]); got != utils.GetIDFromObject(rearTemplates[0]) {
		t.Errorf("front port template rear_port = %d, expected resolved rear port template ID %d",
			got, utils.GetIDFromObject(rearTemplates[0]))
	}

	ifaceTemplates := f.objects("dcim", "interface-templates")
	if len(ifaceTemplates) != 1 || ifaceTemplates[0]["mgmt_only"] != true {
		t.Errorf("interface templates = %v, expected one with mgmt_only=true", ifaceTemplates)
	}
	if got := len(f.objects("dcim", "module-bay-templates")); got != 1 {
		t.Errorf("expected 1 module bay template, got %d", got)
	}
	if got := len(f.objects("dcim", "device-bay-templates")); got != 1 {
		t.Errorf("expected 1 device bay template, got %d", got)
	}

	// The whole tree must be idempotent on a second run.
	f.resetMutations()
	if err := dtr.ReconcileDeviceTypes(deviceTypes); err != nil {
		t.Fatalf("ReconcileDeviceTypes() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

// TestReconcileDeviceTypesPowerAndConsoleTemplates covers the component
// templates a community-library device type commonly carries beyond
// interfaces and patch panel ports.
func TestReconcileDeviceTypesPowerAndConsoleTemplates(t *testing.T) {
	f, c := newFakeNetBox(t)
	dtr := NewDeviceTypeReconciler(c)

	deviceTypes := []*models.DeviceType{{
		Model:        "Widget 4000",
		Slug:         "acme-widget-4000",
		Manufacturer: "Acme",
		UHeight:      2,
		PartNumber:   "W4000",
		Airflow:      "front-to-rear",
		Weight:       21.9,
		WeightUnit:   "kg",
		ConsolePorts: []models.ConsolePortTemplate{{Name: "console", Type: "rj-45"}},
		ConsoleServerPorts: []models.ConsolePortTemplate{
			{Name: "console-server-1", Type: "rj-45"},
		},
		PowerPorts: []models.PowerPortTemplate{
			{Name: "PSU1", Type: "iec-60320-c14", MaximumDraw: 750, AllocatedDraw: 500},
		},
		PowerOutlets: []models.PowerOutletTemplate{
			{Name: "outlet-1", Type: "iec-60320-c13", PowerPort: "PSU1", FeedLeg: "A"},
		},
	}}
	if err := dtr.ReconcileDeviceTypes(deviceTypes); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	dts := f.objects("dcim", "device-types")
	if len(dts) != 1 {
		t.Fatalf("expected 1 device type, got %d", len(dts))
	}
	if dts[0]["part_number"] != "W4000" || dts[0]["airflow"] != "front-to-rear" {
		t.Errorf("device type = %+v, expected part_number and airflow to be sent", dts[0])
	}
	if dts[0]["weight_unit"] != "kg" {
		t.Errorf("device type = %+v, expected weight to be sent with its unit", dts[0])
	}

	for _, endpoint := range []string{
		"console-port-templates", "console-server-port-templates",
		"power-port-templates", "power-outlet-templates",
	} {
		if got := len(f.objects("dcim", endpoint)); got != 1 {
			t.Errorf("%s = %d object(s), want 1", endpoint, got)
		}
	}

	pp := f.objects("dcim", "power-port-templates")[0]
	if pp["maximum_draw"] != float64(750) || pp["allocated_draw"] != float64(500) {
		t.Errorf("power port = %+v, expected draw values to be sent", pp)
	}

	// The outlet must resolve the power port that feeds it, which only works
	// because power ports are reconciled first.
	outlet := f.objects("dcim", "power-outlet-templates")[0]
	if got := utils.GetIDFromObject(outlet["power_port"]); got != utils.GetIDFromObject(pp) {
		t.Errorf("outlet power_port = %v, expected the PSU1 template ID %d", outlet["power_port"], utils.GetIDFromObject(pp))
	}
	if outlet["feed_leg"] != "A" {
		t.Errorf("outlet feed_leg = %v, want A", outlet["feed_leg"])
	}

	// Second run must be a no-op.
	f.resetMutations()
	if err := dtr.ReconcileDeviceTypes(deviceTypes); err != nil {
		t.Fatalf("ReconcileDeviceTypes() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

// TestReconcileDeviceTypesMultiPositionPorts checks that a breakout patch
// panel keeps its rear port capacity and per-front-port position instead of
// collapsing every port to position 1.
func TestReconcileDeviceTypesMultiPositionPorts(t *testing.T) {
	f, c := newFakeNetBox(t)
	dtr := NewDeviceTypeReconciler(c)

	deviceTypes := []*models.DeviceType{{
		Model:        "Breakout Panel",
		Slug:         "breakout-panel",
		Manufacturer: "Acme",
		UHeight:      1,
		RearPorts:    []models.PortTemplate{{Name: "trunk-1", Type: "mpo-12", Positions: 12}},
		FrontPorts: []models.PortTemplate{
			{Name: "breakout-1", Type: "lc", RearPort: "trunk-1", RearPortPosition: 3},
			// Position omitted: falls back to NetBox's default of 1.
			{Name: "breakout-2", Type: "lc", RearPort: "trunk-1"},
		},
	}}
	if err := dtr.ReconcileDeviceTypes(deviceTypes); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	rear := f.objects("dcim", "rear-port-templates")[0]
	if rear["positions"] != float64(12) {
		t.Errorf("rear port positions = %v, want 12", rear["positions"])
	}

	byName := map[string]map[string]interface{}{}
	for _, fp := range f.objects("dcim", "front-port-templates") {
		name, _ := fp["name"].(string)
		byName[name] = fp
	}
	if got := byName["breakout-1"]["rear_port_position"]; got != float64(3) {
		t.Errorf("breakout-1 rear_port_position = %v, want 3", got)
	}
	if got := byName["breakout-2"]["rear_port_position"]; got != float64(1) {
		t.Errorf("breakout-2 rear_port_position = %v, want the default 1", got)
	}
}
