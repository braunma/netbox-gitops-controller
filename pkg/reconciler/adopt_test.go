// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// In adoption mode, reconciling an existing unmanaged object writes only the
// managed tag: no other field is touched, even when the YAML differs from what
// NetBox holds. This is the guarantee that makes a first sync over a populated
// instance structurally incapable of reformatting a field.
func TestAdoptWritesOnlyTheTag(t *testing.T) {
	f, c := newFakeNetBox(t)
	c.SetAdopt(true)

	// An existing site NetBox holds, untagged, with a description the YAML will
	// not restate.
	f.seed("dcim", "sites", client.Object{
		"name": "Berlin", "slug": "berlin",
		"status":      map[string]interface{}{"value": "active"},
		"description": "set in the UI",
		"tags":        []interface{}{},
	})
	f.resetMutations()

	fr := NewFoundationReconciler(c)
	if err := fr.ReconcileSites([]*models.Site{{Name: "Berlin", Slug: "berlin"}}); err != nil {
		t.Fatalf("ReconcileSites: %v", err)
	}

	muts := f.mutationLog()
	if len(muts) != 1 {
		t.Fatalf("expected exactly one write (the tag PATCH), got %d: %+v", len(muts), muts)
	}
	patch := muts[0]
	if patch.method != "PATCH" {
		t.Fatalf("expected a PATCH, got %s", patch.method)
	}
	// The only field written is tags.
	if len(patch.body) != 1 {
		t.Fatalf("adopt wrote more than the tag: %+v", patch.body)
	}
	if _, ok := patch.body["tags"]; !ok {
		t.Fatalf("adopt did not write tags: %+v", patch.body)
	}

	// The description NetBox held must be untouched.
	sites := f.objects("dcim", "sites")
	if got, _ := sites[0]["description"].(string); got != "set in the UI" {
		t.Fatalf("adopt overwrote description: %q", got)
	}
}

// A non-adopt sync of the same object updates the fields normally, proving the
// guard above is doing something.
func TestAdoptContrastNormalSyncUpdatesFields(t *testing.T) {
	f, c := newFakeNetBox(t)

	f.seed("dcim", "sites", client.Object{
		"name": "Berlin", "slug": "berlin",
		"status":      map[string]interface{}{"value": "planned"},
		"description": "old",
		"tags":        []interface{}{},
	})
	f.resetMutations()

	fr := NewFoundationReconciler(c)
	if err := fr.ReconcileSites([]*models.Site{{Name: "Berlin", Slug: "berlin", Description: "new"}}); err != nil {
		t.Fatalf("ReconcileSites: %v", err)
	}
	sites := f.objects("dcim", "sites")
	if got, _ := sites[0]["description"].(string); got != "new" {
		t.Fatalf("normal sync did not update description: %q", got)
	}
	_ = constants.ManagedTagSlug
}
