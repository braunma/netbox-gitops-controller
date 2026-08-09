// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// A rename must edit the object in place. The failure this guards against is
// not an error but a silent success: without rename_from the old identity
// simply stops matching, so a second object is created and the original is left
// behind holding every reference that pointed at it.
func TestRenameEditsInPlaceRatherThanDuplicating(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	sites := []*models.Site{{Name: "Frankfurt", Slug: "frankfurt", Status: "active"}}
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	original := f.objects("dcim", "sites")
	if len(original) != 1 {
		t.Fatalf("expected 1 site, got %d", len(original))
	}
	originalID := utils.GetIDFromObject(original[0])

	// The slug was a typo. Correct it and declare where it came from.
	renamed := []*models.Site{{Name: "Frankfurt", Slug: "frankfurt-dc1", RenameFrom: "frankfurt", Status: "active"}}
	if err := fr.ReconcileSites(renamed); err != nil {
		t.Fatalf("ReconcileSites() after rename error = %v", err)
	}

	after := f.objects("dcim", "sites")
	if len(after) != 1 {
		t.Fatalf("rename created a duplicate: %d sites, want 1: %+v", len(after), after)
	}
	if got := utils.GetIDFromObject(after[0]); got != originalID {
		t.Errorf("renamed site has ID %d, want the original %d — it was recreated, not renamed", got, originalID)
	}
	if after[0]["slug"] != "frankfurt-dc1" {
		t.Errorf("slug = %v, want frankfurt-dc1", after[0]["slug"])
	}

	// Once NetBox holds the new slug, the declaration is inert: it must not
	// keep searching for, or resurrect, the old identity.
	f.resetMutations()
	if err := fr.ReconcileSites(renamed); err != nil {
		t.Fatalf("ReconcileSites() third run error = %v", err)
	}
	f.requireMutationCount(t, 0)

	// And dropping rename_from, as the docs tell the user to, changes nothing.
	f.resetMutations()
	if err := fr.ReconcileSites([]*models.Site{{Name: "Frankfurt", Slug: "frankfurt-dc1", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() after dropping rename_from error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

// Racks are the case the identity audit flagged: NetBox gives them no slug, so
// the name *is* the identity and correcting a typo in it is a rename.
func TestRenameRackByName(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{{Name: "Frankfurt", Slug: "frankfurt", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if err := fr.ReconcileRacks([]*models.Rack{
		{Name: "Rakc 01", Slug: "rack-01", SiteSlug: "frankfurt", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileRacks() error = %v", err)
	}
	originalID := utils.GetIDFromObject(f.objects("dcim", "racks")[0])

	if err := fr.ReconcileRacks([]*models.Rack{
		{Name: "Rack 01", Slug: "rack-01", SiteSlug: "frankfurt", RenameFrom: "Rakc 01", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileRacks() after rename error = %v", err)
	}

	racks := f.objects("dcim", "racks")
	if len(racks) != 1 {
		t.Fatalf("rename created a duplicate rack: %d, want 1: %+v", len(racks), racks)
	}
	if got := utils.GetIDFromObject(racks[0]); got != originalID {
		t.Errorf("rack ID %d, want the original %d", got, originalID)
	}
	if racks[0]["name"] != "Rack 01" {
		t.Errorf("name = %v, want \"Rack 01\"", racks[0]["name"])
	}
}

// rename_from names an object's past, which is weaker evidence than a plain
// identity match: a typo in it can name a real object that has nothing to do
// with the declaration. Objects this tool does not manage are therefore off
// limits, and the refusal has to be an error rather than a silent skip.
func TestRenameRefusesUnmanagedObject(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	// A site somebody else created: no gitops tag.
	f.seed("dcim", "sites", map[string]interface{}{
		"name": "Production", "slug": "production", "status": "active",
	})

	err := fr.ReconcileSites([]*models.Site{
		{Name: "Frankfurt", Slug: "frankfurt", RenameFrom: "production", Status: "active"},
	})
	if err == nil {
		t.Fatal("renaming an unmanaged object was allowed")
	}
	if !strings.Contains(err.Error(), "not managed") {
		t.Errorf("error = %q, want it to explain the object is not managed", err)
	}

	// The unmanaged object must be untouched, and no replacement created behind it.
	sites := f.objects("dcim", "sites")
	if len(sites) != 1 || sites[0]["slug"] != "production" {
		t.Errorf("unmanaged site was modified or a site was created: %+v", sites)
	}
}

// If both identities exist, the tool cannot know which one the user means to
// keep, and merging them is not its call. It must leave both alone.
func TestRenameLeavesBothWhenNewIdentityAlreadyExists(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{
		{Name: "Old", Slug: "old-site", Status: "active"},
		{Name: "New", Slug: "new-site", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	byslug := func(slug string) int {
		for _, s := range f.objects("dcim", "sites") {
			if s["slug"] == slug {
				return utils.GetIDFromObject(s)
			}
		}
		return 0
	}
	oldID, newID := byslug("old-site"), byslug("new-site")

	if err := fr.ReconcileSites([]*models.Site{
		{Name: "New", Slug: "new-site", RenameFrom: "old-site", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}

	if len(f.objects("dcim", "sites")) != 2 {
		t.Errorf("expected both sites to survive, got %+v", f.objects("dcim", "sites"))
	}
	if byslug("old-site") != oldID || byslug("new-site") != newID {
		t.Error("an ambiguous rename modified one of the two sites")
	}
}

// A rename_from that matches nothing is not an error: it is what a declaration
// looks like after it has been applied and before the user removes it.
func TestRenameFromUnknownIdentityCreatesNormally(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{
		{Name: "Frankfurt", Slug: "frankfurt", RenameFrom: "never-existed", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	sites := f.objects("dcim", "sites")
	if len(sites) != 1 || sites[0]["slug"] != "frankfurt" {
		t.Errorf("expected a normal create, got %+v", sites)
	}
}

// A device is identified by name within its site, and renaming one must not
// disturb what hangs off it.
func TestRenameDeviceKeepsItsInterfaces(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)
	dtr := NewDeviceTypeReconciler(c)
	dr := NewDeviceReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{{Name: "FRA", Slug: "fra", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if err := fr.ReconcileRoles([]*models.Role{{Name: "Compute", Slug: "compute", Color: "00ff00"}}); err != nil {
		t.Fatalf("ReconcileRoles() error = %v", err)
	}
	if err := dtr.ReconcileDeviceTypes([]*models.DeviceType{{
		Model: "R660", Slug: "r660", Manufacturer: "Dell", UHeight: 1,
	}}); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	device := func(name, renameFrom string) []*models.DeviceConfig {
		return []*models.DeviceConfig{{
			Name: name, RenameFrom: renameFrom, SiteSlug: "fra",
			DeviceTypeSlug: "r660", RoleSlug: "compute", Status: "active",
			Interfaces: []models.InterfaceConfig{{Name: "eth0", Type: "1000base-t"}},
		}}
	}
	if err := dr.ReconcileDevices(device("srv-01x", "")); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}
	originalID := utils.GetIDFromObject(f.objects("dcim", "devices")[0])
	ifaceCount := len(f.objects("dcim", "interfaces"))

	if err := dr.ReconcileDevices(device("srv-01", "srv-01x")); err != nil {
		t.Fatalf("ReconcileDevices() after rename error = %v", err)
	}

	devices := f.objects("dcim", "devices")
	if len(devices) != 1 {
		t.Fatalf("rename created a duplicate device: %+v", devices)
	}
	if utils.GetIDFromObject(devices[0]) != originalID {
		t.Error("device was recreated rather than renamed")
	}
	if devices[0]["name"] != "srv-01" {
		t.Errorf("name = %v, want srv-01", devices[0]["name"])
	}
	// The interfaces belonged to the device by ID, so a real rename leaves the
	// count alone; a recreate would have produced a second set.
	if got := len(f.objects("dcim", "interfaces")); got != ifaceCount {
		t.Errorf("interfaces = %d, want %d — the device was recreated", got, ifaceCount)
	}
}

// An interface is identified by name within its device, so a typo there is a
// rename too — and the IP bound to it must come along rather than be orphaned.
func TestRenameDeviceInterface(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)
	dtr := NewDeviceTypeReconciler(c)
	dr := NewDeviceReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{{Name: "FRA", Slug: "fra", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if err := fr.ReconcileRoles([]*models.Role{{Name: "Compute", Slug: "compute", Color: "00ff00"}}); err != nil {
		t.Fatalf("ReconcileRoles() error = %v", err)
	}
	if err := dtr.ReconcileDeviceTypes([]*models.DeviceType{{
		Model: "R660", Slug: "r660", Manufacturer: "Dell", UHeight: 1,
	}}); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	device := func(ifName, renameFrom string) []*models.DeviceConfig {
		return []*models.DeviceConfig{{
			Name: "srv-01", SiteSlug: "fra", DeviceTypeSlug: "r660",
			RoleSlug: "compute", Status: "active",
			Interfaces: []models.InterfaceConfig{
				{Name: ifName, RenameFrom: renameFrom, Type: "1000base-t"},
			},
		}}
	}
	if err := dr.ReconcileDevices(device("etho0", "")); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}
	originalID := utils.GetIDFromObject(f.objects("dcim", "interfaces")[0])

	if err := dr.ReconcileDevices(device("eth0", "etho0")); err != nil {
		t.Fatalf("ReconcileDevices() after rename error = %v", err)
	}

	ifaces := f.objects("dcim", "interfaces")
	if len(ifaces) != 1 {
		t.Fatalf("rename created a duplicate interface: %+v", ifaces)
	}
	if utils.GetIDFromObject(ifaces[0]) != originalID {
		t.Error("interface was recreated rather than renamed")
	}
	if ifaces[0]["name"] != "eth0" {
		t.Errorf("name = %v, want eth0", ifaces[0]["name"])
	}
}

// A VLAN is identified by its VID, not its name, so the two cases differ:
// correcting the name needs no declaration at all, while correcting the VID
// does.
func TestRenameVLANByVID(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)
	nr := NewNetworkReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{{Name: "FRA", Slug: "fra", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if err := nr.ReconcileVLANs([]*models.VLAN{
		{Name: "Storage", VID: 201, SiteSlug: "fra", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileVLANs() error = %v", err)
	}
	originalID := utils.GetIDFromObject(f.objects("ipam", "vlans")[0])

	// Renaming only the name: the VID still matches, so no declaration needed.
	if err := nr.ReconcileVLANs([]*models.VLAN{
		{Name: "Storage Net", VID: 201, SiteSlug: "fra", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileVLANs() error = %v", err)
	}
	if vlans := f.objects("ipam", "vlans"); len(vlans) != 1 || vlans[0]["name"] != "Storage Net" {
		t.Fatalf("renaming a VLAN's name should need no rename_from, got %+v", vlans)
	}

	// Correcting the VID is the identity change.
	if err := nr.ReconcileVLANs([]*models.VLAN{
		{Name: "Storage Net", VID: 210, SiteSlug: "fra", RenameFrom: "201", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileVLANs() after VID change error = %v", err)
	}
	vlans := f.objects("ipam", "vlans")
	if len(vlans) != 1 {
		t.Fatalf("changing a VID created a duplicate VLAN: %+v", vlans)
	}
	if utils.GetIDFromObject(vlans[0]) != originalID {
		t.Error("VLAN was recreated rather than renumbered")
	}
	if utils.GetIDFromObject(vlans[0]["vid"]) != 210 {
		t.Errorf("vid = %v, want 210", vlans[0]["vid"])
	}
}

// In --dry-run a rename must be reported and nothing written.
func TestRenameIsPlannedNotWrittenInDryRun(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{{Name: "Frankfurt", Slug: "frankfurt", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}

	c.SetDryRun(true)
	f.resetMutations()
	if err := fr.ReconcileSites([]*models.Site{
		{Name: "Frankfurt", Slug: "frankfurt-dc1", RenameFrom: "frankfurt", Status: "active"},
	}); err != nil {
		t.Fatalf("ReconcileSites() dry-run error = %v", err)
	}
	f.requireMutationCount(t, 0)

	sites := f.objects("dcim", "sites")
	if len(sites) != 1 || sites[0]["slug"] != "frankfurt" {
		t.Errorf("a dry-run rename changed NetBox: %+v", sites)
	}
}
