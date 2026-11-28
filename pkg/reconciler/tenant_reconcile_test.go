// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// tenantOf returns the tenant ID stored on the only object of an endpoint.
func tenantOf(t *testing.T, f *fakeNetBox, app, endpoint string) int {
	t.Helper()
	objs := f.objects(app, endpoint)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object in %s/%s, got %d", app, endpoint, len(objs))
	}
	return utils.GetIDFromObject(objs[0]["tenant"])
}

// A `tenant:` is written on every kind that can belong to one, and the tenant
// is the one reconciled earlier in this same run: nothing is preloaded, so
// each reference resolves through what ReconcileTenants registered.
func TestTenantIsSetOnEveryKindThatDeclaresOne(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)
	nr := NewNetworkReconciler(c)

	if err := fr.ReconcileTenants([]*models.Tenant{{Name: "Platform Team", Slug: "platform"}}); err != nil {
		t.Fatalf("ReconcileTenants() error = %v", err)
	}
	tenantID := utils.GetIDFromObject(f.objects("tenancy", "tenants")[0])

	steps := []struct {
		app, endpoint string
		run           func() error
	}{
		{"dcim", "sites", func() error {
			return fr.ReconcileSites([]*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Tenant: "platform"}})
		}},
		{"dcim", "racks", func() error {
			return fr.ReconcileRacks([]*models.Rack{{Name: "R01", Slug: "r01", SiteSlug: "berlin-dc", Tenant: "platform"}})
		}},
		{"ipam", "vrfs", func() error {
			return nr.ReconcileVRFs([]*models.VRF{{Name: "prod", Tenant: "platform"}})
		}},
		{"ipam", "vlans", func() error {
			return nr.ReconcileVLANs([]*models.VLAN{{Name: "mgmt", VID: 100, SiteSlug: "berlin-dc", Tenant: "platform"}})
		}},
		{"ipam", "prefixes", func() error {
			// The tenant's name is accepted where its slug is, like every
			// other reference.
			return nr.ReconcilePrefixes([]*models.Prefix{{Prefix: "10.0.0.0/24", Tenant: "Platform Team"}})
		}},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("reconcile %s/%s error = %v", step.app, step.endpoint, err)
		}
		if got := tenantOf(t, f, step.app, step.endpoint); got != tenantID {
			t.Errorf("%s/%s tenant = %d, want %d", step.app, step.endpoint, got, tenantID)
		}
	}

	// Idempotent: the same YAML again changes nothing.
	f.resetMutations()
	for _, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("second reconcile %s/%s error = %v", step.app, step.endpoint, err)
		}
	}
	f.requireMutationCount(t, 0)
}

func TestTenantIsSetOnDevice(t *testing.T) {
	f, c := newFakeNetBox(t)
	seedDeviceFoundation(t, f, c)
	tenant := f.seed("tenancy", "tenants", client.Object{"name": "Platform Team", "slug": "platform"})
	if err := c.Cache().LoadSite(testSiteSlug); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	device := testDevice("srv-01")
	device.Tenant = "platform"
	dr := NewDeviceReconciler(c)
	if err := dr.ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}
	if got, want := tenantOf(t, f, "dcim", "devices"), utils.GetIDFromObject(tenant); got != want {
		t.Errorf("device tenant = %d, want %d", got, want)
	}

	f.resetMutations()
	if err := dr.ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

// A tenant somebody assigned in the NetBox UI survives a sync whose YAML says
// nothing about tenants: no `tenant` key may appear in a request at all.
func TestUndeclaredTenantIsLeftAlone(t *testing.T) {
	f, c := newFakeNetBox(t)
	tenant := f.seed("tenancy", "tenants", client.Object{"name": "Platform Team", "slug": "platform"})
	f.seed("dcim", "sites", client.Object{
		"name": "Berlin DC", "slug": "berlin-dc", "status": "active", "tenant": tenant["id"],
		"tags": []interface{}{map[string]interface{}{"id": c.ManagedTagID()}},
	})

	sites := []*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Description: "now with a description"}}
	if err := NewFoundationReconciler(c).ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}

	for _, req := range f.mutationLog() {
		if value, sent := req.body["tenant"]; sent {
			t.Errorf("%s %s sent tenant=%v, which the YAML does not declare", req.method, req.path, value)
		}
	}
	if got, want := tenantOf(t, f, "dcim", "sites"), utils.GetIDFromObject(tenant); got != want {
		t.Errorf("site tenant = %d after a sync that never mentioned it, want %d", got, want)
	}
}

// An unknown tenant does not fail the object: it is created without one, and
// the warning says where to declare it.
func TestUnknownTenantIsLeftUnset(t *testing.T) {
	f, c := newFakeNetBox(t)
	sites := []*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Tenant: "does-not-exist"}}
	if err := NewFoundationReconciler(c).ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() error = %v, expected an unknown tenant to be tolerated", err)
	}
	stored := f.objects("dcim", "sites")
	if len(stored) != 1 {
		t.Fatalf("expected the site to still be created, got %d", len(stored))
	}
	if _, set := stored[0]["tenant"]; set {
		t.Errorf("site tenant should be unset when the tenant is unknown, got %v", stored[0]["tenant"])
	}
}

// In --dry-run a tenant declared in this same run has no ID yet. The site is
// still planned, and nothing is written.
func TestTenantPlannedInDryRunDoesNotBlockTheSite(t *testing.T) {
	f, c := newFakeNetBox(t)
	c.SetDryRun(true)
	fr := NewFoundationReconciler(c)

	if err := fr.ReconcileTenants([]*models.Tenant{{Name: "Platform Team", Slug: "platform"}}); err != nil {
		t.Fatalf("ReconcileTenants() error = %v", err)
	}
	if err := fr.ReconcileSites([]*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Tenant: "platform"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	f.requireMutationCount(t, 0)
}
