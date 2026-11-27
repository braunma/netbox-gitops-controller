// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/export"
	"github.com/braunma/netbox-gitops-controller/pkg/lint"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// ref renders a related object the way NetBox nests it in a response.
func ref(o client.Object) map[string]interface{} {
	m := map[string]interface{}{"id": o["id"]}
	for _, k := range []string{"name", "slug", "model", "display"} {
		if v, ok := o[k]; ok {
			m[k] = v
		}
	}
	return m
}

func val(v string) map[string]interface{} { return map[string]interface{}{"value": v, "label": v} }

func tagRefs(tags ...client.Object) []interface{} {
	out := []interface{}{}
	for _, t := range tags {
		out = append(out, ref(t))
	}
	return out
}

// seedHandMadeInstance fills the fake with objects shaped like NetBox API
// responses, none of them carrying the managed tag: an instance populated by
// hand before GitOps arrived.
func seedHandMadeInstance(f *fakeNetBox) {
	prod := f.seed("extras", "tags", client.Object{"name": "Production", "slug": "prod", "color": "ff0000", "description": ""})
	backup := f.seed("extras", "tags", client.Object{"name": "Backup", "slug": "backup", "color": "00ff00", "description": "nightly"})

	f.seed("extras", "custom-fields", client.Object{"name": "vmid", "type": val("integer"),
		"object_types": []interface{}{"virtualization.virtualmachine"}, "label": "", "description": "", "required": false})
	f.seed("extras", "custom-fields", client.Object{"name": "owner", "type": val("text"),
		"object_types": []interface{}{"dcim.device"}, "label": "Owner", "description": "", "required": false})

	server := f.seed("dcim", "device-roles", client.Object{"name": "Server", "slug": "server", "color": "0000ff", "vm_role": true, "description": ""})
	switchRole := f.seed("dcim", "device-roles", client.Object{"name": "Switch", "slug": "switch", "color": "00ffff", "vm_role": false, "description": ""})
	ppRole := f.seed("dcim", "device-roles", client.Object{"name": "Patch Panel", "slug": "patch-panel", "color": "aaaaaa", "vm_role": false, "description": ""})

	dell := f.seed("dcim", "manufacturers", client.Object{"name": "Dell", "slug": "dell"})
	ubuntu := f.seed("dcim", "platforms", client.Object{"name": "Ubuntu 24.04", "slug": "ubuntu-24-04", "description": "", "tags": tagRefs()})
	tg := f.seed("tenancy", "tenant-groups", client.Object{"name": "Internal", "slug": "internal", "description": "", "tags": tagRefs()})
	tenant := f.seed("tenancy", "tenants", client.Object{"name": "Platform", "slug": "platform", "group": ref(tg), "description": "", "tags": tagRefs()})

	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc", "status": val("active"),
		"time_zone": "Europe/Berlin", "description": "", "comments": "", "tags": tagRefs(prod)})
	rack := f.seed("dcim", "racks", client.Object{"name": "Rack A01", "site": ref(site), "status": val("active"),
		"width": val("19"), "u_height": 42.0, "description": "", "tags": tagRefs()})
	rack["width"] = map[string]interface{}{"value": 19.0, "label": "19 inches"}

	vrf := f.seed("ipam", "vrfs", client.Object{"name": "prod", "rd": "65000:1", "description": "", "enforce_unique": true, "tags": tagRefs()})
	vg := f.seed("ipam", "vlan-groups", client.Object{"name": "Berlin VLANs", "slug": "berlin-vlans", "scope_type": "dcim.site",
		"scope_id": site["id"], "scope": ref(site), "vid_ranges": []interface{}{[]interface{}{1.0, 4094.0}}, "description": "", "tags": tagRefs()})
	mgmt := f.seed("ipam", "vlans", client.Object{"name": "mgmt", "vid": 100.0, "site": ref(site), "group": ref(vg),
		"status": val("active"), "description": "", "tags": tagRefs(prod)})
	data := f.seed("ipam", "vlans", client.Object{"name": "data", "vid": 200.0, "site": ref(site), "group": ref(vg),
		"status": val("active"), "description": "", "tags": tagRefs()})
	f.seed("ipam", "prefixes", client.Object{"prefix": "10.0.0.0/24", "scope_type": "dcim.site", "scope_id": site["id"], "scope": ref(site),
		"vrf": ref(vrf), "vlan": ref(mgmt), "status": val("active"), "is_pool": false, "description": "mgmt net", "tags": tagRefs()})

	// A server type with two NICs, a switch type, a patch panel type.
	r740 := f.seed("dcim", "device-types", client.Object{"model": "PowerEdge R740", "slug": "r740", "manufacturer": ref(dell),
		"u_height": 2.0, "is_full_depth": true, "part_number": "", "description": "", "comments": "", "tags": tagRefs()})
	for _, n := range []string{"eth0", "eth1", "eth2"} {
		f.seed("dcim", "interface-templates", client.Object{"device_type": ref(r740), "name": n, "type": val("10gbase-t"),
			"mgmt_only": false, "label": "", "description": ""})
	}
	f.seed("dcim", "module-bay-templates", client.Object{"device_type": ref(r740), "name": "PCIe 1", "label": "", "description": "", "position": "1"})
	sw := f.seed("dcim", "device-types", client.Object{"model": "S5248F", "slug": "s5248f", "manufacturer": ref(dell),
		"u_height": 1.0, "is_full_depth": true, "part_number": "", "description": "", "comments": "", "tags": tagRefs()})
	for _, n := range []string{"Eth1/1", "Eth1/2", "Eth1/10"} {
		f.seed("dcim", "interface-templates", client.Object{"device_type": ref(sw), "name": n, "type": val("25gbase-x-sfp28"),
			"mgmt_only": false, "label": "", "description": ""})
	}
	ppType := f.seed("dcim", "device-types", client.Object{"model": "PP-12", "slug": "pp-12", "manufacturer": ref(dell),
		"u_height": 1.0, "is_full_depth": false, "part_number": "", "description": "", "comments": "", "tags": tagRefs()})
	rpt := f.seed("dcim", "rear-port-templates", client.Object{"device_type": ref(ppType), "name": "R1", "type": val("mpo"),
		"positions": 12.0, "label": "", "description": "", "color": ""})
	f.seed("dcim", "front-port-templates", client.Object{"device_type": ref(ppType), "name": "1", "type": val("lc"),
		"rear_port": ref(rpt), "rear_port_position": 1.0, "label": "", "description": "", "color": ""})

	gpu := f.seed("dcim", "module-types", client.Object{"model": "A100 GPU", "manufacturer": ref(dell),
		"part_number": "", "description": "", "comments": "", "tags": tagRefs()})
	f.seed("dcim", "interface-templates", client.Object{"module_type": ref(gpu), "name": "gpu{module}", "type": val("other"),
		"mgmt_only": false, "label": "", "description": ""})

	// Devices.
	srv := f.seed("dcim", "devices", client.Object{"name": "srv-01", "site": ref(site), "role": ref(server),
		"device_type": ref(r740), "rack": ref(rack), "position": 10.0, "face": val("front"), "status": val("active"),
		"serial": "ABC123", "asset_tag": nil, "tags": tagRefs(prod, backup),
		"platform": ref(ubuntu), "custom_fields": map[string]interface{}{"owner": "team-a", "unused": nil}})
	swDev := f.seed("dcim", "devices", client.Object{"name": "sw-01", "site": ref(site), "role": ref(switchRole),
		"device_type": ref(sw), "rack": ref(rack), "position": 40.0, "face": val("rear"), "status": val("planned"),
		"serial": "", "tags": tagRefs()})
	pp := f.seed("dcim", "devices", client.Object{"name": "pp-01", "site": ref(site), "role": ref(ppRole),
		"device_type": ref(ppType), "rack": ref(rack), "position": 42.0, "face": val("front"), "status": val("active"),
		"serial": "", "tags": tagRefs()})

	iface := func(dev client.Object, name, typ string, extra client.Object) client.Object {
		o := client.Object{"device": ref(dev), "name": name, "type": val(typ), "enabled": true, "label": "",
			"description": "", "mtu": nil, "mode": nil, "untagged_vlan": nil, "tagged_vlans": []interface{}{},
			"lag": nil, "module": nil, "tags": tagRefs()}
		for k, v := range extra {
			o[k] = v
		}
		return f.seed("dcim", "interfaces", o)
	}
	srvEth0 := iface(srv, "eth0", "10gbase-t", client.Object{"mtu": 9000.0, "mode": val("access"), "untagged_vlan": ref(mgmt)})
	bond := iface(srv, "bond0", "lag", client.Object{"mode": val("tagged"), "tagged_vlans": []interface{}{ref(mgmt), ref(data)}})
	iface(srv, "eth1", "10gbase-t", client.Object{"lag": ref(bond), "tags": tagRefs(backup)})
	iface(srv, "eth2", "10gbase-t", client.Object{"lag": ref(bond)})
	swP1 := iface(swDev, "Eth1/1", "25gbase-x-sfp28", nil)
	swP2 := iface(swDev, "Eth1/2", "25gbase-x-sfp28", client.Object{"enabled": false, "description": "spare"})
	iface(swDev, "Eth1/10", "25gbase-x-sfp28", nil)

	ip := f.seed("ipam", "ip-addresses", client.Object{"address": "10.0.0.10/24", "vrf": ref(vrf), "status": val("active"),
		"dns_name": "srv-01.example.net", "description": "", "assigned_object_type": "dcim.interface",
		"assigned_object_id": srvEth0["id"], "family": val("4"), "tags": tagRefs()})
	srv["primary_ip4"] = ref(ip)
	// An address in the global table inside a VRF's prefix: declaring it
	// would make the sync create a copy in the VRF.
	f.seed("ipam", "ip-addresses", client.Object{"address": "10.0.0.20/24", "vrf": nil, "status": val("active"),
		"dns_name": "", "description": "", "assigned_object_type": "dcim.interface",
		"assigned_object_id": swP1["id"], "family": val("4"), "tags": tagRefs()})

	bay := f.seed("dcim", "module-bays", client.Object{"device": ref(srv), "name": "PCIe 1", "module": nil})
	f.seed("dcim", "modules", client.Object{"device": ref(srv), "module_bay": ref(bay), "module_type": ref(gpu),
		"status": val("active"), "serial": "GPU1", "asset_tag": nil, "description": "", "tags": tagRefs()})

	rp := f.seed("dcim", "rear-ports", client.Object{"device": ref(pp), "name": "R1", "type": val("mpo"),
		"positions": 12.0, "label": "", "description": "", "tags": tagRefs()})
	fp := f.seed("dcim", "front-ports", client.Object{"device": ref(pp), "name": "1", "type": val("lc"),
		"rear_port": ref(rp), "rear_port_position": 1.0, "label": "", "description": "", "tags": tagRefs()})

	cable := func(aType string, a client.Object, bType string, b client.Object, extra client.Object) {
		term := func(t string, o client.Object) []interface{} {
			obj := map[string]interface{}{"id": o["id"], "name": o["name"], "device": o["device"]}
			return []interface{}{map[string]interface{}{"object_type": t, "object_id": o["id"], "object": obj}}
		}
		c := client.Object{"a_terminations": term(aType, a), "b_terminations": term(bType, b),
			"status": val("connected"), "type": "", "color": "", "length": nil, "length_unit": nil, "tags": tagRefs()}
		for k, v := range extra {
			c[k] = v
		}
		c = f.seed("dcim", "cables", c)
		f.mu.Lock()
		f.setCableRefLocked(c, map[string]interface{}{"id": c["id"]})
		f.mu.Unlock()
	}
	// Switch side is A, so the exporter has to decide which end to write.
	cable("dcim.interface", swP1, "dcim.interface", srvEth0, client.Object{"type": "cat6a", "color": "0000ff",
		"length": 3.0, "length_unit": val("m")})
	cable("dcim.frontport", fp, "dcim.interface", swP2, nil)

	// Virtualization.
	ct := f.seed("virtualization", "cluster-types", client.Object{"name": "Proxmox", "slug": "proxmox", "description": "", "tags": tagRefs()})
	cl := f.seed("virtualization", "clusters", client.Object{"name": "pve-berlin", "type": ref(ct), "scope_type": "dcim.site",
		"scope_id": site["id"], "scope": ref(site), "status": val("active"), "description": "", "tenant": ref(tenant), "tags": tagRefs()})
	vm := f.seed("virtualization", "virtual-machines", client.Object{"name": "web-01", "cluster": ref(cl), "site": ref(site),
		"role": ref(server), "platform": ref(ubuntu), "tenant": ref(tenant), "status": val("active"),
		"vcpus": 4.0, "memory": 8192.0, "disk": 100.0, "tags": tagRefs(prod),
		"custom_fields": map[string]interface{}{"vmid": 101.0}})
	vmIf := f.seed("virtualization", "interfaces", client.Object{"virtual_machine": ref(vm), "name": "eth0", "enabled": true,
		"description": "", "mtu": nil, "mac_address": "AA:BB:CC:DD:EE:01", "mode": val("access"),
		"untagged_vlan": ref(data), "tagged_vlans": []interface{}{}, "parent": nil, "tags": tagRefs()})
	vmIP := f.seed("ipam", "ip-addresses", client.Object{"address": "10.0.1.5/24", "vrf": nil, "status": val("active"),
		"dns_name": "", "description": "", "assigned_object_type": "virtualization.vminterface",
		"assigned_object_id": vmIf["id"], "family": val("4"), "tags": tagRefs()})
	vm["primary_ip4"] = ref(vmIP)
}

