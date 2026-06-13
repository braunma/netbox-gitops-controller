package main

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// allPhases returns a phase set with every phase selected, matching a default
// (no --only) run.
func allPhases() map[string]bool {
	phases := map[string]bool{}
	for _, p := range validPhases {
		phases[p] = true
	}
	return phases
}

// indexOf returns the position of an app/endpoint in the target list, or -1.
func indexOf(targets []pruneTargetKey, app, endpoint string) int {
	for i, k := range targets {
		if k.app == app && k.endpoint == endpoint {
			return i
		}
	}
	return -1
}

type pruneTargetKey struct{ app, endpoint string }

func keys(phases map[string]bool) []pruneTargetKey {
	var out []pruneTargetKey
	for _, t := range pruneTargets(phases) {
		out = append(out, pruneTargetKey{t.App, t.Endpoint})
	}
	return out
}

// TestPruneTargetsReverseDependencyOrder is the safety net for the deletion
// order: every object must be pruned before the object it references, or
// NetBox would reject the delete with a foreign-key error. These failures
// only surface against a real NetBox, so the invariant is asserted here as
// data.
func TestPruneTargetsReverseDependencyOrder(t *testing.T) {
	targets := keys(allPhases())

	// child must be deleted before parent (child references parent).
	deps := []struct{ child, childEP, parent, parentEP string }{
		{"ipam", "ip-addresses", "dcim", "interfaces"},
		{"dcim", "front-ports", "dcim", "rear-ports"},
		{"dcim", "interfaces", "dcim", "devices"},
		{"dcim", "rear-ports", "dcim", "devices"},
		{"dcim", "modules", "dcim", "devices"},
		{"dcim", "modules", "dcim", "module-types"},
		{"dcim", "devices", "dcim", "device-types"},
		{"dcim", "devices", "dcim", "racks"},
		{"dcim", "devices", "dcim", "device-roles"},
		{"dcim", "devices", "dcim", "sites"},
		{"ipam", "prefixes", "ipam", "vlans"},
		{"ipam", "prefixes", "ipam", "vrfs"},
		{"ipam", "prefixes", "dcim", "sites"},
		{"ipam", "vlans", "ipam", "vlan-groups"},
		{"ipam", "vlans", "dcim", "sites"},
		{"ipam", "vlan-groups", "dcim", "sites"},
		{"dcim", "racks", "dcim", "sites"},
	}

	for _, d := range deps {
		ci := indexOf(targets, d.child, d.childEP)
		pi := indexOf(targets, d.parent, d.parentEP)
		if ci == -1 {
			t.Errorf("missing prune target %s/%s", d.child, d.childEP)
			continue
		}
		if pi == -1 {
			t.Errorf("missing prune target %s/%s", d.parent, d.parentEP)
			continue
		}
		if ci > pi {
			t.Errorf("%s/%s (idx %d) must be pruned before %s/%s (idx %d)",
				d.child, d.childEP, ci, d.parent, d.parentEP, pi)
		}
	}
}

// TestPruneTargetsExcludesCables guards the deliberate decision not to prune
// cables (they are never tagged, and NetBox cascade-deletes them).
func TestPruneTargetsExcludesCables(t *testing.T) {
	if indexOf(keys(allPhases()), "dcim", "cables") != -1 {
		t.Error("cables must not be a prune target")
	}
	// Manufacturers are shared/auto-created and likewise excluded.
	if indexOf(keys(allPhases()), "dcim", "manufacturers") != -1 {
		t.Error("manufacturers must not be a prune target")
	}
}

// TestPruneTargetsNoDuplicates ensures each endpoint appears at most once, so
// the same object is never visited (and deleted) twice.
func TestPruneTargetsNoDuplicates(t *testing.T) {
	seen := map[pruneTargetKey]bool{}
	for _, k := range keys(allPhases()) {
		if seen[k] {
			t.Errorf("duplicate prune target %s/%s", k.app, k.endpoint)
		}
		seen[k] = true
	}
}

// TestPruneTargetsPhaseScoped verifies --only keeps prune in scope: a phase's
// endpoints appear only when that phase is selected.
func TestPruneTargetsPhaseScoped(t *testing.T) {
	foundationOnly := keys(map[string]bool{"foundation": true})
	if indexOf(foundationOnly, "dcim", "sites") == -1 {
		t.Error("foundation-only prune must include sites")
	}
	if indexOf(foundationOnly, "dcim", "devices") != -1 {
		t.Error("foundation-only prune must not include devices")
	}
	if indexOf(foundationOnly, "ipam", "vlans") != -1 {
		t.Error("foundation-only prune must not include vlans")
	}

	devicesOnly := keys(map[string]bool{"devices": true})
	if indexOf(devicesOnly, "dcim", "interfaces") == -1 {
		t.Error("devices-only prune must include interfaces")
	}
	if indexOf(devicesOnly, "dcim", "sites") != -1 {
		t.Error("devices-only prune must not include sites")
	}

	if len(keys(map[string]bool{})) != 0 {
		t.Error("no phases selected must yield no prune targets")
	}
}

