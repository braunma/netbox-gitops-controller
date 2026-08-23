// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// repo writes a data directory and returns its path.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// run builds and applies a plan against a data directory, returning the plan.
func run(t *testing.T, root string, snap *discovery.Snapshot, apply bool) *Plan {
	t.Helper()
	dl := loader.NewDataLoader(root, utils.NewLogger(true))
	inv, err := dl.LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	plan, err := BuildPlan(snap, inv, Options{DataDir: root})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if apply {
		if err := Apply(plan); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	return plan
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// handWritten is a device file in the shape real inventory takes: a comment
// header, a defaults block, blank lines, aligned trailing comments, nested
// interfaces with cabling. Every one of those is a thing the edit must not
// disturb.
const handWritten = `# Server Inventory - Berlin DC
#
# One defaults block per rack keeps each device down to its own facts.

defaults:
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"
  rack_slug: "rack-a-01"
  face: "front"
  status: "active"
  tags: ["gitops", "production"]

devices:
  - name: "srv-01"
    position: 10 # lowest rack unit
    serial: "OLD-SERIAL"

    interfaces:
      - name: "idrac"
        description: "Dell iDRAC interface"
        ip:
          address: "10.0.1.11/24"
          dns_name: "srv-01-idrac.example.com"

      - name: "eth0"
        description: "Primary production interface"
        ip:
          address: "10.0.100.11/24"
        address_role: "primary"
        link:
          peer_device: "sw-01"
          peer_port: "Eth1/1"

  - name: "srv-02"
    position: 12
    serial: "S2"
`

// scannedSrv01 is what an iDRAC scan reports about the first device.
func scannedSrv01() discovery.Device {
	return discovery.Device{
		Name:       "srv-01.berlin.example.com",
		Serial:     "CN7792048K0042",
		ServiceTag: "7XKQ2M3",
		MgmtIP:     "10.0.1.11",
		Model:      "PowerEdge R7615",
		HW: discovery.HardwareFacts{
			CPUCount: 1, CPUModel: "AMD EPYC 9354P 32-Core Processor", CPUCores: 32,
			RAMTotalGB: 64, RAMSlotsTotal: 24, RAMSlotsUsed: 2, RAMSlotsAvailable: 22,
			MemoryType: "DDR5", MemorySpeedMHz: 4800,
			DiskCount: 3, StorageSummary: "2x894GB, 1x14901GB", StorageTotalTB: 15.32,
			BIOSVersion: "1.14.2", PowerState: "On",
			PowerConsumedWatts: 284, PowerPeakWatts: 612,
			LastInventory: time.Date(2026, 8, 23, 6, 14, 2, 0, time.UTC),
		},
	}
}

// The golden case: a scan updates a declared device's serial and hardware
// facts inside the hand-written file, and disturbs nothing else.
func TestInPlaceEditGolden(t *testing.T) {
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": handWritten})

	snap := &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}
	plan := run(t, root, snap, true)

	if plan.Summary.FactsUpdated != 1 {
		t.Errorf("summary = %+v, want one device updated", plan.Summary)
	}

	want := `# Server Inventory - Berlin DC
#
# One defaults block per rack keeps each device down to its own facts.

defaults:
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"
  rack_slug: "rack-a-01"
  face: "front"
  status: "active"
  tags: ["gitops", "production"]

devices:
  - name: "srv-01"
    position: 10 # lowest rack unit
    serial: "CN7792048K0042"
    asset_tag: "7XKQ2M3"
    custom_fields:
      hw_cpu_count: 1
      hw_cpu_model: "AMD EPYC 9354P 32-Core Processor"
      hw_cpu_cores: 32
      hw_ram_total_gb: 64
      hw_ram_slots_total: 24
      hw_ram_slots_used: 2
      hw_ram_slots_available: 22
      hw_memory_type: "DDR5"
      hw_memory_speed_mhz: 4800
      hw_disk_count: 3
      hw_storage_summary: "2x894GB, 1x14901GB"
      hw_storage_total_tb: "15.32"
      hw_bios_version: "1.14.2"
      hw_power_state: "On"
      hw_power_consumed_watts: 284
      hw_power_peak_watts: 612
      hw_last_inventory: "2026-08-23T06:14:02Z"

    interfaces:
      - name: "idrac"
        description: "Dell iDRAC interface"
        ip:
          address: "10.0.1.11/24"
          dns_name: "srv-01-idrac.example.com"

      - name: "eth0"
        description: "Primary production interface"
        ip:
          address: "10.0.100.11/24"
        address_role: "primary"
        link:
          peer_device: "sw-01"
          peer_port: "Eth1/1"

  - name: "srv-02"
    position: 12
    serial: "S2"
`

	if got := read(t, root, "inventory/hardware/active/servers.yaml"); got != want {
		t.Errorf("edited file differs from the golden expectation\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Re-running on unchanged input must produce nothing at all. This is the
// property that makes a scheduled scan usable: a run that always diffs is a
// run nobody reads.
func TestReRunOnUnchangedInputIsAZeroDiff(t *testing.T) {
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": handWritten})
	snap := &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}

	run(t, root, snap, true)
	first := read(t, root, "inventory/hardware/active/servers.yaml")

	second := run(t, root, snap, true)
	if second.Summary.HasChanges() {
		t.Errorf("a second run of the same scan planned changes: %+v", second.Summary)
	}
	if second.Summary.Unchanged != 1 {
		t.Errorf("unchanged = %d, want the device counted as unchanged", second.Summary.Unchanged)
	}
	if got := read(t, root, "inventory/hardware/active/servers.yaml"); got != first {
		t.Error("a second run of the same scan rewrote the file")
	}
}

// A fact that contradicts a declared value overwrites it — that is the point,
// the merge request diff is the review — but only the contradicted line moves.
func TestChangedFactRewritesOnlyItsOwnLine(t *testing.T) {
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": handWritten})
	snap := &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}
	run(t, root, snap, true)

	hotter := scannedSrv01()
	hotter.HW.PowerConsumedWatts = 301
	plan := run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{hotter}}, true)

	if len(plan.Changes) != 1 {
		t.Fatalf("planned %d changes, want 1", len(plan.Changes))
	}
	diff := plan.Changes[0].Diff()
	if strings.Count(diff, "\n-      ") != 1 || strings.Count(diff, "\n+      ") != 1 {
		t.Errorf("one changed watt reading produced more than a one-line diff:\n%s", diff)
	}
	if !strings.Contains(diff, "+      hw_power_consumed_watts: 301") {
		t.Errorf("diff does not carry the new reading:\n%s", diff)
	}
}

