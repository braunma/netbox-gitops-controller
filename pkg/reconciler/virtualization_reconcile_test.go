// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"reflect"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func TestReconcileClusterTypesCreateThenIdempotentThenUpdate(t *testing.T) {
	f, c := newFakeNetBox(t)
	vr := NewVirtualizationReconciler(c)

	types := []*models.ClusterType{{Name: "VMware vSphere", Slug: "vmware-vsphere", Description: "ESXi"}}

	if err := vr.ReconcileClusterTypes(types); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}
	muts := f.requireMutationCount(t, 1)
	if muts[0].method != "POST" || muts[0].path != "/api/virtualization/cluster-types/" {
		t.Errorf("expected POST /api/virtualization/cluster-types/, got %s %s", muts[0].method, muts[0].path)
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := vr.ReconcileClusterTypes(types); err != nil {
		t.Fatalf("ReconcileClusterTypes() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)

	// A changed field results in exactly one PATCH with only that field.
	types[0].Description = "vSphere 8"
	if err := vr.ReconcileClusterTypes(types); err != nil {
		t.Fatalf("ReconcileClusterTypes() third run error = %v", err)
	}
	muts = f.requireMutationCount(t, 1)
	want := map[string]interface{}{"description": "vSphere 8"}
	if muts[0].method != "PATCH" || !reflect.DeepEqual(muts[0].body, want) {
		t.Errorf("expected PATCH with %v, got %s %v", want, muts[0].method, muts[0].body)
	}
}

func TestReconcileClustersResolvesReferences(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	tenant := f.seed("tenancy", "tenants", client.Object{"name": "Acme Corp", "slug": "acme-corp"})

	vr := NewVirtualizationReconciler(c)

	// Type and group are created in this same run, just before the cluster;
	// they must resolve via the live lookup, not the (pre-run) cache.
	if err := vr.ReconcileClusterTypes([]*models.ClusterType{{Name: "Proxmox", Slug: "proxmox"}}); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}
	if err := vr.ReconcileClusterGroups([]*models.ClusterGroup{{Name: "Production", Slug: "production"}}); err != nil {
		t.Fatalf("ReconcileClusterGroups() error = %v", err)
	}

	clusters := []*models.Cluster{{
		Name:      "prod-cluster",
		TypeSlug:  "proxmox",
		GroupSlug: "production",
		SiteSlug:  "berlin-dc",
		Tenant:    "acme-corp",
		Status:    "active",
	}}
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() error = %v", err)
	}

	stored := f.objects("virtualization", "clusters")
	if len(stored) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(stored))
	}
	cl := stored[0]
	typeID := utils.GetIDFromObject(f.objects("virtualization", "cluster-types")[0])
	groupID := utils.GetIDFromObject(f.objects("virtualization", "cluster-groups")[0])
	if got := utils.GetIDFromObject(cl["type"]); got != typeID {
		t.Errorf("cluster type = %d, expected %d", got, typeID)
	}
	if got := utils.GetIDFromObject(cl["group"]); got != groupID {
		t.Errorf("cluster group = %d, expected %d", got, groupID)
	}
	// NetBox 4.2: the cluster site is carried in the generic scope.
	if st, _ := cl["scope_type"].(string); st != "dcim.site" {
		t.Errorf("cluster scope_type = %v, expected \"dcim.site\"", cl["scope_type"])
	}
	if got := utils.GetIDFromObject(cl["scope_id"]); got != utils.GetIDFromObject(site) {
		t.Errorf("cluster scope_id = %d, expected %d", got, utils.GetIDFromObject(site))
	}
	if got := utils.GetIDFromObject(cl["tenant"]); got != utils.GetIDFromObject(tenant) {
		t.Errorf("cluster tenant = %d, expected %d", got, utils.GetIDFromObject(tenant))
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileClustersSkipsUnknownType(t *testing.T) {
	f, c := newFakeNetBox(t)
	vr := NewVirtualizationReconciler(c)

	clusters := []*models.Cluster{{Name: "orphan", TypeSlug: "does-not-exist"}}
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() error = %v, expected unknown type to be skipped", err)
	}
	f.requireMutationCount(t, 0)
	if got := len(f.objects("virtualization", "clusters")); got != 0 {
		t.Errorf("expected no clusters created when the type is unknown, got %d", got)
	}
}