// The promise `export` makes: merge what it writes, sync, and NetBox is
// exactly as it was, plus the managed tag. Nothing is created, no field other
// than tags changes, and no existing tag is lost.
func TestExportedYAMLAdoptsTheInstanceUnchanged(t *testing.T) {
	f, c := newFakeNetBox(t)
	seedHandMadeInstance(f)

	res, err := export.Export(c, export.Options{})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	root := t.TempDir()
	for _, file := range res.Files {
		p := filepath.Join(root, file.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, file.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ds, err := loader.NewDataLoader(root, utils.NewLogger(false)).LoadDataset(loader.DatasetOptions{})
	if err != nil {
		dumpFiles(t, res)
		t.Fatalf("exported YAML does not load: %v", err)
	}
	for _, finding := range lint.Check(ds, lint.Options{}) {
		if finding.Severity == lint.Error {
			dumpFiles(t, res)
			t.Errorf("exported YAML fails the checks: %s", finding)
		}
	}

	// Every object made it into the YAML.
	counts := map[string]int{
		"tags": len(ds.Tags), "roles": len(ds.Roles), "custom fields": len(ds.CustomFields),
		"sites": len(ds.Sites), "racks": len(ds.Racks), "vlans": len(ds.VLANs), "prefixes": len(ds.Prefixes),
		"device types": len(ds.DeviceTypes), "module types": len(ds.ModuleTypes),
		"devices": len(ds.Devices), "clusters": len(ds.Clusters), "vms": len(ds.VMs),
	}
	want := map[string]int{
		"tags": 2, "roles": 3, "custom fields": 2, "sites": 1, "racks": 1, "vlans": 2, "prefixes": 1,
		"device types": 3, "module types": 1, "devices": 3, "clusters": 1, "vms": 1,
	}
	for k, n := range want {
		if counts[k] != n {
			t.Errorf("exported %d %s, want %d", counts[k], k, n)
		}
	}

	if !hasWarning(res, "10.0.0.20/24 is in the global table") {
		t.Errorf("no warning about the global address left out; warnings: %v", res.Warnings)
	}

	f.resetMutations()
	syncDataset(t, c, ds)

	managed := c.ManagedTagID()
	for _, m := range f.mutationLog() {
		if m.method == "POST" {
			t.Errorf("sync created an object that already existed: POST %s %v", m.path, m.body)
			continue
		}
		if m.method != "PATCH" {
			t.Errorf("unexpected %s %s", m.method, m.path)
			continue
		}
		var keys []string
		for k := range m.body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != "tags" {
			t.Errorf("sync changed more than tags on %s: %v", m.path, m.body)
		}
	}

	// Every taggable object now carries the managed tag, and kept its own.
	for _, ep := range []string{"dcim/sites", "dcim/racks", "dcim/devices", "dcim/interfaces", "ipam/vlans",
		"ipam/prefixes", "ipam/ip-addresses", "dcim/cables", "virtualization/virtual-machines",
		"virtualization/interfaces", "virtualization/clusters", "dcim/modules"} {
		parts := strings.SplitN(ep, "/", 2)
		for _, o := range f.objects(parts[0], parts[1]) {
			ids := map[int]bool{}
			list, _ := o["tags"].([]interface{})
			for _, t := range list {
				ids[utils.GetIDFromObject(t)] = true
			}
			if (ep == "dcim/interfaces") && !ids[managed] {
				// Template interfaces the YAML does not list are left alone.
				continue
			}
			if o["address"] == "10.0.0.20/24" {
				if ids[managed] {
					t.Errorf("the global address inside a VRF prefix was declared; it must be left out")
				}
				continue
			}
			if !ids[managed] {
				t.Errorf("%s %v was not adopted (no managed tag)", ep, o["name"])
			}
		}
	}
	srv := findByName(f.objects("dcim", "devices"), "srv-01")
	if got := len(srv["tags"].([]interface{})); got != 3 {
		t.Errorf("srv-01 has %d tags after adoption, want prod, backup and the managed tag", got)
	}

	// A second export finds nothing left to do once the repository holds the
	// first one.
	again, err := export.Export(c, export.Options{Declared: &ds})
	if err != nil {
		t.Fatalf("second Export() error = %v", err)
	}
	if len(again.Files) != 0 {
		t.Errorf("second export wrote %d files, want none: %v", len(again.Files), again.Files[0].Path)
	}
}

func hasWarning(res *export.Result, substr string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func findByName(objs []client.Object, name string) client.Object {
	for _, o := range objs {
		if o["name"] == name {
			return o
		}
	}
	return nil
}

func dumpFiles(t *testing.T, res *export.Result) {
	t.Helper()
	for _, f := range res.Files {
		t.Logf("--- %s\n%s", f.Path, f.Data)
	}
	for _, w := range res.Warnings {
		t.Logf("warning: %s", w)
	}
}

// syncDataset runs the phases in the order the sync command does.
func syncDataset(t *testing.T, c *client.NetBoxClient, ds loader.Dataset) {
	t.Helper()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must("LoadGlobal", c.Cache().LoadGlobal())
	fr := NewFoundationReconciler(c)
	must("tags", fr.ReconcileTags(ds.Tags))
	must("roles", fr.ReconcileRoles(ds.Roles))
	must("custom fields", fr.ReconcileCustomFields(ds.CustomFields))
	must("platforms", fr.ReconcilePlatforms(ds.Platforms))
	must("tenant groups", fr.ReconcileTenantGroups(ds.TenantGroups))
	must("tenants", fr.ReconcileTenants(ds.Tenants))
	must("sites", fr.ReconcileSites(ds.Sites))
	must("racks", fr.ReconcileRacks(ds.Racks))
	nr := NewNetworkReconciler(c)
	must("vrfs", nr.ReconcileVRFs(ds.VRFs))
	must("vlan groups", nr.ReconcileVLANGroups(ds.VLANGroups))
	must("vlans", nr.ReconcileVLANs(ds.VLANs))
	must("prefixes", nr.ReconcilePrefixes(ds.Prefixes))
	dtr := NewDeviceTypeReconciler(c)
	must("module types", dtr.ReconcileModuleTypes(ds.ModuleTypes))
	must("device types", dtr.ReconcileDeviceTypes(ds.DeviceTypes))
	for _, s := range ds.Sites {
		must("LoadSite", c.Cache().LoadSite(s.Slug))
	}
	dr := NewDeviceReconciler(c)
	dr.SetDeviceTypes(ds.DeviceTypes)
	must("devices", dr.ReconcileDevices(ds.Devices))
	vr := NewVirtualizationReconciler(c)
	must("cluster types", vr.ReconcileClusterTypes(ds.ClusterTypes))
	must("cluster groups", vr.ReconcileClusterGroups(ds.ClusterGroups))
	must("clusters", vr.ReconcileClusters(ds.Clusters))
	must("vms", vr.ReconcileVMs(ds.VMs))
}
