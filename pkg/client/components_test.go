// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// componentServer is a fake interfaces endpoint that filters by device_id and
// name the way NetBox does, and counts the requests it serves.
type componentServer struct {
	mu      sync.Mutex
	srv     *httptest.Server
	objects []Object
	nextID  int
	lists   []string // query strings of the GET list requests
	writes  []string // "METHOD path"
	failGET bool
}

func newComponentServer(t *testing.T, objects ...Object) (*componentServer, *NetBoxClient) {
	t.Helper()
	cs := &componentServer{objects: objects, nextID: 100}
	cs.srv = httptest.NewServer(http.HandlerFunc(cs.handle))
	t.Cleanup(cs.srv.Close)
	c := newTestClient(cs.srv)
	c.managedTagID = 1
	c.tagManager = NewTagManager(c)
	return cs, c
}

func (cs *componentServer) handle(w http.ResponseWriter, r *http.Request) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		cs.lists = append(cs.lists, r.URL.RawQuery)
		if cs.failGET {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"detail":"nope"}`))
			return
		}
		q := r.URL.Query()
		results := []Object{}
		for _, obj := range cs.objects {
			if d := q.Get("device_id"); d != "" && strconv.Itoa(int(obj["device"].(float64))) != d {
				continue
			}
			if n := q.Get("name"); n != "" && obj["name"] != n {
				continue
			}
			results = append(results, obj)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"results": results, "next": nil})
		return
	}

	cs.writes = append(cs.writes, r.Method+" "+r.URL.Path)
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	switch r.Method {
	case "POST":
		cs.nextID++
		obj := Object{"id": float64(cs.nextID)}
		for k, v := range body {
			obj[k] = v
		}
		cs.objects = append(cs.objects, obj)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(obj)
	case "PATCH":
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		id, _ := strconv.Atoi(parts[len(parts)-1])
		for _, obj := range cs.objects {
			if int(obj["id"].(float64)) == id {
				for k, v := range body {
					obj[k] = v
				}
				json.NewEncoder(w).Encode(obj)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func (cs *componentServer) counts() (lists, writes int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.lists), len(cs.writes)
}

func (cs *componentServer) add(obj Object) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.objects = append(cs.objects, obj)
}

func iface(id, device int, name, descr string) Object {
	return Object{
		"id": float64(id), "device": float64(device), "name": name, "description": descr,
		"tags": []interface{}{map[string]interface{}{"id": float64(1), "slug": "gitops"}},
	}
}

func ifacePayload(device int, name, descr string) map[string]interface{} {
	return map[string]interface{}{"device": device, "name": name, "description": descr}
}

func ifaceLookup(device int, name string) map[string]interface{} {
	return map[string]interface{}{"device_id": device, "name": name}
}

// The point of the snapshot: one list request answers every component lookup
// of the parent, and an in-sync component costs nothing more.
func TestPreloadAnswersApplyLookups(t *testing.T) {
	cs, c := newComponentServer(t,
		iface(1, 7, "eth0", "a"), iface(2, 7, "eth1", "b"), iface(3, 8, "eth0", "other device"))

	c.PreloadComponents("dcim", "interfaces", "device_id", 7)
	for _, name := range []string{"eth0", "eth1"} {
		descr := map[string]string{"eth0": "a", "eth1": "b"}[name]
		if _, err := c.Apply("dcim", "interfaces", ifaceLookup(7, name), ifacePayload(7, name, descr)); err != nil {
			t.Fatalf("Apply(%s) error = %v", name, err)
		}
	}
	lists, writes := cs.counts()
	if lists != 1 || writes != 0 {
		t.Fatalf("expected 1 list and no writes, got %d list(s) and %d write(s)", lists, writes)
	}
}

// A name missing from the snapshot must never be read as "does not exist":
// something may have created it since. The lookup goes to NetBox, which finds
// it, so nothing is created twice.
func TestPreloadMissFallsBackToNetBox(t *testing.T) {
	cs, c := newComponentServer(t, iface(1, 7, "eth0", "a"))
	c.PreloadComponents("dcim", "interfaces", "device_id", 7)
	cs.add(iface(2, 7, "eth1", "b")) // appears after the snapshot

	if _, err := c.Apply("dcim", "interfaces", ifaceLookup(7, "eth1"), ifacePayload(7, "eth1", "b")); err != nil {
		t.Fatalf("Apply error = %v", err)
	}
	lists, writes := cs.counts()
	if lists != 2 {
		t.Errorf("expected the miss to be looked up live (2 lists), got %d", lists)
	}
	if writes != 0 {
		t.Errorf("expected no write for an object that exists, got %v", cs.writes)
	}
}

// Once the run has changed an object, the snapshot no longer knows what it
// holds; the next lookup of it must be live, so a second Apply diffs against
// the real value and not the one from before the first.
func TestPreloadForgetsObjectsTheRunChanges(t *testing.T) {
	cs, c := newComponentServer(t, iface(1, 7, "eth0", "old"))
	c.PreloadComponents("dcim", "interfaces", "device_id", 7)

	if _, err := c.Apply("dcim", "interfaces", ifaceLookup(7, "eth0"), ifacePayload(7, "eth0", "new")); err != nil {
		t.Fatalf("first Apply error = %v", err)
	}
	// Against the stale snapshot this would PATCH description back to "new"
	// again; against NetBox it is in sync.
	if _, err := c.Apply("dcim", "interfaces", ifaceLookup(7, "eth0"), ifacePayload(7, "eth0", "new")); err != nil {
		t.Fatalf("second Apply error = %v", err)
	}
	lists, writes := cs.counts()
	if writes != 1 {
		t.Errorf("expected exactly one PATCH, got %v", cs.writes)
	}
	if lists != 2 {
		t.Errorf("expected the second lookup to be live (2 lists), got %d", lists)
	}
}

// The snapshot answers only the exact {parent, name} shape and the exact name;
// anything else is NetBox's to decide.
func TestPreloadOnlyAnswersExactLookups(t *testing.T) {
	cs, c := newComponentServer(t, iface(1, 7, "eth0", "a"))
	c.PreloadComponents("dcim", "interfaces", "device_id", 7)

	cases := []map[string]interface{}{
		{"device_id": 7, "name": "ETH0"},              // different case
		{"device_id": 7, "name": "eth0", "type": "x"}, // more keys
		{"device_id": 8, "name": "eth0"},              // other parent
		{"name": "eth0"},                              // no parent
	}
	for _, lookup := range cases {
		if _, err := c.FindComponent("dcim", "interfaces", lookup); err != nil {
			t.Fatalf("FindComponent(%v) error = %v", lookup, err)
		}
	}
	if lists, _ := cs.counts(); lists != 1+len(cases) {
		t.Errorf("expected every non-exact lookup to go live (%d lists), got %d", 1+len(cases), lists)
	}

	objs, err := c.FindComponent("dcim", "interfaces", ifaceLookup(7, "eth0"))
	if err != nil || len(objs) != 1 {
		t.Fatalf("FindComponent(exact) = %v, %v", objs, err)
	}
	if lists, _ := cs.counts(); lists != 1+len(cases) {
		t.Errorf("expected the exact lookup to come from the snapshot")
	}
}

// A failed preload is not an error: the lookups simply go to NetBox.
func TestPreloadFailureFallsBack(t *testing.T) {
	cs, c := newComponentServer(t, iface(1, 7, "eth0", "a"))
	cs.failGET = true
	c.PreloadComponents("dcim", "interfaces", "device_id", 7)
	if _, ok := c.PreloadedComponents("dcim", "interfaces", "device_id", 7); ok {
		t.Fatal("a failed preload must not leave a snapshot behind")
	}
	cs.failGET = false
	if _, err := c.Apply("dcim", "interfaces", ifaceLookup(7, "eth0"), ifacePayload(7, "eth0", "a")); err != nil {
		t.Fatalf("Apply error = %v", err)
	}
	if _, writes := cs.counts(); writes != 0 {
		t.Errorf("expected no writes, got %v", cs.writes)
	}
}

// After release, and for a parent that is only planned (id 0), nothing is
// answered from a snapshot.
func TestPreloadReleaseAndPlannedParent(t *testing.T) {
	cs, c := newComponentServer(t, iface(1, 7, "eth0", "a"))
	c.PreloadComponents("dcim", "interfaces", "device_id", 0)
	if lists, _ := cs.counts(); lists != 0 {
		t.Fatalf("a planned parent must not be listed, got %d list(s)", lists)
	}

	c.PreloadComponents("dcim", "interfaces", "device_id", 7)
	c.ReleaseComponents("dcim", "interfaces", "device_id", 7)
	if _, err := c.FindComponent("dcim", "interfaces", ifaceLookup(7, "eth0")); err != nil {
		t.Fatal(err)
	}
	if lists, _ := cs.counts(); lists != 2 {
		t.Errorf("expected a live lookup after release (2 lists), got %d", lists)
	}
}

func TestForgetPathIgnoresNonDetailPaths(t *testing.T) {
	var ci componentIndex
	ci.put("dcim", "interfaces", "device_id", 7, []Object{iface(1, 7, "eth0", "a")})
	for _, p := range []string{"/api/dcim/interfaces/", "/api/dcim/interfaces/x/", "/other/dcim/interfaces/1/"} {
		ci.forgetPath(p)
	}
	if _, ok := ci.lookup("dcim", "interfaces", ifaceLookup(7, "eth0")); !ok {
		t.Fatal("a non-detail path must not forget anything")
	}
	ci.forgetPath("/api/dcim/interfaces/1/")
	if _, ok := ci.lookup("dcim", "interfaces", ifaceLookup(7, "eth0")); ok {
		t.Fatal("a write to the object's detail path must forget it")
	}
	if all, _ := ci.all("dcim", "interfaces", "device_id", 7); len(all) != 1 {
		t.Fatal("forgetting must not alter the snapshot as taken")
	}
}
