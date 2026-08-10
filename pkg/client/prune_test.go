// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// pruneTestServer is a tiny in-memory NetBox supporting the two operations
// Prune needs: a tag-filtered GET list and DELETE by ID. It records every
// DELETE path so tests can assert exactly which orphans were removed.
type pruneTestServer struct {
	srv     *httptest.Server
	store   map[string][]Object // keyed by "app/endpoint"
	deletes []string
}

func newPruneTestServer(t *testing.T, dryRun bool) (*pruneTestServer, *NetBoxClient) {
	t.Helper()
	p := &pruneTestServer{store: make(map[string][]Object)}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(p.srv.Close)

	c := &NetBoxClient{
		baseURL:    p.srv.URL,
		token:      "test-token",
		httpClient: p.srv.Client(),
		logger:     utils.NewLogger(dryRun),
		dryRun:     dryRun,
	}
	c.tagManager = NewTagManager(c)
	return p, c
}

func (p *pruneTestServer) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /api/{app}/{endpoint}/ or /api/{app}/{endpoint}/{id}/
	key := parts[1] + "/" + parts[2]
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case "GET":
		// Only objects carrying the requested tag slug are returned, mirroring
		// NetBox's ?tag= filter.
		wantTag := r.URL.Query().Get("tag")
		results := []Object{}
		for _, obj := range p.store[key] {
			if wantTag == "" || objectHasTag(obj, wantTag) {
				results = append(results, obj)
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"results": results, "next": nil})

	case "DELETE":
		p.deletes = append(p.deletes, r.URL.Path)
		id, _ := strconv.Atoi(parts[3])
		objs := p.store[key]
		for i, obj := range objs {
			if utils.GetIDFromObject(obj) == id {
				p.store[key] = append(objs[:i], objs[i+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"detail":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// objectHasTag reports whether obj carries a tag with the given slug.
func objectHasTag(obj Object, slug string) bool {
	tags, ok := obj["tags"].([]interface{})
	if !ok {
		return false
	}
	for _, tag := range tags {
		if tm, ok := tag.(map[string]interface{}); ok {
			if s, ok := tm["slug"].(string); ok && s == slug {
				return true
			}
		}
	}
	return false
}

// managedObj builds a stored object carrying the gitops managed tag.
func managedObj(id int, fields map[string]interface{}) Object {
	obj := Object{
		"id":   float64(id),
		"tags": []interface{}{map[string]interface{}{"slug": "gitops"}},
	}
	for k, v := range fields {
		obj[k] = v
	}
	return obj
}

// TestApplyMarksObjectsSeen verifies the wiring between Apply and Prune:
// both the create path (newly created object) and the update/no-op path
// (matched existing object) must mark the object's ID as seen so a later
// Prune does not delete it.
func TestApplyMarksObjectsSeen(t *testing.T) {
	// Create path: lookup matches nothing, server assigns id 999.
	ats := newApplyTestServer(t, nil)
	c := newApplyTestClient(ats.srv, 7)
	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc"}); err != nil {
		t.Fatalf("Apply() create error = %v", err)
	}
	if seen := c.seenIDs("dcim", "sites"); !seen[999] {
		t.Errorf("created object (id 999) not marked seen: %v", seen)
	}

	// Update/no-op path: lookup matches an existing object with id 42.
	ats2 := newApplyTestServer(t, []Object{{
		"id": float64(42), "name": "Munich DC", "slug": "munich-dc",
		"tags": []interface{}{map[string]interface{}{"id": float64(7)}},
	}})
	c2 := newApplyTestClient(ats2.srv, 7)
	if _, err := c2.Apply("dcim", "sites",
		map[string]interface{}{"slug": "munich-dc"},
		map[string]interface{}{"name": "Munich DC", "slug": "munich-dc"}); err != nil {
		t.Fatalf("Apply() no-op error = %v", err)
	}
	if seen := c2.seenIDs("dcim", "sites"); !seen[42] {
		t.Errorf("matched object (id 42) not marked seen: %v", seen)
	}
}

// TestPruneDeletesOnlyOrphans verifies the core behavior: a managed object
// that was reconciled this run is kept, while a managed object that was not
// is deleted.
func TestPruneDeletesOnlyOrphans(t *testing.T) {
	p, c := newPruneTestServer(t, false)
	p.store["dcim/sites"] = []Object{
		managedObj(1, map[string]interface{}{"name": "Kept", "slug": "kept"}),
		managedObj(2, map[string]interface{}{"name": "Orphan", "slug": "orphan"}),
	}

	// Pretend site 1 was reconciled this run; site 2 was not.
	c.markSeen("dcim", "sites", 1)

	if err := c.Prune([]PruneTarget{{App: "dcim", Endpoint: "sites"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	if len(p.deletes) != 1 || p.deletes[0] != "/api/dcim/sites/2/" {
		t.Fatalf("expected exactly DELETE /api/dcim/sites/2/, got %v", p.deletes)
	}

	summary := c.Recorder().Summary()
	if summary.Delete != 1 {
		t.Errorf("recorder Delete = %d, expected 1", summary.Delete)
	}
}

// TestPruneIgnoresUnmanagedObjects verifies objects without the gitops tag
// are never returned by the scoped query and therefore never deleted, even
// when they were not reconciled.
func TestPruneIgnoresUnmanagedObjects(t *testing.T) {
	p, c := newPruneTestServer(t, false)
	p.store["dcim/sites"] = []Object{
		// No gitops tag: manually created, must be protected.
		{"id": float64(9), "name": "Manual", "slug": "manual"},
	}

	if err := c.Prune([]PruneTarget{{App: "dcim", Endpoint: "sites"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if len(p.deletes) != 0 {
		t.Errorf("unmanaged object was deleted: %v", p.deletes)
	}
}

// TestPruneKeepSlugs verifies that slugs in KeepSlugs (e.g. the managed tag
// itself) are never pruned even when orphaned.
func TestPruneKeepSlugs(t *testing.T) {
	p, c := newPruneTestServer(t, false)
	p.store["extras/tags"] = []Object{
		managedObj(1, map[string]interface{}{"name": "GitOps Managed", "slug": "gitops"}),
		managedObj(2, map[string]interface{}{"name": "Stale", "slug": "stale"}),
	}

	if err := c.Prune([]PruneTarget{
		{App: "extras", Endpoint: "tags", KeepSlugs: []string{"gitops"}},
	}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	if len(p.deletes) != 1 || p.deletes[0] != "/api/extras/tags/2/" {
		t.Fatalf("expected only the stale tag deleted, got %v", p.deletes)
	}
}

// TestPruneProcessesTargetsInOrder verifies targets are pruned in the order
// given, which callers rely on to satisfy NetBox foreign-key constraints
// (children before parents).
func TestPruneProcessesTargetsInOrder(t *testing.T) {
	p, c := newPruneTestServer(t, false)
	p.store["dcim/devices"] = []Object{managedObj(1, map[string]interface{}{"name": "dev"})}
	p.store["dcim/sites"] = []Object{managedObj(2, map[string]interface{}{"name": "site", "slug": "site"})}

	if err := c.Prune([]PruneTarget{
		{App: "dcim", Endpoint: "devices"},
		{App: "dcim", Endpoint: "sites"},
	}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	want := []string{"/api/dcim/devices/1/", "/api/dcim/sites/2/"}
	if len(p.deletes) != 2 || p.deletes[0] != want[0] || p.deletes[1] != want[1] {
		t.Fatalf("delete order = %v, expected %v", p.deletes, want)
	}
}

// TestPruneSkipsIncompleteEndpoints verifies the safety guard: if
// reconciliation skipped a declared object at an endpoint (e.g. a missing
// dependency), Prune must not delete anything there — a declared object that
// failed to reconcile would otherwise look like an orphan.
func TestPruneSkipsIncompleteEndpoints(t *testing.T) {
	p, c := newPruneTestServer(t, false)
	p.store["ipam/vlans"] = []Object{
		// Not seen this run, but the VLAN phase was incomplete, so it may be a
		// declared object that merely failed to reconcile.
		managedObj(1, map[string]interface{}{"name": "vlan-100", "vid": float64(100)}),
	}
	c.MarkReconcileIncomplete("ipam", "vlans")

	if err := c.Prune([]PruneTarget{{App: "ipam", Endpoint: "vlans"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if len(p.deletes) != 0 {
		t.Errorf("prune deleted from an incomplete endpoint: %v", p.deletes)
	}
}

// TestPruneDryRunRecordsWithoutDeleting verifies that in dry-run mode the
// orphan is recorded as a planned delete but no DELETE reaches the server.
func TestPruneDryRunRecordsWithoutDeleting(t *testing.T) {
	p, c := newPruneTestServer(t, true)
	p.store["ipam/prefixes"] = []Object{
		managedObj(1, map[string]interface{}{"prefix": "10.0.0.0/24"}),
	}

	if err := c.Prune([]PruneTarget{{App: "ipam", Endpoint: "prefixes"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if len(p.deletes) != 0 {
		t.Errorf("dry-run prune issued DELETE(s): %v", p.deletes)
	}

	changes := c.Recorder().Changes()
	if len(changes) != 1 || changes[0].Action != ActionDelete {
		t.Fatalf("expected 1 recorded delete, got %+v", changes)
	}
	if changes[0].Object != "prefix=10.0.0.0/24" {
		t.Errorf("recorded label = %q, expected prefix=10.0.0.0/24", changes[0].Object)
	}
}