// Nothing outside the whitelist is ever written, however much the scan knows.
func TestNothingOutsideTheWhitelistIsWritten(t *testing.T) {
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": handWritten})

	dev := scannedSrv01()
	dev.Name = "a-name-the-scan-would-like-to-impose"
	dev.Model = "PowerEdge R7615"
	dev.Manufacturer = "Dell Inc."
	dev.Interfaces = []discovery.NIC{{Name: "eth9", MAC: "AA:BB:CC:DD:EE:FF"}}
	// Match by the address it was scanned on, so the name cannot be the reason.
	dev.Serial = ""
	dev.ServiceTag = ""

	run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{dev}}, true)
	got := read(t, root, "inventory/hardware/active/servers.yaml")

	for _, forbidden := range []string{
		"a-name-the-scan-would-like-to-impose", "PowerEdge R7615", "Dell Inc.", "eth9", "AA:BB:CC:DD:EE:FF",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the scan wrote %q into the YAML; only whitelisted keys may be written\n%s", forbidden, got)
		}
	}
	// The declared serial is untouched, because the scan reported none.
	if !strings.Contains(got, `serial: "OLD-SERIAL"`) {
		t.Error("a scan that reported no serial blanked the declared one")
	}
}

// A custom field somebody else owns is left alone.
func TestForeignCustomFieldsAreUntouched(t *testing.T) {
	const file = `- name: "srv-01"
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"
  serial: "S1"
  custom_fields:
    owner_team: "platform"
    hw_cpu_count: 99
`
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": file})

	dev := discovery.Device{Serial: "S1", HW: discovery.HardwareFacts{CPUCount: 1}}
	run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{dev}}, true)

	got := read(t, root, "inventory/hardware/active/servers.yaml")
	if !strings.Contains(got, `owner_team: "platform"`) {
		t.Errorf("a custom field outside the prefix was disturbed:\n%s", got)
	}
	if !strings.Contains(got, "hw_cpu_count: 1") {
		t.Errorf("the scan's own field was not updated:\n%s", got)
	}
}

