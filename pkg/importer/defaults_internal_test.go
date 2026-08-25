// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

func render(t *testing.T, items []interface{}, enabled bool, minItems int) string {
	t.Helper()
	b, err := renderFile("", "items", items, DefaultsOptions(enabled, minItems))
	if err != nil {
		t.Fatalf("renderFile: %v", err)
	}
	return string(b)
}

// A key identical across every item is hoisted into defaults and removed from
// each item.
func TestDefaultsHoistsSharedKey(t *testing.T) {
	items := []interface{}{
		&models.DeviceConfig{Name: "a", SiteSlug: "bln", RoleSlug: "server", DeviceTypeSlug: "r640", RackSlug: "r1"},
		&models.DeviceConfig{Name: "b", SiteSlug: "bln", RoleSlug: "server", DeviceTypeSlug: "r640", RackSlug: "r1"},
		&models.DeviceConfig{Name: "c", SiteSlug: "bln", RoleSlug: "server", DeviceTypeSlug: "r640", RackSlug: "r1"},
	}
	out := render(t, items, true, 3)
	if !strings.Contains(out, "defaults:") {
		t.Fatalf("expected a defaults block:\n%s", out)
	}
	if !strings.Contains(out, "site_slug: bln") || !strings.Contains(out, "role_slug: server") {
		t.Fatalf("shared keys not hoisted:\n%s", out)
	}
	// site_slug must appear exactly once (in defaults), not on each item.
	if n := strings.Count(out, "site_slug:"); n != 1 {
		t.Fatalf("site_slug appears %d times, want 1:\n%s", n, out)
	}
}

// The critical safety rule: a key any item leaves at its zero value (absent
// after omitempty) must NOT be hoisted, or an item that omits it would silently
// inherit another's value — the rack-teleport bug.
func TestDefaultsNeverHoistsWhenAnyItemOmits(t *testing.T) {
	items := []interface{}{
		&models.DeviceConfig{Name: "a", SiteSlug: "bln", RoleSlug: "server", DeviceTypeSlug: "r640", RackSlug: "r1"},
		&models.DeviceConfig{Name: "b", SiteSlug: "bln", RoleSlug: "server", DeviceTypeSlug: "r640", RackSlug: "r1"},
		// c has no rack: rack_slug is absent, so it must never be hoisted.
		&models.DeviceConfig{Name: "c", SiteSlug: "bln", RoleSlug: "server", DeviceTypeSlug: "r640"},
	}
	out := render(t, items, true, 3)
	if strings.Contains(out, "defaults:\n") && strings.Contains(firstBlock(out), "rack_slug:") {
		t.Fatalf("rack_slug was hoisted despite an item omitting it:\n%s", out)
	}
	// site_slug is present on all three, so it may still hoist.
	if !strings.Contains(out, "site_slug: bln") {
		t.Fatalf("site_slug should still hoist:\n%s", out)
	}
	// c must keep no rack_slug at all.
	if strings.Contains(out, "rack_slug: r1\n- name: c") {
		t.Fatalf("device c wrongly gained a rack_slug")
	}
}

// Below the item floor nothing is hoisted, even when identical.
func TestDefaultsRespectsMinItems(t *testing.T) {
	items := []interface{}{
		&models.Site{Name: "a", Slug: "a", Status: "active"},
		&models.Site{Name: "b", Slug: "b", Status: "active"},
	}
	out := render(t, items, true, 3)
	if strings.Contains(out, "defaults:") {
		t.Fatalf("hoisted below minItems:\n%s", out)
	}
}

// Identity keys are never hoisted even when (pathologically) identical.
func TestDefaultsNeverHoistsIdentity(t *testing.T) {
	items := []interface{}{
		&models.Site{Name: "same", Slug: "same", Status: "active"},
		&models.Site{Name: "same", Slug: "same", Status: "active"},
		&models.Site{Name: "same", Slug: "same", Status: "active"},
	}
	out := render(t, items, true, 3)
	if strings.Contains(firstBlock(out), "name:") || strings.Contains(firstBlock(out), "slug:") {
		t.Fatalf("identity key hoisted:\n%s", out)
	}
	// status, however, may hoist.
	if !strings.Contains(out, "status: active") {
		t.Fatalf("status should hoist:\n%s", out)
	}
}

// firstBlock returns the defaults block region (everything before the list key)
// for coarse assertions.
func firstBlock(out string) string {
	if i := strings.Index(out, "\nitems:"); i >= 0 {
		return out[:i]
	}
	return out
}
