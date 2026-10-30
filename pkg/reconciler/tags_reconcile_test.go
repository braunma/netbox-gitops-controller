// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"sort"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// storedTagIDs returns the tag IDs an object holds in the fake, sorted.
func storedTagIDs(obj client.Object) []int {
	var ids []int
	tags, _ := obj["tags"].([]interface{})
	for _, t := range tags {
		if id := utils.GetIDFromObject(t); id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func sameIDs(got []int, want ...int) bool {
	sort.Ints(want)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestDeclaredTagsAreSyncedExactly verifies that an object's declared tags
// reach NetBox next to the managed tag, are idempotent, and that removing one
// from the YAML removes it from NetBox.
func TestDeclaredTagsAreSyncedExactly(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)
	managed := c.ManagedTagID()

	// Defined in the same run as the site that uses them, by slug and by name.
	if err := fr.ReconcileTags([]*models.Tag{
		{Name: "Production", Slug: "production", Color: "f44336"},
		{Name: "Tier 1", Slug: "tier-1", Color: "ff9800"},
	}); err != nil {
		t.Fatalf("ReconcileTags() error = %v", err)
	}
	tagIDs := map[string]int{}
	for _, tag := range f.objects("extras", "tags") {
		tagIDs[tag["slug"].(string)] = utils.GetIDFromObject(tag)
	}

	sites := []*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Tags: []string{"production", "Tier 1"}}}
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	site := f.objects("dcim", "sites")[0]
	if got := storedTagIDs(site); !sameIDs(got, managed, tagIDs["production"], tagIDs["tier-1"]) {
		t.Fatalf("site tags = %v, want gitops, production and tier-1", got)
	}

	f.resetMutations()
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)

	sites[0].Tags = []string{"production"}
	if err := fr.ReconcileSites(sites); err != nil {
		t.Fatalf("ReconcileSites() third run error = %v", err)
	}
	f.requireMutationCount(t, 1)
	if got := storedTagIDs(f.objects("dcim", "sites")[0]); !sameIDs(got, managed, tagIDs["production"]) {
		t.Errorf("site tags = %v, want tier-1 removed", got)
	}
}

// TestUnknownTagIsLeftOff verifies that a tag defined nowhere does not fail
// the object: it is created with the tags that do resolve.
func TestUnknownTagIsLeftOff(t *testing.T) {
	f, c := newFakeNetBox(t)
	fr := NewFoundationReconciler(c)

	if err := fr.ReconcileSites([]*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Tags: []string{"no-such-tag"}}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if got := storedTagIDs(f.objects("dcim", "sites")[0]); !sameIDs(got, c.ManagedTagID()) {
		t.Errorf("site tags = %v, want only the managed tag", got)
	}
}

// TestLAGBindingKeepsMemberTags verifies that binding a member into its LAG,
// a second write to the member, does not strip the member's declared tags.
func TestLAGBindingKeepsMemberTags(t *testing.T) {
	f, c := newFakeNetBox(t)
	seedDeviceFoundation(t, f, c)
	tag := f.seed("extras", "tags", client.Object{"name": "Uplink", "slug": "uplink"})
	tagID := utils.GetIDFromObject(tag)

	member := testInterface("eth0", "10gbase-x-sfpp")
	member.Tags = []string{"uplink"}
	lag := testInterface("bond0", "lag")
	lag.Members = []string{"eth0"}

	dr := NewDeviceReconciler(c)
	if err := dr.ReconcileDevices([]*models.DeviceConfig{testDevice("srv-01", withInterfaces(member, lag))}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	for _, iface := range f.objects("dcim", "interfaces") {
		if iface["name"] != "eth0" {
			continue
		}
		if got := storedTagIDs(iface); !sameIDs(got, c.ManagedTagID(), tagID) {
			t.Errorf("eth0 tags = %v, want gitops and uplink after LAG binding", got)
		}
		if utils.GetIDFromObject(iface["lag"]) == 0 {
			t.Error("eth0 was not bound into bond0")
		}
		return
	}
	t.Fatal("eth0 was not created")
}