// TestPruneTargetsVirtualizationOrder extends the reverse-dependency invariant
// to the virtualization endpoints: VM children before VMs, VMs/clusters before
// the VLANs/sites/tenants/types they reference, and IPs pruned first of all.
func TestPruneTargetsVirtualizationOrder(t *testing.T) {
	targets := keys(allPhases())

	if i := indexOf(targets, "ipam", "ip-addresses"); i != 0 {
		t.Errorf("ip-addresses index = %d, expected 0 (pruned before any interface)", i)
	}

	deps := []struct{ child, childEP, parent, parentEP string }{
		{"ipam", "ip-addresses", "virtualization", "interfaces"},
		{"virtualization", "interfaces", "virtualization", "virtual-machines"},
		{"virtualization", "interfaces", "ipam", "vlans"},
		{"virtualization", "virtual-machines", "virtualization", "clusters"},
		{"virtualization", "clusters", "virtualization", "cluster-groups"},
		{"virtualization", "clusters", "virtualization", "cluster-types"},
		{"virtualization", "clusters", "dcim", "sites"},
		{"virtualization", "clusters", "tenancy", "tenants"},
		{"virtualization", "virtual-machines", "dcim", "device-roles"},
		{"virtualization", "virtual-machines", "dcim", "platforms"},
		{"tenancy", "tenants", "tenancy", "tenant-groups"},
	}
	for _, d := range deps {
		ci, pi := indexOf(targets, d.child, d.childEP), indexOf(targets, d.parent, d.parentEP)
		if ci == -1 || pi == -1 {
			t.Errorf("missing target: %s/%s=%d %s/%s=%d", d.child, d.childEP, ci, d.parent, d.parentEP, pi)
			continue
		}
		if ci > pi {
			t.Errorf("%s/%s (idx %d) must be pruned before %s/%s (idx %d)",
				d.child, d.childEP, ci, d.parent, d.parentEP, pi)
		}
	}
}

// TestPruneTargetsVirtualizationOnly verifies a virtualization-only prune still
// removes VM-interface IP orphans (the shared ip-addresses entry) without
// touching foundation/network endpoints.
func TestPruneTargetsVirtualizationOnly(t *testing.T) {
	only := keys(map[string]bool{"virtualization": true})

	if indexOf(only, "ipam", "ip-addresses") != 0 {
		t.Errorf("--only virtualization must prune ip-addresses first, got %v", only)
	}
	if indexOf(only, "virtualization", "virtual-machines") == -1 {
		t.Errorf("--only virtualization must prune virtual-machines, got %v", only)
	}
	if indexOf(only, "dcim", "sites") != -1 {
		t.Errorf("--only virtualization must not prune foundation endpoints, got %v", only)
	}
	if indexOf(only, "ipam", "vlans") != -1 {
		t.Errorf("--only virtualization must not prune network endpoints, got %v", only)
	}
}

// TestFilterVMs covers the --site/--vm filtering, including that --site does
// not match a clustered VM (whose site is inherited from its cluster).
func TestFilterVMs(t *testing.T) {
	vms := []*models.VMConfig{
		{Name: "web-01", SiteSlug: "berlin-dc"},
		{Name: "web-02", SiteSlug: "berlin-dc"},
		{Name: "edge-01", SiteSlug: "test-lab"},
		{Name: "clustered", Cluster: "c1"}, // no site_slug
	}

	tests := []struct {
		name      string
		site, vm  string
		wantNames []string
	}{
		{"no filter", "", "", []string{"web-01", "web-02", "edge-01", "clustered"}},
		{"by site", "berlin-dc", "", []string{"web-01", "web-02"}},
		{"by vm name", "", "edge-01", []string{"edge-01"}},
		{"by site and vm", "berlin-dc", "web-01", []string{"web-01"}},
		{"site does not match clustered vm", "berlin-dc", "clustered", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterVMs(vms, tt.site, tt.vm)
			if len(got) != len(tt.wantNames) {
				t.Fatalf("filterVMs(%q,%q) = %d VMs, want %d", tt.site, tt.vm, len(got), len(tt.wantNames))
			}
			for i, name := range tt.wantNames {
				if got[i].Name != name {
					t.Errorf("result[%d] = %q, want %q", i, got[i].Name, name)
				}
			}
		})
	}
}

// TestPruneTargetsProtectsManagedTag verifies the managed tag is shielded from
// pruning via KeepSlugs.
func TestPruneTargetsProtectsManagedTag(t *testing.T) {
	var found bool
	for _, target := range pruneTargets(allPhases()) {
		if target.App == "extras" && target.Endpoint == "tags" {
			found = true
			var protects bool
			for _, slug := range target.KeepSlugs {
				if slug == constants.ManagedTagSlug {
					protects = true
				}
			}
			if !protects {
				t.Errorf("tags prune target must keep the managed tag %q, got KeepSlugs %v",
					constants.ManagedTagSlug, target.KeepSlugs)
			}
		}
	}
	if !found {
		t.Error("foundation prune must include the tags endpoint")
	}
}
