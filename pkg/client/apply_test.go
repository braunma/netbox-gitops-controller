package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// applyTestServer is a minimal fake NetBox endpoint for exercising Apply.
// It serves a fixed filter result for GET and records every mutating
// request (method, path, decoded body) it receives.
type applyTestServer struct {
	srv           *httptest.Server
	filterResults []Object
	mutations     []recordedRequest
}

type recordedRequest struct {
	method string
	path   string
	body   map[string]interface{}
}

func newApplyTestServer(t *testing.T, filterResults []Object) *applyTestServer {
	t.Helper()
	ats := &applyTestServer{filterResults: filterResults}
	ats.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"results": ats.filterResults,
				"next":    nil,
			})
			return
		}

		var body map[string]interface{}
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}
		ats.mutations = append(ats.mutations, recordedRequest{r.Method, r.URL.Path, body})

		switch r.Method {
		case "POST":
			created := map[string]interface{}{"id": 999}
			for k, v := range body {
				created[k] = v
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(created)
		case "PATCH", "PUT":
			json.NewEncoder(w).Encode(body)
		case "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(ats.srv.Close)
	return ats
}

// newApplyTestClient wires up a client against the fake server without
// going through NewClient (which would call the real tag-ensure API).
func newApplyTestClient(srv *httptest.Server, managedTagID int) *NetBoxClient {
	c := &NetBoxClient{
		baseURL:      srv.URL,
		token:        "test-token",
		httpClient:   srv.Client(),
		logger:       utils.NewLogger(false),
		managedTagID: managedTagID,
	}
	c.tagManager = NewTagManager(c)
	return c
}

// TestApplyCreatesWhenMissing verifies the create path: when the lookup
// matches nothing, Apply must POST the payload with the managed tag injected.
func TestApplyCreatesWhenMissing(t *testing.T) {
	ats := newApplyTestServer(t, nil)
	c := newApplyTestClient(ats.srv, 7)

	obj, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc"})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(ats.mutations) != 1 {
		t.Fatalf("expected exactly 1 mutating request, got %d: %+v", len(ats.mutations), ats.mutations)
	}
	req := ats.mutations[0]
	if req.method != "POST" || req.path != "/api/dcim/sites/" {
		t.Errorf("expected POST /api/dcim/sites/, got %s %s", req.method, req.path)
	}
	if req.body["name"] != "Berlin DC" {
		t.Errorf("POST body name = %v, expected \"Berlin DC\"", req.body["name"])
	}
	if !reflect.DeepEqual(req.body["tags"], []interface{}{float64(7)}) {
		t.Errorf("POST body tags = %v, expected managed tag [7] to be injected", req.body["tags"])
	}
	if id := utils.GetIDFromObject(obj); id != 999 {
		t.Errorf("Apply() returned object with id %d, expected 999 (the created object)", id)
	}
}

// TestApplyUpdatesOnlyChangedFields verifies the update path: when the
// existing object differs, Apply must PATCH only the changed fields.
func TestApplyUpdatesOnlyChangedFields(t *testing.T) {
	existing := Object{
		"id":          float64(42),
		"name":        "Berlin DC",
		"slug":        "berlin-dc",
		"description": "old description",
		"tags":        []interface{}{map[string]interface{}{"id": float64(7)}},
	}
	ats := newApplyTestServer(t, []Object{existing})
	c := newApplyTestClient(ats.srv, 7)

	obj, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc", "description": "new description"})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(ats.mutations) != 1 {
		t.Fatalf("expected exactly 1 mutating request, got %d: %+v", len(ats.mutations), ats.mutations)
	}
	req := ats.mutations[0]
	if req.method != "PATCH" || req.path != "/api/dcim/sites/42/" {
		t.Errorf("expected PATCH /api/dcim/sites/42/, got %s %s", req.method, req.path)
	}
	want := map[string]interface{}{"description": "new description"}
	if !reflect.DeepEqual(req.body, want) {
		t.Errorf("PATCH body = %v, expected only the changed field %v", req.body, want)
	}
	if id := utils.GetIDFromObject(obj); id != 42 {
		t.Errorf("Apply() returned object with id %d, expected 42 (the existing object)", id)
	}
}

// TestApplyNoOpWhenInSync is the idempotency guarantee: re-applying a
// payload that matches the existing object must issue no mutating request.
func TestApplyNoOpWhenInSync(t *testing.T) {
	existing := Object{
		"id":          float64(42),
		"name":        "Berlin DC",
		"slug":        "berlin-dc",
		"description": "a description",
		"tags":        []interface{}{map[string]interface{}{"id": float64(7)}},
	}
	ats := newApplyTestServer(t, []Object{existing})
	c := newApplyTestClient(ats.srv, 7)

	obj, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc", "description": "a description"})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(ats.mutations) != 0 {
		t.Errorf("idempotency violated: in-sync Apply() issued %d mutating request(s): %+v",
			len(ats.mutations), ats.mutations)
	}
	if id := utils.GetIDFromObject(obj); id != 42 {
		t.Errorf("Apply() returned object with id %d, expected 42", id)
	}
}

