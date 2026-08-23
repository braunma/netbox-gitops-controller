// SPDX-License-Identifier: Apache-2.0

package idracjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
)

// parseFixture parses testdata/scan.json, a document recorded from
// `idrac-inventory -output json`, and returns it with the warnings it raised.
func parseFixture(t *testing.T, name string) (*discovery.Snapshot, []string) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test fixture

	var warnings []string
	snap, err := Parse(f, Options{Warnf: func(format string, args ...interface{}) {
		warnings = append(warnings, fmtSprintf(format, args...))
	}})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return snap, warnings
}

// The golden expectation: the recorded document maps to exactly this snapshot.
func TestParseGolden(t *testing.T) {
	snap, warnings := parseFixture(t, "scan.json")

	want := &discovery.Snapshot{
		Source:      "idrac",
		CollectedAt: time.Date(2026, 8, 23, 6, 14, 7, 0, time.UTC),
		Devices: []discovery.Device{
			{
				Name:         "srv-berlin-01.example.com",
				Serial:       "CN7792048K0042",
				ServiceTag:   "7XKQ2M3",
				Manufacturer: "Dell Inc.",
				Model:        "PowerEdge R7615",
				MgmtIP:       "10.0.1.11",
				HW: discovery.HardwareFacts{
					CPUCount: 1, CPUModel: "AMD EPYC 9354P 32-Core Processor", CPUCores: 32,
					RAMTotalGB: 64, RAMSlotsTotal: 24, RAMSlotsUsed: 2, RAMSlotsAvailable: 22,
					MemoryType: "DDR5", MemorySpeedMHz: 4800,
					DiskCount: 3, StorageSummary: "2x894GB, 1x14901GB", StorageTotalTB: 15.32,
					BIOSVersion: "1.14.2", PowerState: "On",
					PowerConsumedWatts: 284, PowerPeakWatts: 612,
					LastInventory: time.Date(2026, 8, 23, 6, 14, 2, 0, time.UTC),
				},
			},
			{
				Name:         "srv-berlin-02.example.com",
				Serial:       "CN7792048K0043",
				ServiceTag:   "9LMN4P2",
				Manufacturer: "Dell Inc.",
				Model:        "PowerEdge R740",
				MgmtIP:       "10.0.1.12",
				HW: discovery.HardwareFacts{
					CPUCount: 2, CPUModel: "Intel Xeon Gold 6248R", CPUCores: 24,
					RAMTotalGB: 384, RAMSlotsTotal: 24, RAMSlotsUsed: 6, RAMSlotsAvailable: 18,
					MemoryType: "DDR4", MemorySpeedMHz: 2933,
					BIOSVersion: "2.19.0", PowerState: "Off",
					LastInventory: time.Date(2026, 8, 23, 6, 14, 7, 0, time.UTC),
				},
			},
		},
	}

	if got, expected := jsonOf(t, snap), jsonOf(t, want); got != expected {
		t.Errorf("parsed snapshot differs from the golden expectation\ngot:\n%s\nwant:\n%s", got, expected)
	}

	// The unreachable server is skipped, and said so.
	if len(snap.Devices) != 2 {
		t.Fatalf("parsed %d devices, want the 2 that were collected successfully", len(snap.Devices))
	}
	if !containsSubstring(warnings, "10.0.1.99") {
		t.Errorf("warnings = %v, want the skipped server named", warnings)
	}
	if !containsSubstring(warnings, "1 of 3") {
		t.Errorf("warnings = %v, want a count of what was skipped", warnings)
	}
}

// Only a populated DIMM says anything about the memory type and speed; an
// absent slot reports neither, and reading it would document "" and 0 MHz.
func TestMemoryFactsComeFromAPopulatedSlot(t *testing.T) {
	snap, _ := parseFixture(t, "scan.json")
	second := snap.Devices[1]
	if second.HW.MemoryType != "DDR4" || second.HW.MemorySpeedMHz != 2933 {
		t.Errorf("memory facts = %q/%d, want them read past the absent first slot",
			second.HW.MemoryType, second.HW.MemorySpeedMHz)
	}
}

// A machine with no drives must not carry an invented storage summary.
func TestNoDrivesMeansNoStorageSummary(t *testing.T) {
	snap, _ := parseFixture(t, "scan.json")
	if got := snap.Devices[1].HW.StorageSummary; got != "" {
		t.Errorf("storage summary = %q for a machine with no drives, want empty", got)
	}
}

// The same document parsed twice must yield the same order.
func TestParseIsDeterministic(t *testing.T) {
	first, _ := parseFixture(t, "scan.json")
	second, _ := parseFixture(t, "scan.json")
	if jsonOf(t, first) != jsonOf(t, second) {
		t.Error("two parses of one document produced different snapshots")
	}
}

// An importer that starts stamping a version this parser does not know must
// warn, not fail: refusing would break every ingest pipeline on an upgrade.
func TestUnknownSchemaVersionWarnsButParses(t *testing.T) {
	doc := `{"schema_version": 99, "servers": [
		{"host":"10.0.1.11","serial_number":"S1","collected_at":"2026-08-23T06:00:00Z"}
	], "stats": {"total_servers": 1, "successful_count": 1}}`

	var warnings []string
	snap, err := Parse(bytes.NewReader([]byte(doc)), Options{Warnf: func(format string, args ...interface{}) {
		warnings = append(warnings, fmtSprintf(format, args...))
	}})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(snap.Devices) != 1 {
		t.Fatalf("parsed %d devices, want the document to still be read", len(snap.Devices))
	}
	if !containsSubstring(warnings, "schema_version 99") {
		t.Errorf("warnings = %v, want the unknown version reported", warnings)
	}
}

// A document without a schema version is the importer's original shape and is
// entirely normal.
func TestAbsentSchemaVersionIsSilent(t *testing.T) {
	var warnings []string
	_, err := Parse(bytes.NewReader([]byte(`{"servers": [], "stats": {}}`)), Options{
		Warnf: func(format string, args ...interface{}) { warnings = append(warnings, fmtSprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if containsSubstring(warnings, "schema_version") {
		t.Errorf("warnings = %v, want silence about an absent version", warnings)
	}
}

func TestParseRejectsNonImporterDocuments(t *testing.T) {
	if _, err := Parse(bytes.NewReader([]byte("this is not JSON")), Options{}); err == nil {
		t.Fatal("expected an error for a document that is not the importer's JSON")
	}
}

// A stats block that disagrees with the entries is a sign of a truncated
// artifact, which is worth saying rather than silently documenting half a
// datacentre.
func TestStatsMismatchIsReported(t *testing.T) {
	var warnings []string
	_, err := Parse(bytes.NewReader([]byte(`{"servers":[{"host":"10.0.0.1","serial_number":"S1"}],"stats":{"total_servers":9}}`)),
		Options{Warnf: func(format string, args ...interface{}) { warnings = append(warnings, fmtSprintf(format, args...)) }})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !containsSubstring(warnings, "stats block claims 9") {
		t.Errorf("warnings = %v, want the mismatch reported", warnings)
	}
}

func TestSourceOverride(t *testing.T) {
	snap, err := Parse(bytes.NewReader([]byte(`{"servers":[],"stats":{}}`)), Options{Source: "idrac-berlin"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if snap.Source != "idrac-berlin" {
		t.Errorf("Source = %q, want the override", snap.Source)
	}
	if snap.CollectedAt.IsZero() {
		t.Error("an empty document should still be stamped with a collection time")
	}
}

// fmtSprintf is fmt.Sprintf under a name that says what the warning collector
// is doing with it.
func fmtSprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

func jsonOf(t *testing.T, v interface{}) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