// Comments, quoting style and key order all survive an edit.
func TestEditPreservesCommentsQuotingAndOrder(t *testing.T) {
	const file = `- name: 'web-01' # the production web head
  cluster: "berlin-prod-cluster"
  vcpus: 2 # was 1 before the resize
  status: active
  memory: 4096
`
	root := repo(t, map[string]string{"inventory/virtual/prod/web-01.yaml": file})

	snap := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{{
		Name: "web-01", Cluster: "berlin-prod-cluster",
		VCPUs: 4, MemoryMB: 8192, Status: "active", VMID: 101,
	}}}
	run(t, root, snap, true)

	want := `- name: 'web-01' # the production web head
  cluster: "berlin-prod-cluster"
  vcpus: 4 # was 1 before the resize
  status: active
  memory: 8192
  vmid: 101
`
	if got := read(t, root, "inventory/virtual/prod/web-01.yaml"); got != want {
		t.Errorf("edit did not preserve the file\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A `#` inside a quoted value is part of the value, not a comment.
func TestHashInsideAQuotedValueIsNotAComment(t *testing.T) {
	const file = `- name: "srv-01"
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"
  serial: "ABC#123" # the vendor really does put a hash in it
`
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": file})

	dev := discovery.Device{Name: "srv-01", Serial: "XYZ#456"}
	run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{dev}}, true)

	got := read(t, root, "inventory/hardware/active/servers.yaml")
	if !strings.Contains(got, `serial: "XYZ#456" # the vendor really does put a hash in it`) {
		t.Errorf("the quoted value or its comment was mangled:\n%s", got)
	}
}

// Unknown VMs become complete, appliable YAML; the file is byte-stable.
func TestGeneratedVMFileIsCompleteAndStable(t *testing.T) {
	root := repo(t, nil)
	snap := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "web-02", Cluster: "berlin-prod-cluster", Node: "pve-01", Status: "active",
			VCPUs: 4, MemoryMB: 8192, DiskGB: 100, VMID: 102,
			Interfaces: []discovery.NIC{{Name: "net0", MAC: "BC:24:11:00:00:01", Enabled: true}}},
		{Name: "api-01", Cluster: "berlin-prod-cluster", Node: "pve-02", Status: "offline",
			VCPUs: 2, MemoryMB: 4096, VMID: 103},
	}}

	plan := run(t, root, snap, true)
	if plan.Summary.NewVMs != 2 {
		t.Errorf("summary = %+v, want two new VMs", plan.Summary)
	}

	rel := "inventory/discovered/virtual/pve-prod/berlin-prod-cluster.yaml"
	want := `# Generated by ` + "`netbox-gitops collect`" + ` from source "pve-prod".
#
# Every virtual machine below was found on that source and was declared
# nowhere else in this repository when this file was written. It is an
# ordinary inventory file: it is applied to NetBox like any other, and the
# facts of the machines in it are refreshed here in place.
#
# To take a machine over, declare it in a file of your own. The next run
# sees the two declarations, keeps yours, and removes the block from here.
# A machine that stops answering is never removed by a scan — that is a
# fact for you to interpret.
#
# Edits to keys a collector owns (vcpus, memory, disk, status, vmid) are
# overwritten by the next run. Everything else is yours.

- name: "api-01"
  cluster: "berlin-prod-cluster"
  node: "pve-02"
  status: "offline"
  vcpus: 2
  memory: 4096 # MB
  vmid: 103

- name: "web-02"
  cluster: "berlin-prod-cluster"
  node: "pve-01"
  status: "active"
  vcpus: 4
  memory: 8192 # MB
  disk: 100 # GB
  vmid: 102
  interfaces:
    - name: "net0"
      mac_address: "BC:24:11:00:00:01"

`
	if got := read(t, root, rel); got != want {
		t.Errorf("generated VM file differs\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// Byte-stable: the same snapshot produces the identical file.
	second := run(t, root, snap, true)
	if second.Summary.HasChanges() {
		t.Errorf("re-generating an unchanged cluster planned changes: %+v", second.Summary)
	}
}

// A VM declared by hand is never also generated, so nothing is declared twice.
func TestADeclaredVMIsNeverAlsoGenerated(t *testing.T) {
	root := repo(t, map[string]string{
		"inventory/virtual/prod/web-02.yaml": "- name: \"web-02\"\n  cluster: \"berlin-prod-cluster\"\n",
	})
	snap := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "web-02", Cluster: "berlin-prod-cluster", VCPUs: 4},
	}}

	plan := run(t, root, snap, true)
	if plan.Summary.NewVMs != 0 {
		t.Errorf("a VM the repository declares was generated as well: %+v", plan.Summary)
	}
	if _, err := os.Stat(filepath.Join(root, "inventory/discovered/virtual/pve-prod")); !os.IsNotExist(err) {
		t.Error("a generated file was written for a VM that is already declared")
	}
	if got := read(t, root, "inventory/virtual/prod/web-02.yaml"); !strings.Contains(got, "vcpus: 4") {
		t.Errorf("the fact was not written into the hand-managed file:\n%s", got)
	}
}

