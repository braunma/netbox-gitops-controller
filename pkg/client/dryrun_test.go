package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// newDryRunServer fails the test if any mutating request reaches it and
// counts GETs so read-only behavior can still be asserted.
func newDryRunServer(t *testing.T, filterResults []Object) (*httptest.Server, *int) {
	t.Helper()
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("dry-run violated: server received %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gets++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"results": filterResults, "next": nil})
	}))
	t.Cleanup(srv.Close)
	return srv, &gets
}

func newDryRunClient(srv *httptest.Server) *NetBoxClient {
	c := &NetBoxClient{
		baseURL:    srv.URL,
		token:      "test-token",
		httpClient: srv.Client(),
		logger:     utils.NewLogger(true),
		dryRun:     true,
	}
	c.tagManager = NewTagManager(c)
	return c
}

// TestDryRunBlocksMutatingRequests is the core safety guarantee of
// --dry-run: Create, Update, Delete, and raw PUT requests must never
// reach the server.
func TestDryRunBlocksMutatingRequests(t *testing.T) {
	srv, _ := newDryRunServer(t, nil)
	c := newDryRunClient(srv)

	if _, err := c.Create("dcim", "sites", map[string]interface{}{"name": "x"}); err != nil {
		t.Errorf("Create() in dry-run = %v, expected nil", err)
	}
	if err := c.Update("dcim", "sites", 1, map[string]interface{}{"name": "y"}); err != nil {
		t.Errorf("Update() in dry-run = %v, expected nil", err)
	}
	if err := c.Delete("dcim", "sites", 1); err != nil {
		t.Errorf("Delete() in dry-run = %v, expected nil", err)
	}
	if _, err := c.Request("PUT", "/api/dcim/sites/1/", map[string]interface{}{"name": "z"}); err != nil {
		t.Errorf("Request(PUT) in dry-run = %v, expected nil", err)
	}
}

// TestDryRunStillAllowsReads verifies dry-run does not block GETs: the
// tool must still be able to read live state to compute its diff.
func TestDryRunStillAllowsReads(t *testing.T) {
	srv, gets := newDryRunServer(t, []Object{{"id": float64(1), "name": "Berlin DC"}})
	c := newDryRunClient(srv)

	objects, err := c.Filter("dcim", "sites", map[string]interface{}{"slug": "berlin-dc"})
	if err != nil {
		t.Fatalf("Filter() in dry-run = %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("Filter() returned %d objects, expected 1", len(objects))
	}
	if *gets != 1 {
		t.Errorf("server received %d GET request(s), expected 1", *gets)
	}
}

// TestApplyDryRunCreatePath runs the full Apply flow in dry-run mode for
// an object that does not exist yet: it must read, then skip the POST.
func TestApplyDryRunCreatePath(t *testing.T) {
	srv, gets := newDryRunServer(t, nil)
	c := newDryRunClient(srv)

	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc"}); err != nil {
		t.Fatalf("Apply() in dry-run = %v", err)
	}
	if *gets != 1 {
		t.Errorf("server received %d GET request(s), expected 1 (the lookup)", *gets)
	}
}

// TestApplyDryRunUpdatePath runs the full Apply flow in dry-run mode for
// an object that exists but differs: it must compute the diff, then skip
// the PATCH.
func TestApplyDryRunUpdatePath(t *testing.T) {
	existing := Object{
		"id":          float64(42),
		"name":        "Berlin DC",
		"slug":        "berlin-dc",
		"description": "old",
	}
	srv, _ := newDryRunServer(t, []Object{existing})
	c := newDryRunClient(srv)

	obj, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc", "description": "new"})
	if err != nil {
		t.Fatalf("Apply() in dry-run = %v", err)
	}
	if id := utils.GetIDFromObject(obj); id != 42 {
		t.Errorf("Apply() returned object with id %d, expected the existing object (42)", id)
	}
}

// TestSetDryRun verifies toggling dry-run switches between blocking and
// passing through mutating requests.
func TestSetDryRun(t *testing.T) {
	mutations := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if c.IsDryRun() {
		t.Error("IsDryRun() = true on a live client")
	}

	c.SetDryRun(true)
	if !c.IsDryRun() {
		t.Error("IsDryRun() = false after SetDryRun(true)")
	}
	if _, err := c.Create("dcim", "sites", map[string]interface{}{"name": "x"}); err != nil {
		t.Errorf("Create() in dry-run = %v, expected nil", err)
	}
	if mutations != 0 {
		t.Fatalf("dry-run client sent %d mutating request(s)", mutations)
	}

	c.SetDryRun(false)
	if _, err := c.Create("dcim", "sites", map[string]interface{}{"name": "x"}); err != nil {
		t.Errorf("Create() live = %v, expected nil", err)
	}
	if mutations != 1 {
		t.Errorf("live client sent %d mutating request(s), expected 1", mutations)
	}
}
