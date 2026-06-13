package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// managedTags builds the tag reference a managed object carries, using the
// client's live managed tag ID so the fake's ?tag=gitops filter matches it.
func managedTags(c *client.NetBoxClient) []interface{} {
	return []interface{}{map[string]interface{}{
		"id": float64(c.ManagedTagID()), "slug": "gitops",
	}}
}

// TestPruneEndToEnd exercises the real reconcile→prune flow against the fake
// NetBox: objects declared in YAML are reconciled (and marked seen), a
// previously-managed object that is no longer declared is pruned, and both a
// still-declared object and an unmanaged (untagged) object survive.
func TestPruneEndToEnd(t *testing.T) {
	f, c := newFakeNetBox(t)

	// A site that used to be managed but is no longer in the desired set.
	f.seed("dcim", "sites", client.Object{
		"name": "Old DC", "slug": "old-dc", "status": "active",
		"tags": managedTags(c),
	})
	// A site created by hand in NetBox: no managed tag, must be protected.
	f.seed("dcim", "sites", client.Object{
		"name": "Manual DC", "slug": "manual-dc", "status": "active",
	})

	// Reconcile the desired set: only berlin-dc remains declared.
	fr := NewFoundationReconciler(c)
	desired := []*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Status: "active"}}
	if err := fr.ReconcileSites(desired); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}

	if err := c.Prune([]client.PruneTarget{{App: "dcim", Endpoint: "sites"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	// old-dc (managed orphan) is gone; berlin-dc (declared) and manual-dc
	// (unmanaged) survive.
	got := map[string]bool{}
	for _, obj := range f.objects("dcim", "sites") {
		got[obj["slug"].(string)] = true
	}
	if got["old-dc"] {
		t.Errorf("managed orphan old-dc was not pruned: %v", got)
	}
	if !got["berlin-dc"] {
		t.Errorf("declared site berlin-dc was wrongly pruned: %v", got)
	}
	if !got["manual-dc"] {
		t.Errorf("unmanaged site manual-dc was wrongly pruned: %v", got)
	}

	if summary := c.Recorder().Summary(); summary.Delete != 1 {
		t.Errorf("recorder Delete = %d, expected exactly 1 (old-dc)", summary.Delete)
	}
}
