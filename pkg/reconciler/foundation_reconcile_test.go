package reconciler

import (
	"reflect"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func TestReconcileSitesCreateThenIdempotentThenUpdate(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	sites := []*models.Site{{
		Name:        "Berlin DC",
		Slug:        "berlin-dc",
		Status:      "active",
		Description: "primary site",
	}}

	// First run creates the site.
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	muts := f.requireMutationCount(t, 1)
	if muts[0].method != "POST" || muts[0].path != "/api/dcim/sites/" {
		t.Errorf("expected POST /api/dcim/sites/, got %s %s", muts[0].method, muts[0].path)
	}

	stored := f.objects("dcim", "sites")
	if len(stored) != 1 {
		t.Fatalf("expected 1 site in store, got %d", len(stored))
	}
	if stored[0]["name"] != "Berlin DC" || stored[0]["description"] != "primary site" {
		t.Errorf("stored site = %v, expected name and description from the definition", stored[0])
	}

	// Second run with identical definitions must not mutate anything.
	f.resetMutations()
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)

	// A changed field results in exactly one PATCH containing only that field.
	sites[0].Description = "updated description"
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() third run error = %v", err)
	}
	muts = f.requireMutationCount(t, 1)
	if muts[0].method != "PATCH" {
		t.Errorf("expected PATCH, got %s %s", muts[0].method, muts[0].path)
	}
	want := map[string]interface{}{"description": "updated description"}
	if !reflect.DeepEqual(muts[0].body, want) {
		t.Errorf("PATCH body = %v, expected only the changed field %v", muts[0].body, want)
	}
}

