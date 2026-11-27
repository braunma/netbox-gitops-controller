// SPDX-License-Identifier: Apache-2.0

package client

import (
	"testing"
)

// A dry run plans with the managed tag the apply will send. Without it an
// object with tags of its own was planned as an update on every run, and an
// object about to be adopted as unchanged.
func TestResolveManagedTagForPlanFindsTheTag(t *testing.T) {
	p, c := newPruneTestServer(t, true)
	p.store["extras/tags"] = []Object{{"id": float64(7), "slug": "gitops", "name": "GitOps Managed"}}

	if err := c.ResolveManagedTagForPlan(); err != nil {
		t.Fatalf("ResolveManagedTagForPlan() error = %v", err)
	}
	if c.ManagedTagID() != 7 {
		t.Errorf("managed tag ID = %d, want 7", c.ManagedTagID())
	}
	got := c.Tags().InjectTag(map[string]interface{}{"tags": []int{3}}, c.ManagedTagID())
	if ids, _ := got["tags"].([]int); len(ids) != 2 {
		t.Errorf("planned tags = %v, want the declared tag and the managed one", got["tags"])
	}
}

func TestResolveManagedTagForPlanOnlyReads(t *testing.T) {
	p, c := newPruneTestServer(t, false)
	p.store["extras/tags"] = []Object{{"id": float64(7), "slug": "gitops"}}
	if err := c.ResolveManagedTagForPlan(); err != nil {
		t.Fatal(err)
	}
	if c.ManagedTagID() != 0 {
		t.Errorf("outside a dry run it must leave the ID to NewClient, got %d", c.ManagedTagID())
	}
}

// Against a NetBox the controller never wrote to, the tag does not exist.
// NetBox answers ?tag=<unknown> with a 400, so the dry-run prune must not ask:
// nothing can carry a tag that does not exist.
func TestDryRunPruneWithoutManagedTagPlansNothing(t *testing.T) {
	p, c := newPruneTestServer(t, true)
	// The test server filters by tag slug whether or not the tag exists,
	// so an object claiming it shows whether the tag pass ran at all.
	p.store["dcim/sites"] = []Object{managedObj(1, map[string]interface{}{"slug": "x"})}

	if err := c.ResolveManagedTagForPlan(); err != nil {
		t.Fatal(err)
	}
	if err := c.Prune([]PruneTarget{{App: "dcim", Endpoint: "sites"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if n := len(c.Recorder().Changes()); n != 0 {
		t.Errorf("planned %d deletion(s) with no managed tag in NetBox", n)
	}
}
