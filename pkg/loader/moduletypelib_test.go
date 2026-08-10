// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// A module type as the community library publishes it. The "{module}"
// placeholder in the component names is what NetBox substitutes with the bay
// position on installation, so it must survive the import untouched.
const libraryModuleType = `---
manufacturer: Dell
model: Broadcom 5720 4 Port 1GbE rNIC
part_number: 0FM487
comments: |
  Broadcom 5720 4 Port 1GbE RJ45 rNIC
interfaces:
  - name: '{module}-1GbE-0'
    label: '1'
    type: 1000base-t
    mgmt_only: false
  - name: '{module}-1GbE-1'
    label: '2'
    type: 1000base-t
    mgmt_only: false
power-ports:
  - name: 'PSU-{module}'
    type: iec-60320-c14
    maximum_draw: 750
    allocated_draw: 500
`

func TestLoadModuleTypeLibrary(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Dell", "0fm487.yaml", libraryModuleType)

	got, err := dl.LoadModuleTypeLibrary(root)
	if err != nil {
		t.Fatalf("LoadModuleTypeLibrary() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d module types, want 1", len(got))
	}
	mt := got[0]

	if mt.Manufacturer != "Dell" || mt.Model != "Broadcom 5720 4 Port 1GbE rNIC" {
		t.Errorf("identity = %q/%q", mt.Manufacturer, mt.Model)
	}
	if mt.PartNumber != "0FM487" {
		t.Errorf("PartNumber = %q, want 0FM487", mt.PartNumber)
	}
	// NetBox module types have no slug of their own; the local cache key is
	// derived from the model.
	if mt.Slug != utils.Slugify(mt.Model) {
		t.Errorf("Slug = %q, want it derived from the model", mt.Slug)
	}

	if len(mt.Interfaces) != 2 {
		t.Fatalf("interfaces = %+v, want 2", mt.Interfaces)
	}
	// The placeholder must be preserved verbatim: rewriting or stripping it
	// would make the same module in two bays collide.
	if mt.Interfaces[0].Name != "{module}-1GbE-0" {
		t.Errorf("interface name = %q, want the {module} placeholder untouched", mt.Interfaces[0].Name)
	}
	if mt.Interfaces[0].Label != "1" {
		t.Errorf("interface label = %q, want 1", mt.Interfaces[0].Label)
	}
	if len(mt.PowerPorts) != 1 || mt.PowerPorts[0].Name != "PSU-{module}" ||
		mt.PowerPorts[0].MaximumDraw != 750 || mt.PowerPorts[0].AllocatedDraw != 500 {
		t.Errorf("power ports = %+v", mt.PowerPorts)
	}
}

// TestLoadModuleTypeLibraryRejectsDuplicateIdentity: NetBox enforces
// uniqueness on manufacturer and model together, so two files claiming the
// same pair cannot both be applied. The published library contains one such
// case, so every conflict is reported at once rather than one run at a time.
func TestLoadModuleTypeLibraryRejectsDuplicateIdentity(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Panduit", "a.yaml", "manufacturer: Panduit\nmodel: Adapter Panel\npart_number: A\n")
	writeLibraryFile(t, root, "Panduit", "b.yaml", "manufacturer: Panduit\nmodel: Adapter Panel\npart_number: B\n")
	// The same model from a different vendor is legitimate and must not clash.
	writeLibraryFile(t, root, "Acme", "c.yaml", "manufacturer: Acme\nmodel: Adapter Panel\n")

	_, err := dl.LoadModuleTypeLibrary(root)
	if err == nil {
		t.Fatal("LoadModuleTypeLibrary() = nil, want the duplicate identity rejected")
	}
	if !strings.Contains(err.Error(), "Panduit/Adapter Panel") {
		t.Errorf("error = %q, want it to name the conflicting identity", err)
	}
	if !strings.Contains(err.Error(), "--ignore-file") {
		t.Errorf("error = %q, want it to point at the way out", err)
	}
}

func TestLoadModuleTypeLibraryRequiresIdentity(t *testing.T) {
	dl, root := testLoader(t)
	writeLibraryFile(t, root, "Dell", "nameless.yaml", "manufacturer: Dell\n")

	if _, err := dl.LoadModuleTypeLibrary(root); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("error = %v, want a missing-model error", err)
	}
}

func TestLoadModuleTypeLibraryMissingRootIsNotAnError(t *testing.T) {
	dl, _ := testLoader(t)
	got, err := dl.LoadModuleTypeLibrary("")
	if err != nil || len(got) != 0 {
		t.Errorf("LoadModuleTypeLibrary(\"\") = %v, %v; want no types and no error", got, err)
	}
}

func TestMergeModuleTypesLocalDefinitionWins(t *testing.T) {
	logger := utils.NewLogger(false)
	native := []*models.ModuleType{{Model: "Shared", Manufacturer: "Dell", Description: "local"}}
	library := []*models.ModuleType{
		{Model: "Shared", Manufacturer: "Dell", Description: "library"},
		{Model: "Shared", Manufacturer: "Acme", Description: "different vendor"},
	}

	merged := MergeModuleTypes(native, library, logger)
	if len(merged) != 2 {
		t.Fatalf("merged %d module types, want 2", len(merged))
	}
	if merged[0].Description != "local" {
		t.Errorf("merged[0] = %q, want the local definition to win", merged[0].Description)
	}
	// The same model from another manufacturer is a different module type.
	if merged[1].Manufacturer != "Acme" {
		t.Errorf("merged[1] = %q, want the other vendor's entry kept", merged[1].Manufacturer)
	}
}
