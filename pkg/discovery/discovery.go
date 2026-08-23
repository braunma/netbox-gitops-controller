// SPDX-License-Identifier: Apache-2.0

// Package discovery holds the normalized, source-agnostic view of facts
// observed about running infrastructure.
//
// Every source adapter — the compiled-in Proxmox collector, the iDRAC JSON
// ingest adapter, anything added later — produces a Snapshot, and everything
// downstream (matching against the declared inventory, writing YAML) consumes
// one. Adding a source therefore means writing one adapter, not touching the
// pipeline.
//
// A Snapshot is a description of what was seen, never an instruction. Nothing
// in this package or below it writes to NetBox: discovered facts reach NetBox
// only by being written into this repository's YAML, reviewed as a merge
// request, and reconciled like every hand-written line. See docs/INGEST.md.
//
// Zero means "not reported". A source that could not read a fact leaves the
// field at its zero value, and the YAML writer never writes a zero-valued
// fact — so a scan that fails to read the power draw leaves the last known
// value in the repository rather than blanking it.
package discovery

import "time"

// Snapshot is one source's observation of one point in time.
type Snapshot struct {
	// Source names the adapter and the instance it read, e.g. "pve-prod" for a
	// configured Proxmox source or "idrac-json" for an ingested document. It
	// is used in generated file paths, so keep it filesystem-safe.
	Source string `json:"source"`
	// SourceVersion is the version of the system that produced the facts (the
	// Proxmox release, the importer build). Recorded for provenance only.
	SourceVersion string `json:"source_version,omitempty"`
	// CollectedAt is when the source observed these facts, not when they were
	// ingested.
	CollectedAt time.Time `json:"collected_at"`

	Devices []Device `json:"devices,omitempty"`
	VMs     []VM     `json:"vms,omitempty"`
}

// VM is a discovered virtual machine.
type VM struct {
	Name string `json:"name"`
	// Cluster is the name of the cluster the VM runs on, as the source calls
	// it. Matching resolves it against the declared clusters.
	Cluster string `json:"cluster,omitempty"`
	// Node is the individual hypervisor host inside that cluster.
	Node string `json:"node,omitempty"`
	// Status is already mapped to a NetBox virtual-machine status value
	// ("active", "offline", ...) by the adapter, not left in the source's own
	// vocabulary.
	Status   string `json:"status,omitempty"`
	VCPUs    int    `json:"vcpus,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
	DiskGB   int    `json:"disk_gb,omitempty"`
	Platform string `json:"platform,omitempty"`
	// VMID is the hypervisor's numeric identifier (the Proxmox VMID), stored
	// in NetBox as the `vmid` custom field.
	VMID       int   `json:"vmid,omitempty"`
	Interfaces []NIC `json:"interfaces,omitempty"`
}

// Device is a discovered physical device.
type Device struct {
	// Name is the device's own idea of its name (its hostname), which is a
	// guess at best: the repository, not the machine, owns device names.
	Name         string `json:"name,omitempty"`
	Serial       string `json:"serial,omitempty"`
	ServiceTag   string `json:"service_tag,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	// MgmtIP is the address the device was scanned on (its BMC). It is the
	// strongest matching signal there is, because it is how the scan reached
	// this machine and not another one.
	MgmtIP     string        `json:"mgmt_ip,omitempty"`
	Interfaces []NIC         `json:"interfaces,omitempty"`
	HW         HardwareFacts `json:"hw,omitempty"`
}

// HardwareFacts is the inventory an out-of-band controller reports about a
// physical machine. Each field maps to one `hw_*` custom field in NetBox; the
// mapping and the whitelist that governs it live in pkg/ingest.
//
// Some of these are volatile by nature — the power draw and the last-inventory
// timestamp change on every scan — and are written to git anyway, so the diff
// is the record. How often that churns is a property of the CI schedule, not
// of this tool.
type HardwareFacts struct {
	CPUCount int    `json:"cpu_count,omitempty"`
	CPUModel string `json:"cpu_model,omitempty"`
	// CPUCores is the physical core count of one socket, not of the machine.
	CPUCores int `json:"cpu_cores,omitempty"`

	RAMTotalGB        int    `json:"ram_total_gb,omitempty"`
	RAMSlotsTotal     int    `json:"ram_slots_total,omitempty"`
	RAMSlotsUsed      int    `json:"ram_slots_used,omitempty"`
	RAMSlotsAvailable int    `json:"ram_slots_available,omitempty"`
	MemoryType        string `json:"memory_type,omitempty"`
	MemorySpeedMHz    int    `json:"memory_speed_mhz,omitempty"`

	DiskCount int `json:"disk_count,omitempty"`
	// StorageSummary groups the drives by capacity, e.g. "2x745GB, 16x14306GB".
	StorageSummary string `json:"storage_summary,omitempty"`
	// StorageTotalTB is kept numeric here and rendered to two decimals where it
	// is written, so the YAML matches what the iDRAC importer puts in NetBox.
	StorageTotalTB float64 `json:"storage_total_tb,omitempty"`

	BIOSVersion string `json:"bios_version,omitempty"`

	// PowerState is the source's own vocabulary ("On", "Off"), which is what
	// the importer stores; it is a free-text fact, not a NetBox choice.
	PowerState         string `json:"power_state,omitempty"`
	PowerConsumedWatts int    `json:"power_consumed_watts,omitempty"`
	PowerPeakWatts     int    `json:"power_peak_watts,omitempty"`

	// LastInventory is when this hardware inventory was taken. The zero time
	// means the source did not say.
	LastInventory time.Time `json:"last_inventory,omitzero"`
}

// IsZero reports whether nothing at all was observed, so a caller can tell an
// empty hardware report from one that happens to carry zeroes.
func (h HardwareFacts) IsZero() bool {
	return h == HardwareFacts{}
}

// NIC is a discovered network interface, on a device or a VM.
type NIC struct {
	Name string `json:"name"`
	MAC  string `json:"mac,omitempty"`
	// SpeedKbps is NetBox's unit for interface speed, so adapters convert into
	// it rather than each consumer converting out of something else.
	SpeedKbps int      `json:"speed_kbps,omitempty"`
	IPs       []string `json:"ips,omitempty"`
	Enabled   bool     `json:"enabled"`
}
