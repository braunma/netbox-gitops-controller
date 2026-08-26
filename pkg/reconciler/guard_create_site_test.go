// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// The sandbox rehearsal creates its scratch site during the very run --assert-site
// guards, so the guard must not hard-fail when the allowed site does not exist
// yet: it must let the run create that site and then admit objects into it. This
// reproduces the documented rehearsal, which previously aborted with
// "--assert-site \"sandbox\" names no site on this instance".
func TestAssertSiteAllowsCreatingAndFillingTheAllowedSite(t *testing.T) {
	f, c := newFakeNetBox(t)
	c.SetAssertSites([]string{"sandbox"}) // sandbox does not exist in NetBox yet

	fr := NewFoundationReconciler(c)

	// Creating the allowed site itself must be permitted (a site is site-less).
	if err := fr.ReconcileSites([]*models.Site{{Name: "Sandbox", Slug: "sandbox"}}); err != nil {
		t.Fatalf("creating the allowed site was refused: %v", err)
	}

	// A rack in that just-created site must be admitted: the guard re-resolves
	// the slug and finds the new id.
	if err := fr.ReconcileRacks([]*models.Rack{{Name: "R1", Slug: "r1", SiteSlug: "sandbox"}}); err != nil {
		t.Fatalf("a rack in the newly-created allowed site was refused: %v", err)
	}

	if got := len(f.objects("dcim", "sites")); got != 1 {
		t.Fatalf("sandbox site not created: %d sites", got)
	}
	if got := len(f.objects("dcim", "racks")); got != 1 {
		t.Fatalf("rack in sandbox not created: %d racks", got)
	}

	// A typo is still caught: a write into a site outside the allowed set is
	// refused rather than silently admitted.
	f.seed("dcim", "sites", client.Object{"name": "Prod", "slug": "prod"})
	prodID := 0
	for _, s := range f.objects("dcim", "sites") {
		if s["slug"] == "prod" {
			prodID = s["id"].(int)
		}
	}
	c.Cache().Register("sites", prodID, "prod", "Prod")
	if err := fr.ReconcileRacks([]*models.Rack{{Name: "R2", Slug: "r2", SiteSlug: "prod"}}); err == nil {
		t.Fatal("a rack in a non-allowed site should have been refused")
	}
}
