// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// The example dataset ships the custom field definitions a fresh NetBox needs
// for these facts. If the whitelist grows a field the definitions do not
// declare, the first sync after an ingest fails on an unknown custom field —
// so the two are checked against each other here rather than in production.
func TestExampleDefinitionsDeclareEveryWritableCustomField(t *testing.T) {
	path := filepath.Join("..", "..", "example", "definitions", "custom_fields", "custom_fields.yaml")
	data, err := os.ReadFile(path) // #nosec G304 -- a path inside the repository
	if err != nil {
		t.Fatal(err)
	}

	var declared []struct {
		Name        string   `yaml:"name"`
		Type        string   `yaml:"type"`
		ObjectTypes []string `yaml:"object_types"`
	}
	if err := yaml.Unmarshal(data, &declared); err != nil {
		t.Fatalf("the example custom field definitions do not parse: %v", err)
	}

	byName := map[string]string{}
	types := map[string][]string{}
	for _, field := range declared {
		byName[field.Name] = field.Type
		types[field.Name] = field.ObjectTypes
	}

	for _, name := range HardwareFieldNames(DefaultCustomFieldPrefix) {
		fieldType, ok := byName[name]
		if !ok {
			t.Errorf("the whitelist writes %s but %s does not declare it", name, path)
			continue
		}
		if fieldType != "integer" && fieldType != "text" {
			t.Errorf("%s is declared as %q; the hardware facts are integers and text", name, fieldType)
		}
		if len(types[name]) != 1 || types[name][0] != "dcim.device" {
			t.Errorf("%s applies to %v, want dcim.device", name, types[name])
		}
	}
}

// The value renderer and the declared type must agree, or NetBox rejects the
// value at apply time with a message about the field, not about the scan.
func TestHardwareFactTypesMatchTheirDeclarations(t *testing.T) {
	facts := discovery.HardwareFacts{
		CPUCount: 2, CPUModel: "Intel Xeon Gold 6248R", CPUCores: 24,
		RAMTotalGB: 384, RAMSlotsTotal: 24, RAMSlotsUsed: 6, RAMSlotsAvailable: 18,
		MemoryType: "DDR4", MemorySpeedMHz: 2933,
		DiskCount: 3, StorageSummary: "2x894GB", StorageTotalTB: 15.32,
		BIOSVersion: "2.19.0", PowerState: "Off",
		PowerConsumedWatts: 284, PowerPeakWatts: 612,
	}

	wantInteger := map[string]bool{
		"hw_cpu_count": true, "hw_cpu_cores": true, "hw_ram_total_gb": true,
		"hw_ram_slots_total": true, "hw_ram_slots_used": true, "hw_ram_slots_available": true,
		"hw_memory_speed_mhz": true, "hw_disk_count": true,
		"hw_power_consumed_watts": true, "hw_power_peak_watts": true,
	}

	for _, kv := range hardwareCustomFields(facts, DefaultCustomFieldPrefix) {
		_, isInt := kv.Value.(int)
		if wantInteger[kv.Key] != isInt {
			t.Errorf("%s renders as %T; the definition says otherwise", kv.Key, kv.Value)
		}
	}
}

// A fact the scan did not report is absent, not zero: a BMC that could not
// read the power draw must leave the last known value in the repository.
func TestUnreportedFactsAreNotWritten(t *testing.T) {
	fields := hardwareCustomFields(discovery.HardwareFacts{CPUCount: 1}, DefaultCustomFieldPrefix)
	if len(fields) != 1 || fields[0].Key != "hw_cpu_count" {
		t.Fatalf("fields = %+v, want only the one fact that was reported", fields)
	}
}

// Zero free slots and zero drives are facts, not absences — but only once the
// machine has said something about slots or storage at all.
func TestMeaningfulZeroesAreWritten(t *testing.T) {
	fields := hardwareCustomFields(discovery.HardwareFacts{
		RAMSlotsTotal: 24, RAMSlotsUsed: 24, RAMSlotsAvailable: 0,
		DiskCount: 0, StorageSummary: "",
	}, DefaultCustomFieldPrefix)

	written := map[string]interface{}{}
	for _, kv := range fields {
		written[kv.Key] = kv.Value
	}
	if got, ok := written["hw_ram_slots_available"]; !ok || got != 0 {
		t.Errorf("a fully populated machine reported %v free slots, want 0 written", got)
	}
	if _, ok := written["hw_disk_count"]; ok {
		t.Error("a machine that said nothing about storage had a disk count invented for it")
	}
}

// The prefix is configurable; everything outside it belongs to somebody else.
func TestCustomFieldPrefixIsConfigurable(t *testing.T) {
	names := HardwareFieldNames("inv_")
	for _, name := range names {
		if !strings.HasPrefix(name, "inv_") {
			t.Errorf("%s does not carry the configured prefix", name)
		}
	}
	if len(names) != len(HardwareFieldNames(DefaultCustomFieldPrefix)) {
		t.Error("changing the prefix changed how many fields there are")
	}

	if IsWritableCustomField("owner_team", DefaultCustomFieldPrefix) {
		t.Error("a field outside the prefix was reported as writable")
	}
	if !IsWritableCustomField("hw_cpu_count", DefaultCustomFieldPrefix) {
		t.Error("a field inside the prefix was reported as not writable")
	}
}

// The whitelist is enforced where keys are queued for writing, not only by the
// fact that today's only producer happens to respect it.
func TestDeviceEditRefusesAnUnprefixedCustomField(t *testing.T) {
	// Rendering the facts under one prefix and editing under another is the
	// closest stand-in for a future producer that ignores the whitelist.
	decl := declaredDevice("servers.yaml", 1, &models.DeviceConfig{Name: "srv-01"})
	m := DeviceMatch{
		Discovered: discovery.Device{Serial: "S1", HW: discovery.HardwareFacts{CPUCount: 2}},
		Declared:   &decl,
	}
	edit, changed := deviceEdit(m, "hw_")
	if !changed {
		t.Fatal("setup: the scan should have produced an edit")
	}
	for _, kv := range edit.CustomFields {
		if !IsWritableCustomField(kv.Key, "hw_") {
			t.Errorf("%s was queued for writing but is outside the whitelist", kv.Key)
		}
	}

	// Under a different prefix, none of the hw_ facts may be written.
	other, _ := deviceEdit(m, "inv_")
	for _, kv := range other.CustomFields {
		if !strings.HasPrefix(kv.Key, "inv_") {
			t.Errorf("%s was queued while the configured prefix is inv_", kv.Key)
		}
	}
}
