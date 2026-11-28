// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// fakeIDRAC serves a small Redfish tree. Paths in `missing` answer 404, and a
// wrong password answers 401, as an iDRAC does.
type fakeIDRAC struct {
	mu       sync.Mutex
	missing  map[string]bool
	methods  map[string]int
	password string
}

var fakeTree = map[string]string{
	redfishSystemPath: `{"Model":"PowerEdge R7615","Manufacturer":"Dell Inc.","SerialNumber":" CN7792048K0042 ","SKU":"7XKQ2M3",
		"BiosVersion":"1.6.10","HostName":"ceph-node-01.example","PowerState":"On",
		"ProcessorSummary":{"Count":3,"Model":"AMD EPYC 9354P 32-Core Processor"},
		"MemorySummary":{"TotalSystemMemoryGiB":128},
		"Oem":{"Dell":{"DellSystem":{"MaxDIMMSlots":12}}}}`,
	redfishProcessorsPath: `{"Members":[{"@odata.id":"/p/cpu1"},{"@odata.id":"/p/cpu2"},{"@odata.id":"/p/gpu1"}]}`,
	"/p/cpu1":             `{"Model":"AMD EPYC 9354P","ProcessorType":"CPU","TotalCores":32,"Status":{"State":"Enabled"}}`,
	"/p/cpu2":             `{"Model":"","ProcessorType":"CPU","TotalCores":0,"Status":{"State":"Absent"}}`,
	"/p/gpu1":             `{"Model":"NVIDIA A100","ProcessorType":"GPU","TotalCores":0,"Status":{"State":"Enabled"},"ProcessorMemory":[{"CapacityMiB":81920}]}`,
	redfishMemoryPath:     `{"Members":[{"@odata.id":"/m/a1"},{"@odata.id":"/m/a2"},{"@odata.id":"/m/a3"}]}`,
	"/m/a1":               `{"MemoryDeviceType":"","CapacityMiB":0,"Status":{"State":"Absent"}}`,
	"/m/a2":               `{"MemoryDeviceType":"DDR5","CapacityMiB":65536,"OperatingSpeedMhz":4800,"Status":{"State":"Enabled"}}`,
	"/m/a3":               `{"MemoryDeviceType":"DDR5","CapacityMiB":65536,"OperatingSpeedMhz":4800,"Status":{"State":"Enabled"}}`,
	redfishStoragePath:    `{"Members":[{"@odata.id":"/s/raid"},{"@odata.id":"/s/empty"}]}`,
	"/s/raid":             `{"Drives":[{"@odata.id":"/d/1"},{"@odata.id":"/d/2"},{"@odata.id":"/d/3"}]}`,
	"/s/empty":            `{"Drives":[]}`,
	"/d/1":                `{"CapacityBytes":960197124096}`,
	"/d/2":                `{"CapacityBytes":960197124096}`,
	"/d/3":                `{"CapacityBytes":16000900608000}`,
	redfishPowerPath:      `{"PowerControl":[{"PowerConsumedWatts":412,"PowerMetrics":{"MaxConsumedWatts":655}}]}`,
}