func TestReconcileRacksResolvesSiteID(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	siteID := utils.GetIDFromObject(site)

	fr := NewFoundationReconciler(c)
	racks := []*models.Rack{{
		Name:     "R01",
		SiteSlug: "berlin-dc",
		Status:   "active",
		Width:    19,
		UHeight:  42,
	}}
	if err := fr.ReconcileRacks(racks); err != nil {
		t.Fatalf("ReconcileRacks() error = %v", err)
	}

	stored := f.objects("dcim", "racks")
	if len(stored) != 1 {
		t.Fatalf("expected 1 rack in store, got %d", len(stored))
	}
	if got := utils.GetIDFromObject(stored[0]["site"]); got != siteID {
		t.Errorf("rack site = %d, expected resolved site ID %d", got, siteID)
	}
	if got := utils.GetIDFromObject(stored[0]["u_height"]); got != 42 {
		t.Errorf("rack u_height = %v, expected 42", stored[0]["u_height"])
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := fr.ReconcileRacks(racks); err != nil {
		t.Fatalf("ReconcileRacks() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileRacksFallsBackToSiteName(t *testing.T) {
	f, c := newFakeNetBox(t)
	// The site's slug does not match the rack's site reference, but its
	// name does: the reconciler must fall back to a name lookup.
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "ber1"})

	fr := NewFoundationReconciler(c)
	racks := []*models.Rack{{Name: "R01", SiteSlug: "Berlin DC", Status: "active"}}
	if err := fr.ReconcileRacks(racks); err != nil {
		t.Fatalf("ReconcileRacks() error = %v", err)
	}

	stored := f.objects("dcim", "racks")
	if len(stored) != 1 {
		t.Fatalf("expected 1 rack in store, got %d", len(stored))
	}
	if got := utils.GetIDFromObject(stored[0]["site"]); got != utils.GetIDFromObject(site) {
		t.Errorf("rack site = %d, expected site resolved by name", got)
	}
}

func TestReconcileRacksSkipsUnknownSite(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	racks := []*models.Rack{{Name: "R01", SiteSlug: "does-not-exist", Status: "active"}}
	if err := fr.ReconcileRacks(racks); err != nil {
		t.Fatalf("ReconcileRacks() error = %v, expected unknown site to be skipped without error", err)
	}
	f.requireMutationCount(t, 0)
	if got := len(f.objects("dcim", "racks")); got != 0 {
		t.Errorf("expected no racks created, got %d", got)
	}
}

func TestReconcileRolesNormalizesColor(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	roles := []*models.Role{{Name: "Switch", Slug: "switch", Color: "#FF0000"}}
	if err := fr.ReconcileRoles(roles); err != nil {
		t.Fatalf("ReconcileRoles() error = %v", err)
	}

	stored := f.objects("dcim", "device-roles")
	if len(stored) != 1 {
		t.Fatalf("expected 1 role in store, got %d", len(stored))
	}
	if stored[0]["color"] != "ff0000" {
		t.Errorf("role color = %v, expected normalized \"ff0000\"", stored[0]["color"])
	}

	// Re-running with the same (un-normalized) color must not PATCH.
	f.resetMutations()
	if err := fr.ReconcileRoles(roles); err != nil {
		t.Fatalf("ReconcileRoles() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcilePlatformsCreatesAndResolvesManufacturer(t *testing.T) {
	f, c := newFakeNetBox(t)
	mfg := f.seed("dcim", "manufacturers", client.Object{"name": "Canonical", "slug": "canonical"})
	mfgID := utils.GetIDFromObject(mfg)
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	fr := NewFoundationReconciler(c)
	platforms := []*models.Platform{{
		Name:         "Ubuntu 22.04",
		Slug:         "ubuntu-22-04",
		Manufacturer: "canonical",
		Description:  "LTS",
	}}
	if err := fr.ReconcilePlatforms(platforms); err != nil {
		t.Fatalf("ReconcilePlatforms() error = %v", err)
	}

	stored := f.objects("dcim", "platforms")
	if len(stored) != 1 {
		t.Fatalf("expected 1 platform in store, got %d", len(stored))
	}
	if got := utils.GetIDFromObject(stored[0]["manufacturer"]); got != mfgID {
		t.Errorf("platform manufacturer = %d, expected resolved manufacturer ID %d", got, mfgID)
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := fr.ReconcilePlatforms(platforms); err != nil {
		t.Fatalf("ReconcilePlatforms() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcilePlatformsAutoCreatesMissingManufacturer(t *testing.T) {
	f, c := newFakeNetBox(t)
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	fr := NewFoundationReconciler(c)
	platforms := []*models.Platform{{Name: "VyOS", Slug: "vyos", Manufacturer: "VyOS Networks"}}
	if err := fr.ReconcilePlatforms(platforms); err != nil {
		t.Fatalf("ReconcilePlatforms() error = %v", err)
	}

	mfgs := f.objects("dcim", "manufacturers")
	if len(mfgs) != 1 || mfgs[0]["slug"] != "vyos-networks" {
		t.Fatalf("expected an auto-created manufacturer with slug vyos-networks, got %v", mfgs)
	}
	if got := utils.GetIDFromObject(f.objects("dcim", "platforms")[0]["manufacturer"]); got != utils.GetIDFromObject(mfgs[0]) {
		t.Errorf("platform manufacturer = %d, expected the auto-created manufacturer", got)
	}
}

func TestReconcileTenantGroupsResolvesParent(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	// Parent and child declared in one run; the parent must resolve via the
	// live lookup even though it was created moments earlier.
	groups := []*models.TenantGroup{
		{Name: "Customers", Slug: "customers"},
		{Name: "Enterprise", Slug: "enterprise", ParentSlug: "customers"},
	}
	if err := fr.ReconcileTenantGroups(groups); err != nil {
		t.Fatalf("ReconcileTenantGroups() error = %v", err)
	}

	stored := f.objects("tenancy", "tenant-groups")
	if len(stored) != 2 {
		t.Fatalf("expected 2 tenant groups in store, got %d", len(stored))
	}
	var parentID, childParent int
	for _, g := range stored {
		if g["slug"] == "customers" {
			parentID = utils.GetIDFromObject(g)
		}
		if g["slug"] == "enterprise" {
			childParent = utils.GetIDFromObject(g["parent"])
		}
	}
	if childParent != parentID || parentID == 0 {
		t.Errorf("enterprise parent = %d, expected customers ID %d", childParent, parentID)
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := fr.ReconcileTenantGroups(groups); err != nil {
		t.Fatalf("ReconcileTenantGroups() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileTenantsResolvesGroup(t *testing.T) {
	f, c := newFakeNetBox(t)
	group := f.seed("tenancy", "tenant-groups", client.Object{"name": "Customers", "slug": "customers"})
	groupID := utils.GetIDFromObject(group)

	fr := NewFoundationReconciler(c)
	tenants := []*models.Tenant{{Name: "Acme Corp", Slug: "acme-corp", GroupSlug: "customers"}}
	if err := fr.ReconcileTenants(tenants); err != nil {
		t.Fatalf("ReconcileTenants() error = %v", err)
	}

	stored := f.objects("tenancy", "tenants")
	if len(stored) != 1 {
		t.Fatalf("expected 1 tenant in store, got %d", len(stored))
	}
	if got := utils.GetIDFromObject(stored[0]["group"]); got != groupID {
		t.Errorf("tenant group = %d, expected resolved group ID %d", got, groupID)
	}
}

func TestReconcileTenantsSkipsUnknownGroup(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	tenants := []*models.Tenant{{Name: "Acme Corp", Slug: "acme-corp", GroupSlug: "does-not-exist"}}
	if err := fr.ReconcileTenants(tenants); err != nil {
		t.Fatalf("ReconcileTenants() error = %v, expected unknown group to be tolerated", err)
	}
	stored := f.objects("tenancy", "tenants")
	if len(stored) != 1 {
		t.Fatalf("expected the tenant to still be created, got %d", len(stored))
	}
	if _, set := stored[0]["group"]; set {
		t.Errorf("tenant group should be unset when the group is unknown, got %v", stored[0]["group"])
	}
}

func TestReconcileTagsCreatesTags(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	tags := []*models.Tag{{Name: "Production", Slug: "production", Color: "0f0", Description: "prod gear"}}
	if err := fr.ReconcileTags(tags); err != nil {
		t.Fatalf("ReconcileTags() error = %v", err)
	}

	// The store already contains the managed tag created by NewClient;
	// find the reconciled one by slug.
	var found client.Object
	for _, obj := range f.objects("extras", "tags") {
		if obj["slug"] == "production" {
			found = obj
		}
	}
	if found == nil {
		t.Fatal("tag \"production\" not created")
	}
	if found["color"] != "00ff00" {
		t.Errorf("tag color = %v, expected 3-digit color expanded to \"00ff00\"", found["color"])
	}
}

// TestDefaultStatus covers a class found with randomly generated data: NetBox
// rejects an empty status with "This field may not be blank" rather than
// applying its own default, so an object whose YAML simply omits the field
// used to fail the whole run.
func TestDefaultStatus(t *testing.T) {
	if got := defaultStatus(""); got != "active" {
		t.Errorf("defaultStatus(\"\") = %q, want active", got)
	}
	for _, explicit := range []string{"planned", "offline", "reserved", "deprecated"} {
		if got := defaultStatus(explicit); got != explicit {
			t.Errorf("defaultStatus(%q) = %q, want it preserved", explicit, got)
		}
	}
}

// TestReconcileRacksDefaultsStatus is the end-to-end form: a rack declared
// without a status must still be created.
func TestReconcileRacksDefaultsStatus(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	racks := []*models.Rack{{Name: "Rack 01", Slug: "rack-01", SiteSlug: "berlin-dc", UHeight: 42}}
	if err := NewFoundationReconciler(c).ReconcileRacks(racks); err != nil {
		t.Fatalf("ReconcileRacks() error = %v", err)
	}

	got := f.objects("dcim", "racks")
	if len(got) != 1 {
		t.Fatalf("expected 1 rack, got %d", len(got))
	}
	if got[0]["status"] != "active" {
		t.Errorf("rack status = %v, want the defaulted \"active\"", got[0]["status"])
	}
	// The rack must also be resolvable by its YAML slug, which NetBox does not
	// store — devices reference it that way.
	if _, ok := c.Cache().GetSiteID("racks", utils.GetIDFromObject(site), "rack-01"); !ok {
		t.Error("rack is not resolvable by its YAML slug; devices would get no rack")
	}
}