// TestApplyNestedObjectComparison verifies that nested objects in the
// existing state (e.g. site: {id: 3, ...}) are compared by ID against the
// flat ID in the desired payload, so unchanged references do not PATCH.
func TestApplyNestedObjectComparison(t *testing.T) {
	existing := Object{
		"id":   float64(42),
		"name": "R01",
		"site": map[string]interface{}{"id": float64(3), "slug": "berlin-dc"},
		"tags": []interface{}{map[string]interface{}{"id": float64(7)}},
	}
	ats := newApplyTestServer(t, []Object{existing})
	c := newApplyTestClient(ats.srv, 7)

	if _, err := c.Apply("dcim", "racks",
		map[string]interface{}{"name": "R01"},
		map[string]interface{}{"name": "R01", "site": 3}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(ats.mutations) != 0 {
		t.Errorf("unchanged nested reference triggered %d mutating request(s): %+v",
			len(ats.mutations), ats.mutations)
	}

	// Changing the referenced ID must trigger an update.
	if _, err := c.Apply("dcim", "racks",
		map[string]interface{}{"name": "R01"},
		map[string]interface{}{"name": "R01", "site": 4}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(ats.mutations) != 1 || ats.mutations[0].method != "PATCH" {
		t.Fatalf("expected 1 PATCH for changed reference, got %+v", ats.mutations)
	}
	if !reflect.DeepEqual(ats.mutations[0].body, map[string]interface{}{"site": float64(4)}) {
		t.Errorf("PATCH body = %v, expected {site: 4}", ats.mutations[0].body)
	}
}

// TestApplyChoiceFieldNoOp verifies that NetBox choice/enum fields, which the
// API returns as {"value": ..., "label": ...} objects (with no "id"), are
// compared against the flat string we send. Before the fix these resolved to
// id 0, never matched the desired string, and were re-PATCHed on every run —
// the root cause of "everything updates on a second run with no changes".
func TestApplyChoiceFieldNoOp(t *testing.T) {
	existing := Object{
		"id":     float64(42),
		"name":   "Berlin DC",
		"slug":   "berlin-dc",
		"status": map[string]interface{}{"value": "active", "label": "Active"},
		"tags":   []interface{}{map[string]interface{}{"id": float64(7)}},
	}
	ats := newApplyTestServer(t, []Object{existing})
	c := newApplyTestClient(ats.srv, 7)

	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc", "status": "active"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(ats.mutations) != 0 {
		t.Errorf("unchanged choice field triggered %d mutating request(s): %+v",
			len(ats.mutations), ats.mutations)
	}

	// A genuinely changed choice value must still PATCH.
	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc", "status": "planned"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(ats.mutations) != 1 || ats.mutations[0].method != "PATCH" {
		t.Fatalf("expected 1 PATCH for changed choice value, got %+v", ats.mutations)
	}
	if !reflect.DeepEqual(ats.mutations[0].body, map[string]interface{}{"status": "planned"}) {
		t.Errorf("PATCH body = %v, expected {status: planned}", ats.mutations[0].body)
	}
}

// TestApplyErrorWhenObjectHasNoID verifies Apply fails loudly instead of
// PATCHing object ID 0 when the matched object has no usable ID.
func TestApplyErrorWhenObjectHasNoID(t *testing.T) {
	ats := newApplyTestServer(t, []Object{{"name": "Berlin DC", "slug": "berlin-dc"}})
	c := newApplyTestClient(ats.srv, 7)

	_, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "description": "changed"})
	if err == nil {
		t.Fatal("Apply() expected error for object without ID, got nil")
	}
	if !strings.Contains(err.Error(), "object has no ID") {
		t.Errorf("Apply() error = %q, expected it to mention \"object has no ID\"", err)
	}
	if len(ats.mutations) != 0 {
		t.Errorf("Apply() issued %d mutating request(s) despite missing ID: %+v",
			len(ats.mutations), ats.mutations)
	}
}

// TestApplyFilterErrorAborts verifies that a failed lookup aborts Apply
// before any create/update is attempted.
func TestApplyFilterErrorAborts(t *testing.T) {
	mutations := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"detail": "invalid filter"}`)
	}))
	defer srv.Close()

	c := newApplyTestClient(srv, 7)
	_, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC"})
	if err == nil {
		t.Fatal("Apply() expected error when filter fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to filter objects") {
		t.Errorf("Apply() error = %q, expected it to mention \"failed to filter objects\"", err)
	}
	if mutations != 0 {
		t.Errorf("Apply() issued %d mutating request(s) after a failed filter", mutations)
	}
}