func TestReconcileVMsFullFlow(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	siteID := utils.GetIDFromObject(site)
	role := f.seed("dcim", "device-roles", client.Object{"name": "Server", "slug": "server"})
	platform := f.seed("dcim", "platforms", client.Object{"name": "Ubuntu 22.04", "slug": "ubuntu-22-04"})
	tenant := f.seed("tenancy", "tenants", client.Object{"name": "Acme Corp", "slug": "acme-corp"})
	clusterType := f.seed("virtualization", "cluster-types", client.Object{"name": "Proxmox", "slug": "proxmox"})
	cluster := f.seed("virtualization", "clusters", client.Object{
		"name": "prod-cluster", "type": utils.GetIDFromObject(clusterType), "site": siteID,
	})
	vlan := f.seed("ipam", "vlans", client.Object{"name": "mgmt", "vid": 100, "site": siteID})

	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	vms := []*models.VMConfig{{
		Name: "web-01", Cluster: "prod-cluster", RoleSlug: "server",
		Platform: "ubuntu-22-04", Tenant: "acme-corp", Status: "active",
		VCPUs: 4, Memory: 8192, Disk: 100,
		Interfaces: []models.VMInterfaceConfig{{
			Name: "eth0", MTU: 1500, Mode: "access",
			UntaggedVLAN: "mgmt",
			IP:           &models.IPConfig{Address: "10.0.0.10/24"},
			AddressRole:  "primary",
		}},
	}}
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}

	stored := f.objects("virtualization", "virtual-machines")
	if len(stored) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(stored))
	}
	vm := stored[0]
	if got := utils.GetIDFromObject(vm["cluster"]); got != utils.GetIDFromObject(cluster) {
		t.Errorf("VM cluster = %d, expected %d", got, utils.GetIDFromObject(cluster))
	}
	if got := utils.GetIDFromObject(vm["role"]); got != utils.GetIDFromObject(role) {
		t.Errorf("VM role = %d, expected %d", got, utils.GetIDFromObject(role))
	}
	if got := utils.GetIDFromObject(vm["platform"]); got != utils.GetIDFromObject(platform) {
		t.Errorf("VM platform = %d, expected %d", got, utils.GetIDFromObject(platform))
	}
	if got := utils.GetIDFromObject(vm["tenant"]); got != utils.GetIDFromObject(tenant) {
		t.Errorf("VM tenant = %d, expected %d", got, utils.GetIDFromObject(tenant))
	}
	if got := utils.GetIDFromObject(vm["vcpus"]); got != 4 {
		t.Errorf("VM vcpus = %v, expected 4", vm["vcpus"])
	}

	// Interface: site-scoped VLAN resolved via the cluster's site.
	ifaces := f.objects("virtualization", "interfaces")
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 VM interface, got %d", len(ifaces))
	}
	eth0 := ifaces[0]
	if got := utils.GetIDFromObject(eth0["virtual_machine"]); got != utils.GetIDFromObject(vm) {
		t.Errorf("interface virtual_machine = %d, expected %d", got, utils.GetIDFromObject(vm))
	}
	if got := utils.GetIDFromObject(eth0["untagged_vlan"]); got != utils.GetIDFromObject(vlan) {
		t.Errorf("interface untagged_vlan = %d, expected site-scoped VLAN %d", got, utils.GetIDFromObject(vlan))
	}

	// IP: bound to the VM interface and promoted to the VM's primary IP.
	ips := f.objects("ipam", "ip-addresses")
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP address, got %d", len(ips))
	}
	if ips[0]["assigned_object_type"] != "virtualization.vminterface" {
		t.Errorf("IP assigned_object_type = %v, expected virtualization.vminterface", ips[0]["assigned_object_type"])
	}
	if got := utils.GetIDFromObject(ips[0]["assigned_object_id"]); got != utils.GetIDFromObject(eth0) {
		t.Errorf("IP assigned_object_id = %d, expected interface ID %d", got, utils.GetIDFromObject(eth0))
	}
	if got := utils.GetIDFromObject(vm["primary_ip4"]); got != utils.GetIDFromObject(ips[0]) {
		t.Errorf("VM primary_ip4 = %v, expected IP ID %d", vm["primary_ip4"], utils.GetIDFromObject(ips[0]))
	}

	// Second run: everything diffs to a no-op. setVMPrimaryIP now skips the
	// PATCH when the VM already points at the IP, so an unchanged inventory
	// produces zero mutations (same behavior as the device reconciler).
	f.resetMutations()
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileVMsTaggedVLANsIdempotent(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	siteID := utils.GetIDFromObject(site)
	clusterType := f.seed("virtualization", "cluster-types", client.Object{"name": "KVM", "slug": "kvm"})
	f.seed("virtualization", "clusters", client.Object{
		"name": "c1", "type": utils.GetIDFromObject(clusterType), "site": siteID,
	})
	f.seed("ipam", "vlans", client.Object{"name": "red", "vid": 10, "site": siteID})
	f.seed("ipam", "vlans", client.Object{"name": "blue", "vid": 20, "site": siteID})

	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	vms := []*models.VMConfig{{
		Name: "trunk-vm", Cluster: "c1",
		Interfaces: []models.VMInterfaceConfig{{
			Name: "eth0", Mode: "tagged",
			TaggedVLANs: []string{"red", "blue"},
		}},
	}}
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}

	// No primary IP here, so a second run must be a complete no-op — proving
	// tagged_vlans are compared as an ID set, not re-PATCHed every run.
	f.resetMutations()
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileVMInterfaceResolvesParent(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	// eth0 is declared before its sub-interface, so the live parent lookup
	// finds it.
	vms := []*models.VMConfig{{
		Name: "vm1", SiteSlug: "berlin-dc",
		Interfaces: []models.VMInterfaceConfig{
			{Name: "eth0"},
			{Name: "eth0.100", Parent: "eth0"},
		},
	}}
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}

	var eth0ID, childParent int
	for _, iface := range f.objects("virtualization", "interfaces") {
		switch iface["name"] {
		case "eth0":
			eth0ID = utils.GetIDFromObject(iface)
		case "eth0.100":
			childParent = utils.GetIDFromObject(iface["parent"])
		}
	}
	if eth0ID == 0 || childParent != eth0ID {
		t.Errorf("eth0.100 parent = %d, expected eth0 ID %d", childParent, eth0ID)
	}
}

