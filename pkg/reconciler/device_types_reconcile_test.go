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
