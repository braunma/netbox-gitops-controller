// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"errors"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// setupGuardSites seeds an allowed "sandbox" site and a disallowed "prod" site,
// arms the guard on sandbox, and returns their ids.
func setupGuardSites(t *testing.T) (*fakeNetBox, *client.NetBoxClient, int, int) {
	t.Helper()
	f, c := newFakeNetBox(t)
	sandbox := f.seed("dcim", "sites", client.Object{"name": "Sandbox", "slug": "sandbox"})
	prod := f.seed("dcim", "sites", client.Object{"name": "Prod", "slug": "prod"})
	c.Cache().Register("sites", sandbox["id"].(int), "sandbox", "Sandbox")
	c.Cache().Register("sites", prod["id"].(int), "prod", "Prod")
	c.SetAssertSites([]string{"sandbox"})
	f.resetMutations()
	return f, c, sandbox["id"].(int), prod["id"].(int)
}

// A create targeting a site outside the allowed set is refused before any
// write, with a guard error the CLI maps to exit 3.
func TestAssertSiteRefusesWriteOutsideAllowedSite(t *testing.T) {
	f, c, _, _ := setupGuardSites(t)

	nr := NewNetworkReconciler(c)
	err := nr.ReconcileVLANs([]*models.VLAN{
		{Name: "web", VID: 100, SiteSlug: "prod"}, // prod is not allowed
	})
	if err == nil {
		t.Fatal("expected the guard to refuse a write into prod")
	}
	var sg *client.SiteGuardError
	if !errors.As(err, &sg) {
		t.Fatalf("expected a *client.SiteGuardError, got %T: %v", err, err)
	}
	if muts := f.mutationLog(); len(muts) != 0 {
		t.Fatalf("guard let %d write(s) through before aborting: %+v", len(muts), muts)
	}
}

// An update to an existing object whose current site is outside the allowed set
// is refused — the case that catches a write about to move a production object.
func TestAssertSiteRefusesUpdatingObjectCurrentlyOutside(t *testing.T) {
	f, c, sandboxID, prodID := setupGuardSites(t)

	// A prefix that currently lives in prod. The reconciler will find it by
	// CIDR and try to update it (re-scoping it to sandbox), which the guard
	// must refuse because its *current* site is prod.
	f.seed("ipam", "prefixes", client.Object{
		"prefix": "10.0.0.0/24", "scope_type": "dcim.site",
		"scope_id": prodID, "scope": map[string]interface{}{"id": prodID},
		"status": map[string]interface{}{"value": "active"},
	})
	f.resetMutations()

	// Register sandbox so the reconciler resolves the site scope for its payload.
	c.Cache().Register("sites", sandboxID, "sandbox", "Sandbox")

	nr := NewNetworkReconciler(c)
	err := nr.ReconcilePrefixes([]*models.Prefix{
		{Prefix: "10.0.0.0/24", SiteSlug: "sandbox"},
	})
	if err == nil {
		t.Fatal("expected the guard to refuse updating a prefix currently in prod")
	}
	var sg *client.SiteGuardError
	if !errors.As(err, &sg) {
		t.Fatalf("expected a *client.SiteGuardError, got %T: %v", err, err)
	}
	for _, m := range f.mutationLog() {
		if m.method != "GET" {
			t.Fatalf("guard let a %s through before aborting", m.method)
		}
	}
}

// A shared, site-less object (a device role) is allowed through and recorded as
// a shared touch.
func TestAssertSiteAllowsSharedObjects(t *testing.T) {
	_, c, _, _ := setupGuardSites(t)
	fr := NewFoundationReconciler(c)
	if err := fr.ReconcileRoles([]*models.Role{{Name: "Server", Slug: "server", Color: "00ff00"}}); err != nil {
		t.Fatalf("shared role should be allowed under the guard: %v", err)
	}
	found := false
	for _, e := range c.SharedTouched() {
		if e == "device-roles" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected device-roles recorded as a shared touch, got %v", c.SharedTouched())
	}
}
