package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// managedTags builds the tag reference a managed object carries, using the
// client's live managed tag ID so the fake's ?tag=gitops filter matches it.
func managedTags(c *client.NetBoxClient) []interface{} {
	return []interface{}{map[string]interface{}{
		"id": float64(c.ManagedTagID()), "slug": "gitops",
	}}
}

// TestPruneEndToEnd exercises the real reconcile→prune flow against the fake
// NetBox: objects declared in YAML are reconciled (and marked seen), a
// previously-managed object that is no longer declared is pruned, and both a
// still-declared object and an unmanaged (untagged) object survive.
func TestPruneEndToEnd(t *testing.T) {
	f, c := newFakeNetBox(t)

	// A site that used to be managed but is no longer in the desired set.
	f.seed("dcim", "sites", client.Object{
		"name": "Old DC", "slug": "old-dc", "status": "active",
		"tags": managedTags(c),
	})
	// A site created by hand in NetBox: no managed tag, must be protected.
	f.seed("dcim", "sites", client.Object{
		"name": "Manual DC", "slug": "manual-dc", "status": "active",
	})

	// Reconcile the desired set: only berlin-dc remains declared.
	fr := NewFoundationReconciler(c)
	desired := []*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Status: "active"}}
	if err := fr.ReconcileSites(desired); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}

	if err := c.Prune([]client.PruneTarget{{App: "dcim", Endpoint: "sites"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	// old-dc (managed orphan) is gone; berlin-dc (declared) and manual-dc
	// (unmanaged) survive.
	got := map[string]bool{}
	for _, obj := range f.objects("dcim", "sites") {
		got[obj["slug"].(string)] = true
	}
	if got["old-dc"] {
		t.Errorf("managed orphan old-dc was not pruned: %v", got)
	}
	if !got["berlin-dc"] {
		t.Errorf("declared site berlin-dc was wrongly pruned: %v", got)
	}
	if !got["manual-dc"] {
		t.Errorf("unmanaged site manual-dc was wrongly pruned: %v", got)
	}

	if summary := c.Recorder().Summary(); summary.Delete != 1 {
		t.Errorf("recorder Delete = %d, expected exactly 1 (old-dc)", summary.Delete)
	}
}

// TestPruneVirtualMachinesEndToEnd verifies that an orphaned managed VM is
// pruned while a still-declared VM and an unmanaged (untagged) VM survive.
func TestPruneVirtualMachinesEndToEnd(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	// A managed VM left over from a previous run, no longer declared.
	f.seed("virtualization", "virtual-machines", client.Object{
		"name": "old-vm", "tags": managedTags(c),
	})
	// A hand-created VM with no managed tag, which must be protected.
	f.seed("virtualization", "virtual-machines", client.Object{"name": "manual-vm"})

	// Reconcile the desired set: only keep-vm remains declared.
	vr := NewVirtualizationReconciler(c)
	desired := []*models.VMConfig{{Name: "keep-vm", SiteSlug: "berlin-dc", Status: "active"}}
	if err := vr.ReconcileVMs(desired); err != nil {
		t.Fatalf("ReconcileVMs() error = %v", err)
	}

	if err := c.Prune([]client.PruneTarget{{App: "virtualization", Endpoint: "virtual-machines"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	got := map[string]bool{}
	for _, obj := range f.objects("virtualization", "virtual-machines") {
		got[obj["name"].(string)] = true
	}
	if got["old-vm"] {
		t.Errorf("managed orphan old-vm was not pruned: %v", got)
	}
	if !got["keep-vm"] {
		t.Errorf("declared VM keep-vm was wrongly pruned: %v", got)
	}
	if !got["manual-vm"] {
		t.Errorf("unmanaged VM manual-vm was wrongly pruned: %v", got)
	}
}

// TestPrunePlatformsAndTenantsEndToEnd verifies that orphaned managed
// platforms and tenants are pruned while still-declared ones survive.
func TestPrunePlatformsAndTenantsEndToEnd(t *testing.T) {
	f, c := newFakeNetBox(t)
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	f.seed("dcim", "platforms", client.Object{"name": "Old OS", "slug": "old-os", "tags": managedTags(c)})
	f.seed("tenancy", "tenants", client.Object{"name": "Old Tenant", "slug": "old-tenant", "tags": managedTags(c)})

	fr := NewFoundationReconciler(c)
	if err := fr.ReconcilePlatforms([]*models.Platform{{Name: "Ubuntu", Slug: "ubuntu"}}); err != nil {
		t.Fatalf("ReconcilePlatforms() error = %v", err)
	}
	if err := fr.ReconcileTenants([]*models.Tenant{{Name: "Acme", Slug: "acme"}}); err != nil {
		t.Fatalf("ReconcileTenants() error = %v", err)
	}

	err := c.Prune([]client.PruneTarget{
		{App: "dcim", Endpoint: "platforms"},
		{App: "tenancy", Endpoint: "tenants"},
	})
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	platforms := map[string]bool{}
	for _, o := range f.objects("dcim", "platforms") {
		platforms[o["slug"].(string)] = true
	}
	if platforms["old-os"] {
		t.Errorf("orphaned managed platform old-os was not pruned: %v", platforms)
	}
	if !platforms["ubuntu"] {
		t.Errorf("declared platform ubuntu was wrongly pruned: %v", platforms)
	}

	tenants := map[string]bool{}
	for _, o := range f.objects("tenancy", "tenants") {
		tenants[o["slug"].(string)] = true
	}
	if tenants["old-tenant"] {
		t.Errorf("orphaned managed tenant old-tenant was not pruned: %v", tenants)
	}
	if !tenants["acme"] {
		t.Errorf("declared tenant acme was wrongly pruned: %v", tenants)
	}
}

// TestPruneDeviceChildrenEndToEnd verifies that orphaned device children
// (interfaces and IP addresses) are pruned while the still-declared child and
// an unmanaged (untagged) child are protected. This exercises the seen-wiring
// for nested components created through Apply during device reconciliation.
func TestPruneDeviceChildrenEndToEnd(t *testing.T) {
	f, c := newFakeNetBox(t)
	seedDeviceFoundation(t, f, c)
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	// Reconcile a device declaring a single interface with an IP. Both go
	// through Apply, so both are tagged and marked seen.
	devices := []*models.DeviceConfig{{
		Name: "sw-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
		Interfaces: []models.InterfaceConfig{{
			Name: "eth0", Type: "1000base-t",
			IP: &models.IPConfig{Address: "10.0.0.1/24"},
		}},
	}}
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	deviceID := utils.GetIDFromObject(f.objects("dcim", "devices")[0])

	// Seed children that look like leftovers from a previous run: managed
	// (tagged) but not declared this run, so not seen.
	f.seed("dcim", "interfaces", client.Object{
		"name": "eth1", "device": float64(deviceID), "tags": managedTags(c),
	})
	f.seed("ipam", "ip-addresses", client.Object{
		"address": "10.0.0.99/24", "tags": managedTags(c),
	})
	// And an unmanaged interface (no tag) that must never be pruned.
	f.seed("dcim", "interfaces", client.Object{
		"name": "mgmt0", "device": float64(deviceID),
	})

	err := c.Prune([]client.PruneTarget{
		{App: "ipam", Endpoint: "ip-addresses"},
		{App: "dcim", Endpoint: "interfaces"},
	})
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	ifaces := map[string]bool{}
	for _, o := range f.objects("dcim", "interfaces") {
		ifaces[o["name"].(string)] = true
	}
	if ifaces["eth1"] {
		t.Errorf("orphaned managed interface eth1 was not pruned: %v", ifaces)
	}
	if !ifaces["eth0"] {
		t.Errorf("declared interface eth0 was wrongly pruned: %v", ifaces)
	}
	if !ifaces["mgmt0"] {
		t.Errorf("unmanaged interface mgmt0 was wrongly pruned: %v", ifaces)
	}

	addrs := map[string]bool{}
	for _, o := range f.objects("ipam", "ip-addresses") {
		addrs[o["address"].(string)] = true
	}
	if addrs["10.0.0.99/24"] {
		t.Errorf("orphaned managed IP was not pruned: %v", addrs)
	}
	if !addrs["10.0.0.1/24"] {
		t.Errorf("declared IP was wrongly pruned: %v", addrs)
	}
}

// TestPruneKeepsStillReferencedObject covers a hazard found against a real
// NetBox: removing a site from YAML while a VLAN still declares it made the
// site look like an orphan. Deleting it was either refused by NetBox with a
// 409 — after other objects had already been deleted — or, for a nullable
// foreign key, would have silently unlinked the referring object.
func TestPruneKeepsStillReferencedObject(t *testing.T) {
	f, c := newFakeNetBox(t)

	// A site that is no longer declared, but that a declared VLAN points at.
	site := f.seed("dcim", "sites", client.Object{
		"name": "Test Lab", "slug": "test-lab", "status": "active",
		"tags": managedTags(c),
	})
	// A genuinely orphaned site nothing points at.
	f.seed("dcim", "sites", client.Object{
		"name": "Abandoned", "slug": "abandoned", "status": "active",
		"tags": managedTags(c),
	})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	// Reconciling the VLAN records a reference to the site.
	vlans := []*models.VLAN{{Name: "Lab VLAN", VID: 100, SiteSlug: "test-lab"}}
	if err := NewNetworkReconciler(c).ReconcileVLANs(vlans); err != nil {
		t.Fatalf("ReconcileVLANs() error = %v", err)
	}

	if err := c.Prune([]client.PruneTarget{{App: "dcim", Endpoint: "sites"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	slugs := map[string]bool{}
	for _, s := range f.objects("dcim", "sites") {
		slug, _ := s["slug"].(string)
		slugs[slug] = true
	}
	if !slugs["test-lab"] {
		t.Errorf("the referenced site was pruned; it is still in use by a declared VLAN (id %d)",
			utils.GetIDFromObject(site))
	}
	if slugs["abandoned"] {
		t.Error("a genuinely orphaned site survived; the reference check must not keep everything")
	}
}
