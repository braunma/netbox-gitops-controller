// SPDX-License-Identifier: Apache-2.0

// Package ingest turns a discovery.Snapshot into changes to this repository's
// YAML files.
//
// It is the only thing in the project that writes YAML, and it never writes
// anything else. In particular it holds no NetBox client and cannot acquire
// one: a discovered fact becomes NetBox truth by being committed, reviewed in
// a merge request and reconciled, exactly like a hand-written line. The merge
// request diff is the review gate — a fact that contradicts a declared value
// shows up as a change to that line, and merging it is what makes the fact the
// truth.
//
// Two further rules hold everywhere below: ingest never deletes an object from
// the YAML, and it never removes a key it did not write.
package ingest

import (
	"fmt"
	"strings"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
)

// DefaultCustomFieldPrefix is the prefix of the custom fields an out-of-band
// hardware scan owns. It matches the sibling idrac-netbox-importer's default,
// so a device documented by either path carries the same field names.
const DefaultCustomFieldPrefix = "hw_"

// Collector-writable device keys. Nothing outside this list is ever created,
// updated or reordered by ingest.
//
// The line is drawn at what a machine can observe about itself. A serial and a
// service tag are burned into the hardware; a name, a site, a rack, a
// position, a role, a device type, a cable are decisions people make, and no
// scan is entitled to an opinion about them.
const (
	KeyDeviceSerial       = "serial"
	KeyDeviceAssetTag     = "asset_tag"
	KeyDeviceCustomFields = "custom_fields"
)

// Collector-writable VM keys.
const (
	KeyVMVCPUs  = "vcpus"
	KeyVMMemory = "memory"
	KeyVMDisk   = "disk"
	KeyVMStatus = "status"
	KeyVMVMID   = "vmid"
)

// DeviceWritableKeys are the top-level device keys ingest may touch.
var DeviceWritableKeys = []string{KeyDeviceSerial, KeyDeviceAssetTag, KeyDeviceCustomFields}

// VMWritableKeys are the top-level VM keys ingest may touch.
var VMWritableKeys = []string{KeyVMVCPUs, KeyVMMemory, KeyVMDisk, KeyVMStatus, KeyVMVMID}

// hardwareField describes one `hw_*` custom field: the suffix appended to the
// configured prefix, and how to read its value out of a HardwareFacts.
//
// The order of this slice is the order the fields are written in a generated
// file and the order a missing field is inserted in an existing one, so it is
// part of the output's determinism.
type hardwareField struct {
	suffix string
	// value returns the fact and whether the source reported it. A fact that
	// was not reported is never written: a scan that could not read the power
	// draw must leave the last known value in place, not blank it.
	value func(discovery.HardwareFacts) (interface{}, bool)
}

var hardwareFields = []hardwareField{
	{"cpu_count", func(h discovery.HardwareFacts) (interface{}, bool) { return h.CPUCount, h.CPUCount > 0 }},
	{"cpu_model", func(h discovery.HardwareFacts) (interface{}, bool) { return h.CPUModel, h.CPUModel != "" }},
	{"cpu_cores", func(h discovery.HardwareFacts) (interface{}, bool) { return h.CPUCores, h.CPUCores > 0 }},
	{"ram_total_gb", func(h discovery.HardwareFacts) (interface{}, bool) { return h.RAMTotalGB, h.RAMTotalGB > 0 }},
	{"ram_slots_total", func(h discovery.HardwareFacts) (interface{}, bool) { return h.RAMSlotsTotal, h.RAMSlotsTotal > 0 }},
	{"ram_slots_used", func(h discovery.HardwareFacts) (interface{}, bool) { return h.RAMSlotsUsed, h.RAMSlotsUsed > 0 }},
	{"ram_slots_available", func(h discovery.HardwareFacts) (interface{}, bool) {
		// Zero free slots is a fact worth recording, so this one is reported
		// whenever the machine said anything about its slots at all.
		return h.RAMSlotsAvailable, h.RAMSlotsTotal > 0
	}},
	{"memory_type", func(h discovery.HardwareFacts) (interface{}, bool) { return h.MemoryType, h.MemoryType != "" }},
	{"memory_speed_mhz", func(h discovery.HardwareFacts) (interface{}, bool) { return h.MemorySpeedMHz, h.MemorySpeedMHz > 0 }},
	{"disk_count", func(h discovery.HardwareFacts) (interface{}, bool) {
		// A diskless node is a fact; report it whenever anything else about the
		// storage was read.
		return h.DiskCount, h.DiskCount > 0 || h.StorageSummary != ""
	}},
	{"storage_summary", func(h discovery.HardwareFacts) (interface{}, bool) { return h.StorageSummary, h.StorageSummary != "" }},
	{"storage_total_tb", func(h discovery.HardwareFacts) (interface{}, bool) {
		// Text in NetBox, to two decimals, matching what the importer writes.
		return fmt.Sprintf("%.2f", h.StorageTotalTB), h.StorageTotalTB > 0
	}},
	{"bios_version", func(h discovery.HardwareFacts) (interface{}, bool) { return h.BIOSVersion, h.BIOSVersion != "" }},
	{"power_state", func(h discovery.HardwareFacts) (interface{}, bool) { return h.PowerState, h.PowerState != "" }},
	{"power_consumed_watts", func(h discovery.HardwareFacts) (interface{}, bool) {
		return h.PowerConsumedWatts, h.PowerConsumedWatts > 0
	}},
	{"power_peak_watts", func(h discovery.HardwareFacts) (interface{}, bool) { return h.PowerPeakWatts, h.PowerPeakWatts > 0 }},
	{"last_inventory", func(h discovery.HardwareFacts) (interface{}, bool) {
		if h.LastInventory.IsZero() {
			return "", false
		}
		return h.LastInventory.UTC().Format(time.RFC3339), true
	}},
}

// HardwareFieldNames returns the full set of custom field names ingest may
// write, in output order, for a given prefix. It is what the documentation and
// the custom-field definitions are checked against.
func HardwareFieldNames(prefix string) []string {
	names := make([]string, 0, len(hardwareFields))
	for _, f := range hardwareFields {
		names = append(names, prefix+f.suffix)
	}
	return names
}

// hardwareCustomFields renders the reported facts as custom field values, in
// output order. Unreported facts are absent, not zero.
func hardwareCustomFields(hw discovery.HardwareFacts, prefix string) []keyValue {
	out := make([]keyValue, 0, len(hardwareFields))
	for _, f := range hardwareFields {
		value, reported := f.value(hw)
		if !reported {
			continue
		}
		out = append(out, keyValue{Key: prefix + f.suffix, Value: value})
	}
	return out
}

// keyValue is one key to write, in the order it should be written.
type keyValue struct {
	Key   string
	Value interface{}
}

// IsWritableCustomField reports whether a custom field name is one ingest owns.
// Every other custom field on a device belongs to somebody else and is never
// touched, whatever a snapshot says.
func IsWritableCustomField(name, prefix string) bool {
	return strings.HasPrefix(name, prefix)
}
