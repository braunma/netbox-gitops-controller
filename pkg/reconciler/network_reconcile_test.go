package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// singleVIDRange extracts a lone [min, max] pair from a stored vid_ranges value
// (a list of two-element arrays, JSON-decoded to float64 by the fake server).
func singleVIDRange(v interface{}) (lo, hi int, ok bool) {
	list, isList := v.([]interface{})
	if !isList || len(list) != 1 {
		return 0, 0, false
	}
	pair, isPair := list[0].([]interface{})
	if !isPair || len(pair) != 2 {
		return 0, 0, false
	}
	return utils.GetIDFromObject(pair[0]), utils.GetIDFromObject(pair[1]), true
}

func TestReconcileVRFsCreateThenIdempotent(t *testing.T) {
	f, c := newFakeNetBox(t)
	nr := NewNetworkReconciler(c)

	vrfs := []*models.VRF{{Name: "prod", RD: "65000:100", Description: "production"}}
	if err := nr.ReconcileVRFs(vrfs); err != nil {
		t.Fatalf("ReconcileVRFs() error = %v", err)
	}

	stored := f.objects("ipam", "vrfs")
	if len(stored) != 1 {
		t.Fatalf("expected 1 VRF in store, got %d", len(stored))
	}
	if stored[0]["rd"] != "65000:100" {
		t.Errorf("VRF rd = %v, expected \"65000:100\"", stored[0]["rd"])
	}

	f.resetMutations()
	if err := nr.ReconcileVRFs(vrfs); err != nil {
		t.Fatalf("ReconcileVRFs() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileVLANGroupsResolvesSiteFromCache(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	nr := NewNetworkReconciler(c)
	groups := []*models.VLANGroup{{
		Name:     "Fabric",
		Slug:     "fabric",
		SiteSlug: "berlin-dc",
		MinVID:   100,
		MaxVID:   199,
	}}
	if err := nr.ReconcileVLANGroups(groups); err != nil {
		t.Fatalf("ReconcileVLANGroups() error = %v", err)
	}

	stored := f.objects("ipam", "vlan-groups")
	if len(stored) != 1 {
		t.Fatalf("expected 1 VLAN group in store, got %d", len(stored))
	}
	// NetBox 4.2: the site is carried in the generic scope, not a `site` field.
	if st, _ := stored[0]["scope_type"].(string); st != "dcim.site" {
		t.Errorf("VLAN group scope_type = %v, expected \"dcim.site\"", stored[0]["scope_type"])
	}
	if got := utils.GetIDFromObject(stored[0]["scope_id"]); got != utils.GetIDFromObject(site) {
		t.Errorf("VLAN group scope_id = %d, expected cached site ID %d", got, utils.GetIDFromObject(site))
	}
	if lo, hi, ok := singleVIDRange(stored[0]["vid_ranges"]); !ok || lo != 100 || hi != 199 {
		t.Errorf("VLAN group vid_ranges = %v, expected single range [100, 199]", stored[0]["vid_ranges"])
	}

	// Idempotency: a second run against the just-written object must change
	// nothing — the scope and vid_ranges must round-trip cleanly.
	f.resetMutations()
	if err := nr.ReconcileVLANGroups(groups); err != nil {
		t.Fatalf("ReconcileVLANGroups() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileVLANsResolvesSiteAndGroup(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	siteID := utils.GetIDFromObject(site)
	group := f.seed("ipam", "vlan-groups", client.Object{
		"name": "Fabric", "slug": "fabric", "site": siteID,
	})

	// VLAN group resolution goes through the site-scoped cache.
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	nr := NewNetworkReconciler(c)
	vlans := []*models.VLAN{{
		Name:      "mgmt",
		VID:       100,
		SiteSlug:  "berlin-dc",
		GroupSlug: "fabric",
		Status:    "active",
	}}
	if err := nr.ReconcileVLANs(vlans); err != nil {
		t.Fatalf("ReconcileVLANs() error = %v", err)
	}

	stored := f.objects("ipam", "vlans")
	if len(stored) != 1 {
		t.Fatalf("expected 1 VLAN in store, got %d", len(stored))
	}
	if got := utils.GetIDFromObject(stored[0]["site"]); got != siteID {
		t.Errorf("VLAN site = %d, expected %d", got, siteID)
	}
	if got := utils.GetIDFromObject(stored[0]["group"]); got != utils.GetIDFromObject(group) {
		t.Errorf("VLAN group = %d, expected cached group ID %d", got, utils.GetIDFromObject(group))
	}
	if got := utils.GetIDFromObject(stored[0]["vid"]); got != 100 {
		t.Errorf("VLAN vid = %v, expected 100", stored[0]["vid"])
	}

	// Idempotency: the lookup is site_id + vid, so a second run must
	// find the VLAN just created and change nothing.
	f.resetMutations()
	if err := nr.ReconcileVLANs(vlans); err != nil {
		t.Fatalf("ReconcileVLANs() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileVLANsSkipsUnknownSite(t *testing.T) {
	f, c := newFakeNetBox(t)
	nr := NewNetworkReconciler(c)

	vlans := []*models.VLAN{{Name: "mgmt", VID: 100, SiteSlug: "does-not-exist", Status: "active"}}
	if err := nr.ReconcileVLANs(vlans); err != nil {
		t.Fatalf("ReconcileVLANs() error = %v, expected unknown site to be skipped without error", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcilePrefixesResolvesReferences(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	siteID := utils.GetIDFromObject(site)
	vrf := f.seed("ipam", "vrfs", client.Object{"name": "prod"})
	vlan := f.seed("ipam", "vlans", client.Object{"name": "mgmt", "vid": 100, "site": siteID})

	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	nr := NewNetworkReconciler(c)
	prefixes := []*models.Prefix{{
		Prefix:   "10.0.0.0/24",
		SiteSlug: "berlin-dc",
		VRFName:  "prod",
		VLANName: "mgmt",
		Status:   "active",
	}}
	if err := nr.ReconcilePrefixes(prefixes); err != nil {
		t.Fatalf("ReconcilePrefixes() error = %v", err)
	}

	stored := f.objects("ipam", "prefixes")
	if len(stored) != 1 {
		t.Fatalf("expected 1 prefix in store, got %d", len(stored))
	}
	// NetBox 4.2: the prefix site is carried in the generic scope.
	if st, _ := stored[0]["scope_type"].(string); st != "dcim.site" {
		t.Errorf("prefix scope_type = %v, expected \"dcim.site\"", stored[0]["scope_type"])
	}
	if got := utils.GetIDFromObject(stored[0]["scope_id"]); got != siteID {
		t.Errorf("prefix scope_id = %d, expected %d", got, siteID)
	}
	if got := utils.GetIDFromObject(stored[0]["vrf"]); got != utils.GetIDFromObject(vrf) {
		t.Errorf("prefix vrf = %d, expected %d", got, utils.GetIDFromObject(vrf))
	}
	if got := utils.GetIDFromObject(stored[0]["vlan"]); got != utils.GetIDFromObject(vlan) {
		t.Errorf("prefix vlan = %d, expected site-scoped VLAN ID %d", got, utils.GetIDFromObject(vlan))
	}

	// Idempotency: the lookup includes vrf_id, so the second run must
	// match the prefix just created.
	f.resetMutations()
	if err := nr.ReconcilePrefixes(prefixes); err != nil {
		t.Fatalf("ReconcilePrefixes() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

// TestReconcileNetworkResolvesReferencesWithoutSiteCache reproduces the
// production phase ordering: VLAN groups, VLANs and prefixes are reconciled in
// the network phase with only the global cache warm — the site caches that hold
// vlan_groups and vlans are not loaded until the later device phase. The
// reconcilers must seed the cache as they create objects so that a VLAN resolves
// its group and a prefix resolves its VLAN within the same phase. Before the
// fix, both lookups missed and the associations were silently dropped.
func TestReconcileNetworkResolvesReferencesWithoutSiteCache(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	vrf := f.seed("ipam", "vrfs", client.Object{"name": "prod"})

	// Only the global cache is loaded, exactly as in the network phase. No
	// LoadSite, so vlan_groups and vlans start absent from the cache.
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	nr := NewNetworkReconciler(c)

	if err := nr.ReconcileVLANGroups([]*models.VLANGroup{{
		Name: "Fabric", Slug: "fabric", SiteSlug: "berlin-dc",
	}}); err != nil {
		t.Fatalf("ReconcileVLANGroups() error = %v", err)
	}
	if err := nr.ReconcileVLANs([]*models.VLAN{{
		Name: "mgmt", VID: 100, SiteSlug: "berlin-dc", GroupSlug: "fabric", Status: "active",
	}}); err != nil {
		t.Fatalf("ReconcileVLANs() error = %v", err)
	}
	if err := nr.ReconcilePrefixes([]*models.Prefix{{
		Prefix: "10.0.0.0/24", SiteSlug: "berlin-dc", VRFName: "prod", VLANName: "mgmt", Status: "active",
	}}); err != nil {
		t.Fatalf("ReconcilePrefixes() error = %v", err)
	}

	groupID := utils.GetIDFromObject(f.objects("ipam", "vlan-groups")[0])
	vlan := f.objects("ipam", "vlans")[0]
	if got := utils.GetIDFromObject(vlan["group"]); got != groupID {
		t.Errorf("VLAN group = %v, expected group %d resolved from the seeded cache", vlan["group"], groupID)
	}
	prefix := f.objects("ipam", "prefixes")[0]
	if got := utils.GetIDFromObject(prefix["vlan"]); got != utils.GetIDFromObject(vlan) {
		t.Errorf("prefix vlan = %v, expected VLAN %d resolved from the seeded cache", prefix["vlan"], utils.GetIDFromObject(vlan))
	}
	if got := utils.GetIDFromObject(prefix["vrf"]); got != utils.GetIDFromObject(vrf) {
		t.Errorf("prefix vrf = %d, expected %d", got, utils.GetIDFromObject(vrf))
	}
}

// TestReconcileVRFsRegistersForSameRunLookup covers a first-run bug found
// against a live NetBox: the global cache is loaded once, before any phase, so
// a VRF created during the run was invisible to the prefixes that reference
// it. Those prefixes were created in the global table, and the next run
// created a second, correctly scoped copy beside them.
func TestReconcileVRFsRegistersForSameRunLookup(t *testing.T) {
	f, c := newFakeNetBox(t)
	nr := NewNetworkReconciler(c)

	vrfs := []*models.VRF{{Name: "Management", RD: "65000:10"}}
	if err := nr.ReconcileVRFs(vrfs); err != nil {
		t.Fatalf("ReconcileVRFs() error = %v", err)
	}

	vrfID, ok := c.Cache().GetGlobalID("vrfs", "Management")
	if !ok {
		t.Fatal("VRF created this run is not resolvable from the cache")
	}
	if want := utils.GetIDFromObject(f.objects("ipam", "vrfs")[0]); vrfID != want {
		t.Errorf("cached VRF id = %d, want %d", vrfID, want)
	}

	// The prefix that references it must land in the VRF on this same run.
	prefixes := []*models.Prefix{{Prefix: "10.0.100.0/24", VRFName: "Management", Status: "active"}}
	if err := nr.ReconcilePrefixes(prefixes); err != nil {
		t.Fatalf("ReconcilePrefixes() error = %v", err)
	}
	got := f.objects("ipam", "prefixes")
	if len(got) != 1 {
		t.Fatalf("expected 1 prefix, got %d", len(got))
	}
	if utils.GetIDFromObject(got[0]["vrf"]) != vrfID {
		t.Errorf("prefix vrf = %v, want the VRF created this run (%d)", got[0]["vrf"], vrfID)
	}

	// Second pass must not create a duplicate.
	f.resetMutations()
	if err := nr.ReconcilePrefixes(prefixes); err != nil {
		t.Fatalf("ReconcilePrefixes() second pass error = %v", err)
	}
	if n := len(f.objects("ipam", "prefixes")); n != 1 {
		t.Errorf("prefixes after second pass = %d, want 1 (no duplicate)", n)
	}
}
