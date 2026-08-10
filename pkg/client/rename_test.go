// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
)

// renameServer is a NetBox stand-in that answers filters out of a fixed table,
// keyed by the query string, so a test can say exactly what exists under the
// old and the new identity.
func renameServer(t *testing.T, byQuery map[string][]Object) (*NetBoxClient, *[]string) {
	t.Helper()
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		// Filter adds its own pagination; the table is keyed on the lookup.
		q := r.URL.Query()
		q.Del("limit")
		results := byQuery[q.Encode()]
		if results == nil {
			results = []Object{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"count": len(results), "results": results,
		})
	}))
	t.Cleanup(srv.Close)
	return newTestClient(srv), &queries
}

func managed() []interface{} {
	return []interface{}{map[string]interface{}{"id": 1, "slug": constants.ManagedTagSlug}}
}

func TestRenameLookupUsesPreviousIdentityWhenOnlyItExists(t *testing.T) {
	c, _ := renameServer(t, map[string][]Object{
		"slug=old": {{"id": 7, "slug": "old", "tags": managed()}},
	})

	got, err := c.RenamedLookup("dcim", "sites", "FRA",
		map[string]interface{}{"slug": "new"}, "slug", "old")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["slug"] != "old" {
		t.Errorf("lookup = %v, want the previous identity so Apply finds and renames it", got)
	}
}

func TestRenameLookupPrefersCurrentIdentityOnceRenamed(t *testing.T) {
	c, _ := renameServer(t, map[string][]Object{
		"slug=new": {{"id": 7, "slug": "new", "tags": managed()}},
	})

	got, err := c.RenamedLookup("dcim", "sites", "FRA",
		map[string]interface{}{"slug": "new"}, "slug", "old")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["slug"] != "new" {
		t.Errorf("lookup = %v, want the current identity: the rename is already applied", got)
	}
}

// The rename must not reach for an object that this tool does not manage:
// rename_from asserts something about an object's past, and a typo in it can
// name a real, unrelated object.
func TestRenameLookupRefusesUntaggedObject(t *testing.T) {
	c, _ := renameServer(t, map[string][]Object{
		"slug=old": {{"id": 7, "slug": "old"}}, // no tags
	})

	_, err := c.RenamedLookup("dcim", "sites", "FRA",
		map[string]interface{}{"slug": "new"}, "slug", "old")
	if err == nil {
		t.Fatal("expected an error when renaming an unmanaged object")
	}
	if !strings.Contains(err.Error(), "not managed") {
		t.Errorf("error = %q, want it to say the object is not managed", err)
	}
}

// Component templates cannot carry tags at all, so the ownership check has to
// be skipped for them rather than failing every template rename.
func TestRenameLookupSkipsTagCheckOnUntaggableEndpoint(t *testing.T) {
	c, _ := renameServer(t, map[string][]Object{
		"device_type_id=3&name=old": {{"id": 7, "name": "old"}},
	})

	got, err := c.RenamedLookup("dcim", "interface-templates", "eth0",
		map[string]interface{}{"device_type_id": 3, "name": "new"}, "name", "old")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["name"] != "old" {
		t.Errorf("lookup = %v, want the previous identity", got)
	}
}

func TestRenameLookupRejectsAmbiguousPreviousIdentity(t *testing.T) {
	c, _ := renameServer(t, map[string][]Object{
		"name=old": {
			{"id": 7, "name": "old", "tags": managed()},
			{"id": 8, "name": "old", "tags": managed()},
		},
	})

	_, err := c.RenamedLookup("ipam", "vrfs", "mgmt",
		map[string]interface{}{"name": "new"}, "name", "old")
	if err == nil {
		t.Fatal("expected an error when rename_from matches several objects")
	}
	if !strings.Contains(err.Error(), "2 objects") {
		t.Errorf("error = %q, want it to report how many matched", err)
	}
}

// Nothing under either identity is the ordinary first-time create, and a
// rename_from left behind after it was applied. Neither is an error.
func TestRenameLookupFallsBackToCurrentWhenNothingMatches(t *testing.T) {
	c, _ := renameServer(t, nil)

	got, err := c.RenamedLookup("dcim", "sites", "FRA",
		map[string]interface{}{"slug": "new"}, "slug", "gone")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["slug"] != "new" {
		t.Errorf("lookup = %v, want a normal create under the current identity", got)
	}
}

