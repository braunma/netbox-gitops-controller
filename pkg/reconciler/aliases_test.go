// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// What NetBox returns for a rack and a module type carries no YAML slug. A
// sync that skips the phases reconciling them must still resolve the slug.
func TestAliasesResolveWhenThePhaseDidNotRun(t *testing.T) {
	_, c := newFakeNetBox(t)
	cache := c.Cache()
	// As loaded from NetBox: site by slug, rack by name, module type by model
	// and by "<manufacturer slug>/<slugified model>".
	cache.Register("sites", 7, "berlin")
	cache.RegisterSite("racks", 7, 70, "Rack A-01")
	cache.Register("module_types", 9, "GPU A100", "example-vendor/gpu-a100")

	racks := []*models.Rack{
		{Name: "Rack A-01", Slug: "rack-a01", SiteSlug: "berlin"},
		{Name: "Rack Not There", Slug: "rack-x", SiteSlug: "berlin"},
		{Name: "Rack A-01", Slug: "rack-a01", SiteSlug: "unknown-site"},
	}
	RegisterRackAliases(c, racks)
	if id, ok := cache.GetSiteID("racks", 7, "rack-a01"); !ok || id != 70 {
		t.Errorf("rack-a01 = %d, %v; want 70", id, ok)
	}
	if _, ok := cache.GetSiteID("racks", 7, "rack-x"); ok {
		t.Error("a rack NetBox does not hold became resolvable")
	}

	types := []*models.ModuleType{
		{Manufacturer: "Example Vendor", Model: "GPU A100", Slug: "ex-gpu"},
		{Manufacturer: "Example Vendor", Model: "Not There", Slug: "ex-none"},
	}
	RegisterModuleTypeAliases(c, types)
	if id, ok := cache.GetGlobalID("module_types", "ex-gpu"); !ok || id != 9 {
		t.Errorf("ex-gpu = %d, %v; want 9", id, ok)
	}
	if id, ok := cache.GetGlobalID("module_types", "example-vendor/ex-gpu"); !ok || id != 9 {
		t.Errorf("example-vendor/ex-gpu = %d, %v; want 9", id, ok)
	}
	if _, ok := cache.GetGlobalID("module_types", "ex-none"); ok {
		t.Error("a module type NetBox does not hold became resolvable")
	}

	// What a phase registered in this run is left alone.
	cache.RegisterSite("racks", 7, 71, "rack-b")
	RegisterRackAliases(c, []*models.Rack{{Name: "Rack A-01", Slug: "rack-b", SiteSlug: "berlin"}})
	if id, _ := cache.GetSiteID("racks", 7, "rack-b"); id != 71 {
		t.Errorf("rack-b = %d, want the 71 registered earlier", id)
	}
}

// Two manufacturers with the same module type slug: the bare slug stays
// unresolvable, as it does when the phase runs.
func TestModuleTypeAliasesKeepAmbiguousSlugsUnresolved(t *testing.T) {
	_, c := newFakeNetBox(t)
	cache := c.Cache()
	cache.Register("module_types", 1, "vendor-a/psu")
	cache.Register("module_types", 2, "vendor-b/psu")
	RegisterModuleTypeAliases(c, []*models.ModuleType{
		{Manufacturer: "Vendor A", Model: "PSU", Slug: "psu"},
		{Manufacturer: "Vendor B", Model: "PSU", Slug: "psu"},
	})
	if _, ok := cache.GetGlobalID("module_types", "psu"); ok {
		t.Error("an ambiguous slug resolved")
	}
	if id, ok := cache.GetGlobalID("module_types", "vendor-b/psu"); !ok || id != 2 {
		t.Errorf("vendor-b/psu = %d, %v", id, ok)
	}
}
