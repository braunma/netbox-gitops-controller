// SPDX-License-Identifier: Apache-2.0

// Package idracjson parses the JSON document produced by the sibling project
// idrac-netbox-importer (`idrac-inventory -config config.yaml -output json`)
// into a discovery.Snapshot.
//
// That document is the contract between the two projects. This package is
// deliberately tolerant of it: an unknown schema version is a warning, an
// unknown field is ignored, and a server the importer failed to reach is
// skipped and reported rather than failing the run — a BMC that was down
// during a scan must not stop the other fifty from being documented.
//
// It is also read-only in the strongest sense: it reads a file. The importer
// has its own direct-to-NetBox mode, which this project does not use and does
// not invoke; facts reach NetBox here only through YAML and a merge request.
package idracjson

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
)

// SourceName is the Snapshot source this adapter reports when the caller does
// not name one. It also becomes the directory generated files land in.
const SourceName = "idrac"

// SupportedSchemaVersion is the document version this parser was written
// against. The importer's JSON did not carry a version at first, so an absent
// one is normal and means "the original shape".
const SupportedSchemaVersion = 1

// Options tunes one parse.
type Options struct {
	// Source overrides the snapshot's source name, so several scans (one per
	// datacentre, say) can land in separate directories.
	Source string
	// Warnf receives non-fatal findings: skipped servers, an unknown schema
	// version. Nil discards them.
	Warnf func(format string, args ...interface{})
}

// document is the top level of the importer's `-output json`.
type document struct {
	// SchemaVersion is absent in older importer builds.
	SchemaVersion *int    `json:"schema_version"`
	Servers       []entry `json:"servers"`
	Stats         stats   `json:"stats"`
}

// stats is the importer's own summary of the scan. It is used to cross-check
// what was parsed, not as data.
type stats struct {
	TotalServers    int `json:"total_servers"`
	SuccessfulCount int `json:"successful_count"`
	FailedCount     int `json:"failed_count"`
}

// entry is one element of `servers`. Field names mirror the importer's
// models.ServerInfo exactly; anything it adds later is ignored here.
type entry struct {
	Host        string    `json:"host"`
	Name        string    `json:"name"`
	HostName    string    `json:"hostname"`
	CollectedAt time.Time `json:"collected_at"`
	// Error is the importer's message for a server it could not collect. Its
	// presence is what marks the entry as failed.
	Error string `json:"error"`

	Model        string `json:"model"`
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serial_number"`
	ServiceTag   string `json:"service_tag"`
	BiosVersion  string `json:"bios_version"`
	PowerState   string `json:"power_state"`

	CPUs     []cpu  `json:"cpus"`
	CPUCount int    `json:"cpu_count"`
	CPUModel string `json:"cpu_model"`

	Memory           []memory `json:"memory"`
	TotalMemoryGiB   float64  `json:"total_memory_gib"`
	MemorySlotsTotal int      `json:"memory_slots_total"`
	MemorySlotsUsed  int      `json:"memory_slots_used"`
	MemorySlotsFree  int      `json:"memory_slots_free"`

	Drives         []drive `json:"drives"`
	DriveCount     int     `json:"drive_count"`
	TotalStorageTB float64 `json:"total_storage_tb"`

	PowerConsumedWatts int `json:"power_consumed_watts"`
	PowerPeakWatts     int `json:"power_peak_watts"`
}

type cpu struct {
	Cores int `json:"cores"`
}

type memory struct {
	Type     string `json:"type"`
	SpeedMHz int    `json:"speed_mhz"`
	State    string `json:"state"`
}

type drive struct {
	CapacityGB float64 `json:"capacity_gb"`
}

// memoryStateEnabled is the Redfish state of a populated slot; the importer
// reads memory type and speed from the first such module.
const memoryStateEnabled = "Enabled"

