// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// dataDir builds a data directory from a map of relative path to content.
func dataDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func provLoader(t *testing.T, root string) *DataLoader {
	t.Helper()
	dl := NewDataLoader(root, utils.NewLogger(false))
	return dl
}

// The grouped defaults/devices form is the one real inventory files use, so
// provenance has to work with it: the node is the device's own block, while
// the fields it inherits come from the file's defaults.
func TestProvenanceGroupedForm(t *testing.T) {
	root := dataDir(t, map[string]string{
		"inventory/hardware/active/servers.yaml": `# Berlin servers
defaults:
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"
  tags: ["gitops"]

devices:
  - name: "srv-01"
    position: 10
    serial: "S1"
  - name: "srv-02"
    position: 12
`,
	})

	inv, err := provLoader(t, root).LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("LoadInventoryWithProvenance: %v", err)
	}
	if len(inv.Devices) != 2 {
		t.Fatalf("loaded %d devices, want 2", len(inv.Devices))
	}

	first := inv.Devices[0]
	if first.Object.Name != "srv-01" || first.Object.SiteSlug != "berlin-dc" {
		t.Errorf("device = %+v, want the defaults merged in", first.Object)
	}
	if first.Prov.Node == nil || first.Prov.Node.Line != 9 {
		t.Errorf("node line = %d, want the device's own block (line 9)", nodeLine(first))
	}
	if first.Prov.Defaults == nil {
		t.Error("the file's defaults block was not recorded")
	}
	if first.Prov.Index != 0 || inv.Devices[1].Prov.Index != 1 {
		t.Error("device indexes do not follow file order")
	}
	if !strings.HasSuffix(first.Prov.Ref(), "servers.yaml:9") {
		t.Errorf("Ref() = %q, want file:line", first.Prov.Ref())
	}
	if first.Prov.Parked {
		t.Error("a normal file was reported as parked")
	}
}

// The plain-list form must work too — that is what the VM files use.
func TestProvenancePlainListAndSingleMapping(t *testing.T) {
	root := dataDir(t, map[string]string{
		"inventory/virtual/prod/web-01.yaml": `- name: "web-01"
  cluster: "berlin-prod-cluster"
  vcpus: 4
`,
		"inventory/virtual/prod/db-01.yaml": `name: "db-01"
cluster: "berlin-prod-cluster"
vcpus: 8
`,
	})

	inv, err := provLoader(t, root).LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("LoadInventoryWithProvenance: %v", err)
	}
	if len(inv.VMs) != 2 {
		t.Fatalf("loaded %d VMs, want 2", len(inv.VMs))
	}

	byName := map[string]Declared[*models.VMConfig]{}
	for _, vm := range inv.VMs {
		byName[vm.Object.Name] = vm
	}
	for name, want := range map[string]int{"web-01": 4, "db-01": 8} {
		vm, ok := byName[name]
		if !ok {
			t.Fatalf("%s was not loaded", name)
		}
		if vm.Object.VCPUs != want {
			t.Errorf("%s vcpus = %d, want %d", name, vm.Object.VCPUs, want)
		}
		if vm.Prov.Node == nil {
			t.Errorf("%s carries no node", name)
		}
		if vm.Prov.File == "" {
			t.Errorf("%s carries no file", name)
		}
	}
}

// A parked file is skipped by the reconciler but must be visible to ingest, or
// a device somebody has started documenting gets generated a second time.
// Parked objects are not validated: an unfinished skeleton is the point.
func TestProvenanceIncludesParkedFilesUnvalidated(t *testing.T) {
	root := dataDir(t, map[string]string{
		"inventory/hardware/active/_discovered.yaml": `# parked skeleton
- name: "srv-99"
  serial: "S99"
`,
	})

	inv, err := provLoader(t, root).LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("a parked skeleton missing required fields failed the load: %v", err)
	}
	if len(inv.Devices) != 1 {
		t.Fatalf("loaded %d devices, want the parked one to be visible", len(inv.Devices))
	}
	if !inv.Devices[0].Prov.Parked {
		t.Error("an underscore-prefixed file was not marked as parked")
	}
	if inv.Devices[0].Object.Serial != "S99" {
		t.Errorf("parked device = %+v, want it decoded", inv.Devices[0].Object)
	}
}