// An undeclared rename_from must cost nothing — no extra round trip per object.
func TestRenameLookupWithoutDeclarationQueriesNothing(t *testing.T) {
	c, queries := renameServer(t, nil)

	current := map[string]interface{}{"slug": "fra"}
	got, err := c.RenamedLookup("dcim", "sites", "FRA", current, "slug", nil)
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["slug"] != "fra" {
		t.Errorf("lookup = %v, want it returned unchanged", got)
	}
	if len(*queries) != 0 {
		t.Errorf("made %d request(s) for an object that declares no rename: %v", len(*queries), *queries)
	}
}

// Declaring the value the object already has is a no-op, not a lookup.
func TestRenameLookupIgnoresSelfRename(t *testing.T) {
	c, queries := renameServer(t, nil)

	got, err := c.RenamedLookup("dcim", "sites", "FRA",
		map[string]interface{}{"slug": "fra"}, "slug", "fra")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["slug"] != "fra" || len(*queries) != 0 {
		t.Errorf("lookup = %v after %d queries, want an untouched lookup and no requests", got, len(*queries))
	}
}

// A lookup scoped by an object that only exists as a plan (--dry-run against an
// empty NetBox, where a planned object has id 0) can never match, so it must not
// be queried — NetBox rejects site_id=0 outright.
func TestRenameLookupSkipsUnresolvedScope(t *testing.T) {
	c, queries := renameServer(t, nil)

	current := map[string]interface{}{"site_id": 0, "name": "Rack 01"}
	got, err := c.RenamedLookup("dcim", "racks", "Rack 01", current, "name", "Rakc 01")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["name"] != "Rack 01" || len(*queries) != 0 {
		t.Errorf("lookup = %v after %d queries, want the current lookup and no requests", got, len(*queries))
	}
}

// A rename changes what an object is called, not where it lives: the scope keys
// must survive into the previous-identity lookup untouched.
func TestRenameLookupPreservesScope(t *testing.T) {
	c, _ := renameServer(t, map[string][]Object{
		"name=Rakc+01&site_id=4": {{"id": 7, "name": "Rakc 01", "tags": managed()}},
	})

	got, err := c.RenamedLookup("dcim", "racks", "Rack 01",
		map[string]interface{}{"site_id": 4, "name": "Rack 01"}, "name", "Rakc 01")
	if err != nil {
		t.Fatalf("RenamedLookup() error = %v", err)
	}
	if got["name"] != "Rakc 01" {
		t.Errorf("name = %v, want the previous name", got["name"])
	}
	if got["site_id"] != 4 {
		t.Errorf("site_id = %v, want the scope carried over unchanged", got["site_id"])
	}
}

func TestSlugifiedRename(t *testing.T) {
	if got := SlugifiedRename("Dell Inc."); got != "dell-inc" {
		t.Errorf("SlugifiedRename(%q) = %v, want dell-inc", "Dell Inc.", got)
	}
	if got := SlugifiedRename("already-a-slug"); got != "already-a-slug" {
		t.Errorf("SlugifiedRename() = %v, want the slug unchanged", got)
	}
	if got := SlugifiedRename(""); got != nil {
		t.Errorf("SlugifiedRename(\"\") = %v, want nil so it reads as undeclared", got)
	}
}

func TestHasManagedTagAcceptsBothTagShapes(t *testing.T) {
	c := &NetBoxClient{managedTagID: 42}

	nested := Object{"tags": []interface{}{map[string]interface{}{"id": 42, "slug": constants.ManagedTagSlug}}}
	if !c.hasManagedTag(nested) {
		t.Error("nested tag object with the managed slug was not recognised")
	}
	byID := Object{"tags": []interface{}{float64(42)}}
	if !c.hasManagedTag(byID) {
		t.Error("bare managed tag ID was not recognised")
	}
	other := Object{"tags": []interface{}{map[string]interface{}{"id": 9, "slug": "something-else"}}}
	if c.hasManagedTag(other) {
		t.Error("an unrelated tag was treated as the managed tag")
	}
	if c.hasManagedTag(Object{}) {
		t.Error("an object with no tags was treated as managed")
	}
}