// Parse reads an importer document and returns the snapshot it describes.
func Parse(r io.Reader, opts Options) (*discovery.Snapshot, error) {
	warnf := opts.Warnf
	if warnf == nil {
		warnf = func(string, ...interface{}) {}
	}

	var doc document
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("this is not an idrac-netbox-importer JSON document: %w", err)
	}

	if doc.SchemaVersion != nil && *doc.SchemaVersion != SupportedSchemaVersion {
		// Refusing would make an importer upgrade break every ingest pipeline
		// at once; warning lets the fields this parser knows keep working and
		// makes the mismatch visible in the job log.
		warnf("idrac JSON declares schema_version %d, this parser was written against %d — fields it does not know are ignored",
			*doc.SchemaVersion, SupportedSchemaVersion)
	}

	source := opts.Source
	if source == "" {
		source = SourceName
	}

	snap := &discovery.Snapshot{Source: source}

	var skipped int
	for _, srv := range doc.Servers {
		if srv.Error != "" {
			skipped++
			warnf("skipping %s: the importer could not collect it (%s)", srv.Host, srv.Error)
			continue
		}
		if srv.SerialNumber == "" && srv.ServiceTag == "" && srv.Host == "" {
			// Nothing to match on and nothing to name a skeleton after.
			skipped++
			warnf("skipping a server entry with no host, serial or service tag")
			continue
		}

		snap.Devices = append(snap.Devices, toDevice(srv))
		// The snapshot's own timestamp is the newest observation it carries.
		if srv.CollectedAt.After(snap.CollectedAt) {
			snap.CollectedAt = srv.CollectedAt
		}
	}

	if snap.CollectedAt.IsZero() {
		snap.CollectedAt = time.Now().UTC()
	}

	// The importer counts what it attempted; this counts what survived. A gap
	// beyond the entries skipped above means the document is inconsistent with
	// its own summary, which is worth saying out loud.
	if doc.Stats.TotalServers > 0 {
		if parsed := len(snap.Devices) + skipped; parsed != doc.Stats.TotalServers {
			warnf("the document lists %d server entries but its stats block claims %d",
				parsed, doc.Stats.TotalServers)
		}
	}
	if skipped > 0 {
		warnf("%d of %d server(s) in the document were skipped", skipped, len(doc.Servers))
	}

	// Deterministic order, independent of how the importer's worker pool
	// happened to finish, so re-ingesting one scan twice yields one diff.
	sort.Slice(snap.Devices, func(i, j int) bool {
		return deviceKey(snap.Devices[i]) < deviceKey(snap.Devices[j])
	})

	return snap, nil
}

// ParseBytes is Parse over an in-memory document.
func ParseBytes(data []byte, opts Options) (*discovery.Snapshot, error) {
	return Parse(strings.NewReader(string(data)), opts)
}

// deviceKey orders devices by the most stable identifier each one carries.
func deviceKey(d discovery.Device) string {
	switch {
	case d.Serial != "":
		return "1" + d.Serial
	case d.ServiceTag != "":
		return "2" + d.ServiceTag
	default:
		return "3" + d.Name
	}
}

// toDevice maps one importer entry onto the normalized model, reproducing the
// derivations the importer itself makes when it writes NetBox custom fields:
// core count comes from the first socket, memory type and speed from the first
// populated DIMM, and the storage summary groups drives by capacity.
func toDevice(srv entry) discovery.Device {
	dev := discovery.Device{
		Name:         displayName(srv),
		Serial:       srv.SerialNumber,
		ServiceTag:   srv.ServiceTag,
		Manufacturer: srv.Manufacturer,
		Model:        srv.Model,
		MgmtIP:       srv.Host,
		HW: discovery.HardwareFacts{
			CPUCount:           srv.CPUCount,
			CPUModel:           srv.CPUModel,
			RAMTotalGB:         int(srv.TotalMemoryGiB),
			RAMSlotsTotal:      srv.MemorySlotsTotal,
			RAMSlotsUsed:       srv.MemorySlotsUsed,
			RAMSlotsAvailable:  srv.MemorySlotsFree,
			DiskCount:          srv.DriveCount,
			StorageTotalTB:     srv.TotalStorageTB,
			BIOSVersion:        srv.BiosVersion,
			PowerState:         srv.PowerState,
			PowerConsumedWatts: srv.PowerConsumedWatts,
			PowerPeakWatts:     srv.PowerPeakWatts,
			LastInventory:      srv.CollectedAt,
		},
	}

	if len(srv.CPUs) > 0 {
		dev.HW.CPUCores = srv.CPUs[0].Cores
	}
	for _, mem := range srv.Memory {
		if mem.State == memoryStateEnabled {
			dev.HW.MemoryType = mem.Type
			dev.HW.MemorySpeedMHz = mem.SpeedMHz
			break
		}
	}
	if len(srv.Drives) > 0 {
		dev.HW.StorageSummary = storageSummary(srv.Drives)
	}

	return dev
}

// displayName picks the best name the scan knows, mirroring the importer's own
// preference: an explicitly configured name, then the machine's hostname, then
// the address it was reached on. It is a starting point for a human to correct,
// never an authority — the repository owns device names.
func displayName(srv entry) string {
	switch {
	case srv.Name != "":
		return srv.Name
	case srv.HostName != "":
		return srv.HostName
	default:
		return srv.Host
	}
}

// storageSummary groups drives by whole-GB capacity, e.g. "2x745GB, 16x14306GB".
// The format is the importer's, so a device documented by either path reads the
// same in NetBox.
func storageSummary(drives []drive) string {
	groups := make(map[int]int, len(drives))
	for _, d := range drives {
		groups[int(d.CapacityGB)]++
	}

	capacities := make([]int, 0, len(groups))
	for capacity := range groups {
		capacities = append(capacities, capacity)
	}
	sort.Ints(capacities)

	parts := make([]string, 0, len(capacities))
	for _, capacity := range capacities {
		parts = append(parts, fmt.Sprintf("%dx%dGB", groups[capacity], capacity))
	}
	return strings.Join(parts, ", ")
}