// Adopting a generated VM: declare it in a file of your own, and the next run
// takes it out of the generated file rather than leaving the repository
// describing it twice.
//
// The previous version of this test performed the removal itself and then
// asserted it did not come back, which passed without exercising the behaviour
// its name claimed. This one only adds a declaration — the thing a person
// actually does — and lets the run do the rest.
func TestAdoptingAGeneratedVMRemovesItFromTheGeneratedFile(t *testing.T) {
	root := repo(t, nil)
	snap := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "web-02", Cluster: "berlin-prod-cluster", VCPUs: 4},
		{Name: "api-01", Cluster: "berlin-prod-cluster", VCPUs: 2},
	}}
	run(t, root, snap, true)

	generated := "inventory/discovered/virtual/pve-prod/berlin-prod-cluster.yaml"
	if !strings.Contains(read(t, root, generated), "web-02") {
		t.Fatal("setup: web-02 was not generated")
	}

	// The operator adopts web-02 by declaring it, and does NOT touch the
	// generated file.
	adopt := filepath.Join(root, "inventory/virtual/prod/web-02.yaml")
	if err := os.MkdirAll(filepath.Dir(adopt), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adopt,
		[]byte("- name: \"web-02\"\n  cluster: \"berlin-prod-cluster\"\n  role_slug: \"vm\"\n  vcpus: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := run(t, root, snap, true)
	if plan.Summary.Adopted != 1 {
		t.Errorf("summary = %+v, want one adopted object", plan.Summary)
	}

	after := read(t, root, generated)
	if strings.Contains(after, "web-02") {
		t.Errorf("the adopted VM is still declared in the generated file:\n%s", after)
	}
	if !strings.Contains(after, "api-01") {
		t.Errorf("the VM that was not adopted was dropped too:\n%s", after)
	}
	// The header and the untouched entry survive the removal intact.
	if !strings.Contains(after, "# Generated by") {
		t.Errorf("the removal ate the file header:\n%s", after)
	}
	if !strings.Contains(read(t, root, "inventory/virtual/prod/web-02.yaml"), `role_slug: "vm"`) {
		t.Error("the operator's own declaration was disturbed")
	}

	// And it stays gone: a third run neither re-generates nor re-removes it.
	third := run(t, root, snap, true)
	if third.Summary.HasChanges() {
		t.Errorf("a run after the adoption still planned changes: %+v", third.Summary)
	}
}

// A machine that stops answering is never removed from the YAML. Its
// disappearance is a fact for a person to interpret, not a deletion to apply.
func TestAVMThatVanishesFromTheSourceIsKept(t *testing.T) {
	root := repo(t, nil)
	full := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "web-02", Cluster: "berlin-prod-cluster", VCPUs: 4},
		{Name: "api-01", Cluster: "berlin-prod-cluster", VCPUs: 2},
	}}
	run(t, root, full, true)

	// api-01 is deleted on the hypervisor; the next scan does not report it.
	shrunk := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "web-02", Cluster: "berlin-prod-cluster", VCPUs: 4},
	}}
	plan := run(t, root, shrunk, true)

	generated := read(t, root, "inventory/discovered/virtual/pve-prod/berlin-prod-cluster.yaml")
	if !strings.Contains(generated, "api-01") {
		t.Errorf("a VM that vanished from the source was deleted from the YAML:\n%s", generated)
	}
	if plan.Summary.HasChanges() {
		t.Errorf("summary = %+v, want a vanished VM to change nothing", plan.Summary)
	}
}