func (f *fakeIDRAC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.methods[r.Method]++
	missing := f.missing[r.URL.Path]
	f.mu.Unlock()

	if _, pass, ok := r.BasicAuth(); !ok || pass != f.password {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, ok := fakeTree[r.URL.Path]
	if !ok || missing {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// newFakeIDRAC starts a TLS server and returns a scanner pointed at it, with
// the target that reaches it.
func newFakeIDRAC(t *testing.T, password string) (*fakeIDRAC, *IDRACScanner, Target) {
	t.Helper()
	fake := &fakeIDRAC{missing: map[string]bool{}, methods: map[string]int{}, password: "secret"}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	t.Setenv("TEST_IDRAC_USER", "reader")
	t.Setenv("TEST_IDRAC_PASS", password)
	noVerify := false
	scanner, err := NewIDRACScanner(Source{
		Name: "idrac-test", Type: TypeIDRAC,
		UsernameEnv: "TEST_IDRAC_USER", PasswordEnv: "TEST_IDRAC_PASS",
		Targets: TargetsInventory, Interface: "idrac",
		VerifyTLS: &noVerify, Port: port,
	}, utils.NewDiscardLogger())
	if err != nil {
		t.Fatalf("NewIDRACScanner: %v", err)
	}
	scanner.now = func() time.Time { return time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC) }
	return fake, scanner, Target{Device: "ceph-node-01", Address: host}
}

func TestIDRACScanReadsHardwareFacts(t *testing.T) {
	fake, scanner, target := newFakeIDRAC(t, "secret")

	results := scanner.Scan(context.Background(), []Target{target})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("scan failed: %v", r.Err)
	}
	if len(r.Incomplete) != 0 {
		t.Errorf("Incomplete = %v, want none", r.Incomplete)
	}
	if r.Target != target {
		t.Errorf("result belongs to %+v, want the target it was scanned for", r.Target)
	}

	dev := r.Device
	if dev.Serial != "CN7792048K0042" || dev.ServiceTag != "7XKQ2M3" {
		t.Errorf("serial/service tag = %q/%q", dev.Serial, dev.ServiceTag)
	}
	hw := dev.HW
	checks := []struct {
		name      string
		got, want interface{}
	}{
		// The absent socket and the GPU are not CPUs.
		{"CPUCount", hw.CPUCount, 1},
		{"CPUModel", hw.CPUModel, "AMD EPYC 9354P 32-Core Processor"},
		{"CPUCores", hw.CPUCores, 32},
		{"GPUsRead", hw.GPUsRead, true},
		{"GPUCount", hw.GPUCount, 1},
		{"GPUModel", hw.GPUModel, "1× NVIDIA A100 (80 GB)"},
		{"GPUMemoryGB", hw.GPUMemoryGB, 80},
		{"RAMTotalGB", hw.RAMTotalGB, 128},
		// The Dell OEM block wins over the three slots the collection lists.
		{"RAMSlotsTotal", hw.RAMSlotsTotal, 12},
		{"RAMSlotsUsed", hw.RAMSlotsUsed, 2},
		{"RAMSlotsAvailable", hw.RAMSlotsAvailable, 10},
		{"MemoryType", hw.MemoryType, "DDR5"},
		{"MemorySpeedMHz", hw.MemorySpeedMHz, 4800},
		{"DiskCount", hw.DiskCount, 3},
		{"StorageSummary", hw.StorageSummary, "2x894GB, 1x14901GB"},
		{"BIOSVersion", hw.BIOSVersion, "1.6.10"},
		{"PowerState", hw.PowerState, "On"},
		{"PowerConsumedWatts", hw.PowerConsumedWatts, 412},
		{"PowerPeakWatts", hw.PowerPeakWatts, 655},
		{"LastInventory", hw.LastInventory, time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if hw.StorageTotalTB < 16.2 || hw.StorageTotalTB > 16.4 {
		t.Errorf("StorageTotalTB = %v, want about 16.3", hw.StorageTotalTB)
	}

	// The scanner must never send anything but a GET to a BMC.
	for method, n := range fake.methods {
		if method != http.MethodGet {
			t.Errorf("%d %s request(s) reached the BMC; a scan only reads", n, method)
		}
	}
}

// A part of the inventory that does not answer is named and left unreported;
// it does not fail the host and does not zero the facts.
func TestIDRACScanPartialAnswer(t *testing.T) {
	fake, scanner, target := newFakeIDRAC(t, "secret")
	fake.missing[redfishPowerPath] = true
	fake.missing["/m/a3"] = true

	r := scanner.Scan(context.Background(), []Target{target})[0]
	if r.Err != nil {
		t.Fatalf("a missing part failed the whole host: %v", r.Err)
	}
	if got := strings.Join(r.Incomplete, ","); got != "memory,power" {
		t.Errorf("Incomplete = %q, want memory,power", got)
	}
	hw := r.Device.HW
	if hw.PowerConsumedWatts != 0 || hw.RAMSlotsUsed != 0 || hw.MemoryType != "" {
		t.Errorf("facts of an unread part were reported: %+v", hw)
	}
	if hw.DiskCount != 3 {
		t.Errorf("DiskCount = %d; the parts that answered must still be reported", hw.DiskCount)
	}
}

func TestIDRACScanRejectedCredentials(t *testing.T) {
	_, scanner, target := newFakeIDRAC(t, "wrong")

	r := scanner.Scan(context.Background(), []Target{target})[0]
	if !errors.Is(r.Err, errRedfishAuth) {
		t.Fatalf("Err = %v, want the credentials error", r.Err)
	}
}

// One host that fails does not stop the others, and results keep target order.
func TestIDRACScanKeepsOrderAcrossFailures(t *testing.T) {
	_, scanner, good := newFakeIDRAC(t, "secret")
	scanner.client.Timeout = 2 * time.Second
	// TEST-NET-1 is not routed; whatever the error, it must be on that entry.
	bad := Target{Device: "gone", Address: "192.0.2.1"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	results := scanner.Scan(ctx, []Target{bad, good, bad})
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Err == nil || results[2].Err == nil {
		t.Error("an unreachable host reported no error")
	}
	if results[1].Err != nil || results[1].Target.Device != good.Device {
		t.Errorf("the reachable host did not come back in its place: %+v", results[1])
	}
}

func TestNewIDRACScannerNeedsCredentials(t *testing.T) {
	t.Setenv("TEST_IDRAC_USER", "reader")
	_, err := NewIDRACScanner(Source{
		Name: "idrac-test", Type: TypeIDRAC,
		UsernameEnv: "TEST_IDRAC_USER", PasswordEnv: "TEST_IDRAC_UNSET_PASS",
	}, utils.NewDiscardLogger())
	if err == nil || !strings.Contains(err.Error(), "TEST_IDRAC_UNSET_PASS") {
		t.Fatalf("err = %v, want one naming the unset variable", err)
	}
}

func device(name, site, rack, role string, tags []string, ifaces map[string]string) *models.DeviceConfig {
	dev := &models.DeviceConfig{Name: name, SiteSlug: site, RackSlug: rack, RoleSlug: role, Tags: tags}
	for ifName, addr := range ifaces {
		iface := models.InterfaceConfig{Name: ifName}
		if addr != "" {
			iface.IP = &models.IPConfig{Address: addr}
		}
		dev.Interfaces = append(dev.Interfaces, iface)
	}
	return dev
}

// deviceTypes: servers ("r660") have an idrac port, nothing else does.
var deviceTypes = []*models.DeviceType{
	{Slug: "r660", Interfaces: []models.InterfaceTemplate{{Name: "idrac"}, {Name: "eth0"}}},
	{Slug: "panel", Interfaces: nil},
}

func withType(dev *models.DeviceConfig, slug string) *models.DeviceConfig {
	dev.DeviceTypeSlug = slug
	return dev
}

func scanSource(name string, sel Selector) Source {
	return Source{Name: name, Type: TypeIDRAC, Targets: TargetsInventory, Interface: "idrac", Select: sel}
}

func TestAssignTargets(t *testing.T) {
	devices := []*models.DeviceConfig{
		device("ceph-02", "berlin", "a08", "server", []string{"ceph"}, map[string]string{"idrac": "192.0.2.12/24", "eth0": "10.0.0.12/24"}),
		device("ceph-01", "berlin", "a08", "server", []string{"ceph", "gitops"}, map[string]string{"idrac": "192.0.2.11/24"}),
		device("gpu-01", "lab", "b01", "server", nil, map[string]string{"idrac": "198.51.100.5"}),
		device("ceph-03", "berlin", "a08", "server", []string{"ceph"}, map[string]string{"idrac": ""}),
		device("sw-01", "berlin", "a08", "switch", nil, map[string]string{"mgmt": "10.0.0.1/24"}),
		// A server whose idrac port exists only on its device type, with no
		// address declared, and a patch panel that has no such port.
		withType(device("ceph-04", "berlin", "a08", "server", []string{"ceph"}, nil), "r660"),
		withType(device("pp-01", "berlin", "a08", "patch-panel", []string{"ceph"}, nil), "panel"),
		device("orphan", "munich", "c01", "server", nil, map[string]string{"idrac": "203.0.113.9/24"}),
	}
	sources := []Source{
		scanSource("idrac-ceph", Selector{Tags: []string{"ceph"}}),
		scanSource("idrac-lab", Selector{Site: "lab"}),
	}

	got, err := AssignTargets(sources, devices, deviceTypes)
	if err != nil {
		t.Fatalf("AssignTargets: %v", err)
	}

	ceph := got.Targets["idrac-ceph"]
	if len(ceph) != 2 || ceph[0] != (Target{"ceph-01", "192.0.2.11"}) || ceph[1] != (Target{"ceph-02", "192.0.2.12"}) {
		t.Errorf("idrac-ceph targets = %+v, want ceph-01 and ceph-02 by name, without prefix length", ceph)
	}
	if lab := got.Targets["idrac-lab"]; len(lab) != 1 || lab[0] != (Target{"gpu-01", "198.51.100.5"}) {
		t.Errorf("idrac-lab targets = %+v", lab)
	}
	if na := got.NoAddress["idrac-ceph"]; len(na) != 2 || na[0] != "ceph-03" || na[1] != "ceph-04" {
		t.Errorf("NoAddress = %v, want the two servers with an idrac port and no address, and not the patch panel", na)
	}
	// The switch has no idrac interface at all, so it is nobody's gap.
	if len(got.Unassigned) != 1 || got.Unassigned[0] != "orphan" {
		t.Errorf("Unassigned = %v, want [orphan]", got.Unassigned)
	}
}

// Two sources selecting one device would send it two sets of credentials.
func TestAssignTargetsRejectsOverlap(t *testing.T) {
	devices := []*models.DeviceConfig{
		device("ceph-01", "berlin", "a08", "server", []string{"ceph"}, map[string]string{"idrac": "192.0.2.11/24"}),
	}
	sources := []Source{
		scanSource("by-tag", Selector{Tags: []string{"ceph"}}),
		scanSource("by-site", Selector{Site: "berlin"}),
	}

	_, err := AssignTargets(sources, devices, deviceTypes)
	if err == nil {
		t.Fatal("an overlapping selection was accepted")
	}
	for _, want := range []string{"ceph-01", "by-tag", "by-site"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// A prefix splits one site into two credential groups by management network.
func TestAssignTargetsByPrefix(t *testing.T) {
	devices := []*models.DeviceConfig{
		device("old-01", "berlin", "a01", "server", nil, map[string]string{"idrac": "192.0.2.20/24"}),
		device("new-01", "berlin", "a02", "server", nil, map[string]string{"idrac": "198.51.100.20/24"}),
	}
	sources := []Source{
		scanSource("oob-old", Selector{Prefix: "192.0.2.0/24"}),
		scanSource("oob-new", Selector{Prefix: "198.51.100.0/24"}),
	}

	got, err := AssignTargets(sources, devices, deviceTypes)
	if err != nil {
		t.Fatalf("AssignTargets: %v", err)
	}
	if o := got.Targets["oob-old"]; len(o) != 1 || o[0].Device != "old-01" {
		t.Errorf("oob-old = %+v", o)
	}
	if n := got.Targets["oob-new"]; len(n) != 1 || n[0].Device != "new-01" {
		t.Errorf("oob-new = %+v", n)
	}
	if len(got.Unassigned) != 0 {
		t.Errorf("Unassigned = %v, want none", got.Unassigned)
	}
}
