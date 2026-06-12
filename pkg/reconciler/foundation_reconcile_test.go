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
