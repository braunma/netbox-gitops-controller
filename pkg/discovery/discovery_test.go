// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The Snapshot is the contract between every source adapter and everything
// downstream, so its serialized field names are pinned here: renaming one
// silently breaks a recorded snapshot on someone's disk.
func TestSnapshotJSONFieldNames(t *testing.T) {
	snap := Snapshot{
		Source:        "pve-prod",
		SourceVersion: "8.2.2",
		CollectedAt:   time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC),
		Devices: []Device{{
			Name:       "srv-01",
			Serial:     "ABC123",
			ServiceTag: "ABC123",
			MgmtIP:     "10.0.0.5",
			HW: HardwareFacts{
				CPUCount:       2,
				StorageTotalTB: 1.5,
				LastInventory:  time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC),
			},
		}},
		VMs: []VM{{Name: "web-01", VMID: 101, MemoryMB: 8192, DiskGB: 100}},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		`"source":"pve-prod"`, `"source_version":"8.2.2"`, `"collected_at":`,
		`"service_tag":"ABC123"`, `"mgmt_ip":"10.0.0.5"`,
		`"cpu_count":2`, `"storage_total_tb":1.5`, `"last_inventory":`,
		`"vmid":101`, `"memory_mb":8192`, `"disk_gb":100`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("snapshot JSON is missing %s\ngot: %s", want, got)
		}
	}
}

// A fact a source did not report must not appear at all, so that a consumer
// can tell "not reported" from "reported as zero".
func TestUnreportedFactsAreOmitted(t *testing.T) {
	data, err := json.Marshal(Snapshot{Source: "idrac-json"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	for _, absent := range []string{"devices", "vms", "source_version"} {
		if strings.Contains(got, absent) {
			t.Errorf("empty snapshot should not carry %q: %s", absent, got)
		}
	}

	data, err = json.Marshal(HardwareFacts{CPUCount: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); got != `{"cpu_count":1}` {
		t.Errorf("HardwareFacts = %s, want only the reported field", got)
	}
}

func TestHardwareFactsIsZero(t *testing.T) {
	if !(HardwareFacts{}).IsZero() {
		t.Error("an empty HardwareFacts should report itself as zero")
	}
	if (HardwareFacts{PowerState: "Off"}).IsZero() {
		t.Error("a reported power state is an observation, not an absence")
	}
}