// Adopting a scanned server works the same way: declare it properly and the
// parked skeleton stops declaring it.
func TestAdoptingAParkedDeviceRemovesTheSkeleton(t *testing.T) {
	root := repo(t, nil)
	run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}, true)

	skeleton := "inventory/discovered/hardware/idrac/_cn7792048k0042.yaml"
	if !strings.Contains(read(t, root, skeleton), "CN7792048K0042") {
		t.Fatal("setup: no skeleton was written")
	}

	// The operator declares the machine properly, in their own file.
	own := filepath.Join(root, "inventory/hardware/active/servers.yaml")
	if err := os.MkdirAll(filepath.Dir(own), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte(`- name: "srv-07"
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"
  rack_slug: "rack-a-01"
  position: 22
  serial: "CN7792048K0042"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}, true)
	if plan.Summary.Adopted != 1 {
		t.Errorf("summary = %+v, want the skeleton's declaration removed", plan.Summary)
	}
	if _, err := os.Stat(filepath.Join(root, skeleton)); err == nil {
		if body := read(t, root, skeleton); strings.Contains(body, "CN7792048K0042") {
			t.Errorf("the skeleton still declares the adopted machine:\n%s", body)
		}
	}
	if !strings.Contains(read(t, root, "inventory/hardware/active/servers.yaml"), "position: 22") {
		t.Error("the operator's own declaration was disturbed")
	}
}

// An unknown machine becomes a parked skeleton carrying everything the scan
// knows and TODOs for everything it cannot.
func TestParkedSkeletonForAnUnknownDevice(t *testing.T) {
	root := repo(t, nil)
	dev := scannedSrv01()
	dev.Interfaces = []discovery.NIC{{Name: "NIC.Integrated.1-1", MAC: "AA:BB:CC:00:11:22"}}

	plan := run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{dev}}, true)
	if plan.Summary.ParkedDevices != 1 {
		t.Fatalf("summary = %+v, want one parked device", plan.Summary)
	}

	rel := "inventory/discovered/hardware/idrac/_cn7792048k0042.yaml"
	if len(plan.Parked) != 1 || !strings.HasSuffix(plan.Parked[0], rel) {
		t.Errorf("parked list = %v, want it to name %s", plan.Parked, rel)
	}

	got := read(t, root, rel)
	for _, want := range []string{
		"NOT APPLIED",
		`site_slug: "TODO"`, `rack_slug: "TODO"`, "position: 0 # TODO", `face: "front" # TODO`,
		`role_slug: "TODO"`, `device_type_slug: "TODO"`,
		`serial: "CN7792048K0042"`, `asset_tag: "7XKQ2M3"`,
		"hw_cpu_count: 1", "hw_power_state: \"On\"",
		"#   management address: 10.0.1.11",
		"#   NIC.Integrated.1-1  AA:BB:CC:00:11:22",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("parked skeleton is missing %q:\n%s", want, got)
		}
	}
}

// The skeleton is parked, so the reconciler's loader skips it — but the next
// ingest run sees it, matches it and refreshes its facts in place instead of
// writing a second copy.
func TestASecondScanRefreshesTheParkedSkeletonInPlace(t *testing.T) {
	root := repo(t, nil)
	run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}, true)

	rel := "inventory/discovered/hardware/idrac/_cn7792048k0042.yaml"
	completed := strings.Replace(read(t, root, rel), `site_slug: "TODO"`, `site_slug: "berlin-dc"`, 1)
	if err := os.WriteFile(filepath.Join(root, rel), []byte(completed), 0o600); err != nil {
		t.Fatal(err)
	}

	hotter := scannedSrv01()
	hotter.HW.PowerConsumedWatts = 999
	plan := run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{hotter}}, true)

	if plan.Summary.ParkedDevices != 0 {
		t.Errorf("summary = %+v, want the known machine not parked again", plan.Summary)
	}
	if plan.Summary.FactsUpdated != 1 {
		t.Errorf("summary = %+v, want the skeleton's facts refreshed", plan.Summary)
	}
	got := read(t, root, rel)
	if !strings.Contains(got, "hw_power_consumed_watts: 999") {
		t.Errorf("the skeleton's facts were not refreshed:\n%s", got)
	}
	if !strings.Contains(got, `site_slug: "berlin-dc"`) {
		t.Error("the human's completed field was overwritten")
	}
}

