package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

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
	if got := utils.GetIDFromObject(stored[0]["site"]); got != utils.GetIDFromObject(site) {
		t.Errorf("VLAN group site = %d, expected cached site ID %d", got, utils.GetIDFromObject(site))
	}
	if got := utils.GetIDFromObject(stored[0]["min_vid"]); got != 100 {
		t.Errorf("VLAN group min_vid = %v, expected 100", stored[0]["min_vid"])
	}
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
	if got := utils.GetIDFromObject(stored[0]["site"]); got != siteID {
		t.Errorf("prefix site = %d, expected %d", got, siteID)
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