// An unparked file that is genuinely broken must still fail the load.
func TestProvenanceValidatesUnparkedObjects(t *testing.T) {
	root := dataDir(t, map[string]string{
		"inventory/hardware/active/servers.yaml": `- name: "srv-01"
  serial: "S1"
`,
	})

	_, err := provLoader(t, root).LoadInventoryWithProvenance()
	if err == nil {
		t.Fatal("expected a validation error for a device with no site")
	}
	if !strings.Contains(err.Error(), "site_slug") {
		t.Errorf("error = %q, want it to name the missing field", err)
	}
}

// A stray key next to `devices` is a typo whose data would otherwise be
// silently dropped, exactly as the reconciler's loader treats it.
func TestProvenanceRejectsStrayTopLevelKey(t *testing.T) {
	root := dataDir(t, map[string]string{
		"inventory/hardware/active/servers.yaml": `defaults:
  site_slug: "berlin-dc"
name: "oops"
devices:
  - name: "srv-01"
`,
	})

	_, err := provLoader(t, root).LoadInventoryWithProvenance()
	if err == nil {
		t.Fatal("expected an error for a stray top-level key")
	}
	if !strings.Contains(err.Error(), `found "name"`) {
		t.Errorf("error = %q, want it to name the stray key", err)
	}
}

// A file of nothing but comments declares nothing; it is not an error.
func TestProvenanceAcceptsCommentOnlyFile(t *testing.T) {
	root := dataDir(t, map[string]string{
		"inventory/hardware/active/notes.yaml": "# nothing here yet\n",
	})
	inv, err := provLoader(t, root).LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("a comment-only file failed the load: %v", err)
	}
	if len(inv.Devices) != 0 {
		t.Errorf("loaded %d devices from a comment-only file", len(inv.Devices))
	}
}

// Missing inventory folders are ordinary: a repository may document only VMs.
func TestProvenanceSkipsMissingFolders(t *testing.T) {
	inv, err := provLoader(t, t.TempDir()).LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("an empty data directory failed the load: %v", err)
	}
	if len(inv.Devices)+len(inv.VMs) != 0 {
		t.Error("objects appeared out of an empty data directory")
	}
}

// The provenance loader and the reconciler's loader must agree on what a file
// declares, or ingest would edit objects the sync never sees.
func TestProvenanceAgreesWithTypedLoader(t *testing.T) {
	dl := NewDataLoader("../../example", utils.NewLogger(false))
	dl.SetIgnorePatterns(DefaultIgnorePatterns)

	inv, err := dl.LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("LoadInventoryWithProvenance on the example data: %v", err)
	}

	active, err := dl.LoadDevices("inventory/hardware/active")
	if err != nil {
		t.Fatal(err)
	}
	passive, err := dl.LoadDevices("inventory/hardware/passive")
	if err != nil {
		t.Fatal(err)
	}
	vms, err := dl.LoadVMs("inventory/virtual")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(inv.Devices), len(active)+len(passive); got != want {
		t.Errorf("provenance loader found %d devices, typed loader %d", got, want)
	}
	if got, want := len(inv.VMs), len(vms); got != want {
		t.Errorf("provenance loader found %d VMs, typed loader %d", got, want)
	}

	declared := map[string]bool{}
	for _, d := range inv.Devices {
		declared[d.Object.Name] = true
	}
	for _, d := range append(append([]*models.DeviceConfig{}, active...), passive...) {
		if !declared[d.Name] {
			t.Errorf("device %s is loaded by the reconciler but invisible to ingest", d.Name)
		}
	}
}

func nodeLine[T any](d Declared[T]) int {
	if d.Prov.Node == nil {
		return 0
	}
	return d.Prov.Node.Line
}