func TestReconcileVMIPWithVRF(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	vrf := f.seed("ipam", "vrfs", client.Object{"name": "prod"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	vms := []*models.VMConfig{{
		Name: "vm1", SiteSlug: "berlin-dc",
		Interfaces: []models.VMInterfaceConfig{{
			Name: "eth0",
			IP:   &models.IPConfig{Address: "10.0.0.5/24", VRF: "prod"},
		}},
	}}
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}

	ips := f.objects("ipam", "ip-addresses")
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP address, got %d", len(ips))
	}
	if got := utils.GetIDFromObject(ips[0]["vrf"]); got != utils.GetIDFromObject(vrf) {
		t.Errorf("IP vrf = %d, expected resolved VRF ID %d", got, utils.GetIDFromObject(vrf))
	}
}

func TestReconcileVMsSiteOnly(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	vms := []*models.VMConfig{{Name: "standalone", SiteSlug: "berlin-dc", Status: "active"}}
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}

	stored := f.objects("virtualization", "virtual-machines")
	if len(stored) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(stored))
	}
	if got := utils.GetIDFromObject(stored[0]["site"]); got != utils.GetIDFromObject(site) {
		t.Errorf("VM site = %d, expected %d", got, utils.GetIDFromObject(site))
	}
	if _, set := stored[0]["cluster"]; set {
		t.Errorf("site-only VM should have no cluster, got %v", stored[0]["cluster"])
	}
}

func TestReconcileVMsSkipsWhenNeitherClusterNorSiteResolves(t *testing.T) {
	f, c := newFakeNetBox(t)
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	vms := []*models.VMConfig{{Name: "lost", Cluster: "ghost-cluster"}}
	if err := vr.ReconcileVMs(vms); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}
	if got := len(f.objects("virtualization", "virtual-machines")); got != 0 {
		t.Errorf("expected no VM created when the cluster does not resolve, got %d", got)
	}
}

// TestReconcileVMsDryRunResolvesSameRunCluster reproduces a branch that adds a
// new cluster type, cluster and a VM in that cluster together, validated with
// --dry-run before merging. None exist in NetBox yet, so the VM used to be
// silently skipped; it must now be planned (resolved against the same-run
// cluster) while nothing is written to NetBox.
func TestReconcileVMsDryRunResolvesSameRunCluster(t *testing.T) {
	f, c := newFakeNetBox(t)
	c.SetDryRun(true)
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	vr := NewVirtualizationReconciler(c)
	if err := vr.ReconcileClusterTypes([]*models.ClusterType{{Name: "Proxmox", Slug: "proxmox"}}); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}
	if err := vr.ReconcileClusters([]*models.Cluster{{Name: "prod-cluster", TypeSlug: "proxmox"}}); err != nil {
		t.Fatalf("ReconcileClusters() error = %v", err)
	}
	if err := vr.ReconcileVMs([]*models.VMConfig{{Name: "web-01", Cluster: "prod-cluster", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileVMs() in dry-run = %v; a VM in a same-run cluster must validate, not be skipped silently", err)
	}

	// Dry-run plans the VM but writes nothing.
	if muts := f.mutationLog(); len(muts) != 0 {
		t.Errorf("dry-run sent %d mutating request(s), expected 0: %+v", len(muts), muts)
	}
	create := 0
	for _, ch := range c.Recorder().Changes() {
		if ch.Endpoint == "virtual-machines" && ch.Action == client.ActionCreate {
			create++
		}
	}
	if create != 1 {
		t.Errorf("expected the VM to be planned as 1 create, got %d", create)
	}
}

func TestReconcileClustersToleratesMissingOptionalRefs(t *testing.T) {
	f, c := newFakeNetBox(t)
	vr := NewVirtualizationReconciler(c)

	if err := vr.ReconcileClusterTypes([]*models.ClusterType{{Name: "KVM", Slug: "kvm"}}); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}

	// Group, site and tenant are all unknown; the cluster is still created
	// with only its required type.
	clusters := []*models.Cluster{{
		Name:      "minimal",
		TypeSlug:  "kvm",
		GroupSlug: "nope",
		SiteSlug:  "nope",
		Tenant:    "nope",
	}}
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() error = %v", err)
	}

	stored := f.objects("virtualization", "clusters")
	if len(stored) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(stored))
	}
	for _, field := range []string{"group", "scope_type", "scope_id", "tenant"} {
		if _, set := stored[0][field]; set {
			t.Errorf("cluster %s should be unset when the reference is unknown, got %v", field, stored[0][field])
		}
	}
}

