// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/ingest"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// The short field names and per-interface defaults, end to end: a file
// written in the compact form is edited by ingest, loaded, and synced, and
// NetBox ends up holding every reference and every defaulted interface value.
// The loader tests prove the compact form decodes to the same models; this
// proves nothing downstream of the loader depends on the spelling.
func TestCompactFormReachesNetBox(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	rack := f.seed("dcim", "racks", client.Object{"name": "R01", "site": siteID})
	vrf := f.seed("ipam", "vrfs", client.Object{"name": "Management"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	root := t.TempDir()
	servers := filepath.Join(root, "inventory/hardware/active/servers.yaml")
	if err := os.MkdirAll(filepath.Dir(servers), 0o750); err != nil {
		t.Fatal(err)
	}
	const original = `defaults:
  site: "berlin-dc"
  role: "switch"
  device_type: "c9300"
  rack: "R01"
  face: "front"
  interfaces:
    idrac:
      description: "BMC"
      ip: { status: "active", vrf: "Management" }
      address_role: "primary"

devices:
  - name: "srv-01"
    position: 10
    serial: "STALE"
    interfaces:
      - name: "idrac"
        ip: "10.0.10.1/24"
`
	if err := os.WriteFile(servers, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ingest edits the compact file in place without disturbing it.
	dl := loader.NewDataLoader(root, utils.NewLogger(false))
	inv, err := dl.LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("LoadInventoryWithProvenance() error = %v", err)
	}
	snap := &discovery.Snapshot{Source: "idrac", Devices: []discovery.Device{{Name: "srv-01", Serial: "CN-0042"}}}
	plan, err := ingest.BuildPlan(snap, inv, ingest.Options{DataDir: root})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if err := ingest.Apply(plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	written, err := os.ReadFile(servers)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(original, `"STALE"`, `"CN-0042"`, 1); string(written) != want {
		t.Fatalf("ingest rewrote more than the serial:\n%s", written)
	}

	devices, err := dl.LoadDevices("inventory/hardware/active")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	stored := f.objects("dcim", "devices")
	if len(stored) != 1 {
		t.Fatalf("NetBox holds %d devices, want 1", len(stored))
	}
	device := stored[0]
	for field, want := range map[string]int{
		"site": siteID, "role": roleID, "device_type": deviceTypeID, "rack": utils.GetIDFromObject(rack),
	} {
		if got := utils.GetIDFromObject(device[field]); got != want {
			t.Errorf("device %s = %d, want %d (from the short key)", field, got, want)
		}
	}
	if device["serial"] != "CN-0042" {
		t.Errorf("serial = %v, want the ingested one", device["serial"])
	}

	ifaces := f.objects("dcim", "interfaces")
	if len(ifaces) != 1 || ifaces[0]["name"] != "idrac" || ifaces[0]["description"] != "BMC" {
		t.Fatalf("interfaces = %v, want idrac with the default description", ifaces)
	}
	ips := f.objects("ipam", "ip-addresses")
	if len(ips) != 1 {
		t.Fatalf("NetBox holds %d IPs, want 1", len(ips))
	}
	ip := ips[0]
	if ip["address"] != "10.0.10.1/24" || ip["status"] != "active" ||
		utils.GetIDFromObject(ip["vrf"]) != utils.GetIDFromObject(vrf) {
		t.Errorf("IP = %v, want the shorthand address with the default status and VRF", ip)
	}
	if got := utils.GetIDFromObject(f.objects("dcim", "devices")[0]["primary_ip4"]); got != utils.GetIDFromObject(ip) {
		t.Errorf("primary_ip4 = %d, want the idrac IP from the default address_role", got)
	}

	f.resetMutations()
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}