// A dry run writes nothing.
func TestPlanWritesNothing(t *testing.T) {
	root := repo(t, map[string]string{"inventory/hardware/active/servers.yaml": handWritten})
	before := read(t, root, "inventory/hardware/active/servers.yaml")

	plan := run(t, root, &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{scannedSrv01()}}, false)
	if !plan.Summary.HasChanges() {
		t.Fatal("the plan found nothing to do")
	}
	if got := read(t, root, "inventory/hardware/active/servers.yaml"); got != before {
		t.Error("building a plan wrote to the file")
	}
	if _, err := os.Stat(filepath.Join(root, "inventory/discovered")); !os.IsNotExist(err) {
		t.Error("building a plan created the discovered directory")
	}
}

// The verifier is the backstop: if surgery ever produced something other than
// what was intended, the file must not be written.
func TestVerifierRefusesAMismatchedRewrite(t *testing.T) {
	change := &FileChange{
		Path:   "servers.yaml",
		Before: "- name: \"srv-01\"\n  serial: \"S1\"\n",
		After:  "- name: \"srv-01\"\n  serial: \"WRONG\"\n",
	}
	edits := []objectEdit{{
		Label:   `device "srv-01"`,
		Prov:    loader.Provenance{File: "servers.yaml"},
		Scalars: []keyValue{{Key: KeyDeviceSerial, Value: "S2"}},
	}}

	if err := verifyFileChange(change, edits); err == nil {
		t.Fatal("the verifier accepted a rewrite that says something else")
	} else if !strings.Contains(err.Error(), "was not written") {
		t.Errorf("error = %q, want it to say the file was left alone", err)
	}
}

// A generated file whose every object has been adopted is removed, not left as
// a header with nothing under it — and the removal is visible as a deletion in
// the diff, so a reviewer sees it.
func TestAFullyAdoptedGeneratedFileIsDeleted(t *testing.T) {
	root := repo(t, nil)
	snap := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "only-01", Cluster: "berlin-prod-cluster", VCPUs: 2},
	}}
	run(t, root, snap, true)

	generated := filepath.Join(root, "inventory/discovered/virtual/pve-prod/berlin-prod-cluster.yaml")
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("setup: %v", err)
	}

	adopt := filepath.Join(root, "inventory/virtual/prod/only-01.yaml")
	if err := os.MkdirAll(filepath.Dir(adopt), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adopt, []byte("- name: \"only-01\"\n  cluster: \"berlin-prod-cluster\"\n  vcpus: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := run(t, root, snap, true)
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ActionDelete {
		t.Fatalf("changes = %+v, want a single deletion", plan.Changes)
	}
	if diff := plan.Changes[0].Diff(); !strings.Contains(diff, "+++ /dev/null") {
		t.Errorf("the diff does not read as a deletion:\n%s", diff)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Error("the emptied generated file was left behind")
	}
}

// A file cannot be rewritten for its facts and for a removal in one run: both
// are computed from the same original line numbers. The run must say so and do
// neither, rather than half-apply.
func TestFactsAndRemovalInOneFileIsRefused(t *testing.T) {
	root := repo(t, nil)
	snap := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "keep-01", Cluster: "berlin-prod-cluster", VCPUs: 2},
		{Name: "adopt-01", Cluster: "berlin-prod-cluster", VCPUs: 2},
	}}
	run(t, root, snap, true)

	adopt := filepath.Join(root, "inventory/virtual/prod/adopt-01.yaml")
	if err := os.MkdirAll(filepath.Dir(adopt), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adopt, []byte("- name: \"adopt-01\"\n  cluster: \"berlin-prod-cluster\"\n  vcpus: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// keep-01 also grew, so the generated file has both a fact to write and an
	// object to remove.
	grown := &discovery.Snapshot{Source: "pve-prod", VMs: []discovery.VM{
		{Name: "keep-01", Cluster: "berlin-prod-cluster", VCPUs: 8},
		{Name: "adopt-01", Cluster: "berlin-prod-cluster", VCPUs: 2},
	}}

	dl := loader.NewDataLoader(root, utils.NewLogger(true))
	inv, err := dl.LoadInventoryWithProvenance()
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildPlan(grown, inv, Options{DataDir: root})
	if err == nil {
		t.Fatal("expected the combination to be refused")
	}
	if !strings.Contains(err.Error(), "neither was written") {
		t.Errorf("error = %q, want it to say nothing was written", err)
	}

	// And the file is genuinely untouched.
	if !strings.Contains(read(t, root, "inventory/discovered/virtual/pve-prod/berlin-prod-cluster.yaml"), "vcpus: 2") {
		t.Error("the refused run wrote to the file anyway")
	}
}