// Since NetBox 4.2 a MAC address is an object assigned to the interface, and
// the interface's mac_address field is read-only. The declared MAC must be
// created as such an object and made the interface's primary — and never be
// sent in the interface payload, where NetBox drops it and every run would
// plan it again.
func TestReconcileVMInterfaceMACAddress(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	vr := NewVirtualizationReconciler(c)
	vms := func(mac string) []*models.VMConfig {
		return []*models.VMConfig{{
			Name: "vm1", SiteSlug: "berlin-dc",
			Interfaces: []models.VMInterfaceConfig{{Name: "eth0", MACAddress: mac}, {Name: "eth1"}},
		}}
	}
	primary := func() (ifaceID, macID int) {
		t.Helper()
		for _, iface := range f.objects("virtualization", "interfaces") {
			if iface["name"] == "eth0" {
				if _, sent := iface["mac_address"]; sent {
					t.Errorf("mac_address was sent in the interface payload: %v", iface["mac_address"])
				}
				return utils.GetIDFromObject(iface), utils.GetIDFromObject(iface["primary_mac_address"])
			}
		}
		t.Fatal("eth0 not found")
		return 0, 0
	}

	// A lower-case, dash-separated spelling is stored the way NetBox writes it.
	if err := vr.ReconcileVMs(vms("02-00-00-aa-bb-01")); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}
	macs := f.objects("dcim", "mac-addresses")
	if len(macs) != 1 {
		t.Fatalf("MAC address objects = %d, want 1 (eth1 declares none)", len(macs))
	}
	ifaceID, macID := primary()
	if macs[0]["mac_address"] != "02:00:00:AA:BB:01" {
		t.Errorf("mac_address = %v, want the canonical spelling", macs[0]["mac_address"])
	}
	if assigned, _ := macs[0]["assigned_object_id"].(float64); macs[0]["assigned_object_type"] != "virtualization.vminterface" || int(assigned) != ifaceID {
		t.Errorf("MAC address is assigned to %v %v, want the interface %d", macs[0]["assigned_object_type"], macs[0]["assigned_object_id"], ifaceID)
	}
	if macID != utils.GetIDFromObject(macs[0]) {
		t.Errorf("primary_mac_address = %d, want %d", macID, utils.GetIDFromObject(macs[0]))
	}

	// A second run, in either spelling, writes nothing.
	f.resetMutations()
	if err := vr.ReconcileVMs(vms("02:00:00:AA:BB:01")); err != nil {
		t.Fatal(err)
	}
	if len(f.mutations) != 0 {
		t.Errorf("a converged run wrote %d requests: %+v", len(f.mutations), f.mutations)
	}

	// A changed MAC becomes a second object and the new primary; the old one
	// is left for the prune.
	if err := vr.ReconcileVMs(vms("02:00:00:AA:BB:02")); err != nil {
		t.Fatal(err)
	}
	macs = f.objects("dcim", "mac-addresses")
	if len(macs) != 2 {
		t.Fatalf("MAC address objects = %d, want the old and the new", len(macs))
	}
	_, macID = primary()
	var newID int
	for _, m := range macs {
		if m["mac_address"] == "02:00:00:AA:BB:02" {
			newID = utils.GetIDFromObject(m)
		}
	}
	if newID == 0 || macID != newID {
		t.Errorf("primary_mac_address = %d, want the new MAC %d", macID, newID)
	}
}

func TestCanonicalMAC(t *testing.T) {
	for in, want := range map[string]string{
		"02:00:00:aa:bb:01": "02:00:00:AA:BB:01",
		"02-00-00-AA-BB-01": "02:00:00:AA:BB:01",
		"0200.00aa.bb01":    "02:00:00:AA:BB:01",
		" 020000aabb01 ":    "02:00:00:AA:BB:01",
		"not-a-mac":         "NOT-A-MAC",
	} {
		if got := canonicalMAC(in); got != want {
			t.Errorf("canonicalMAC(%q) = %q, want %q", in, got, want)
		}
	}
}
