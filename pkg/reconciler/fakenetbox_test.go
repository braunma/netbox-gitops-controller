// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// fakeNetBox is an in-memory NetBox API for reconciler tests. It supports
// filtered list, get-by-ID, create, partial update, and delete on any
// /api/{app}/{endpoint}/ path, and records every mutating request so tests
// can assert exactly what a reconciler did.
//
// Like real NetBox, creating or deleting a cable sets or clears the
// "cable" reference on the terminated ports — the cable reconciler's
// idempotency depends on that side effect.
type fakeNetBox struct {
	mu        sync.Mutex
	nextID    int
	store     map[string][]client.Object // keyed by "app/endpoint"
	mutations []recordedRequest
	srv       *httptest.Server
}

type recordedRequest struct {
	method string
	path   string
	body   map[string]interface{}
}

// newFakeNetBox starts a fake server and connects a real NetBoxClient to
// it. NewClient ensures the managed tag against the fake, so the mutation
// log is cleared afterwards: tests only see their own reconciler traffic.
func newFakeNetBox(t *testing.T) (*fakeNetBox, *client.NetBoxClient) {
	t.Helper()
	f := &fakeNetBox{store: make(map[string][]client.Object)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)

	c, err := client.NewClient(f.srv.URL, "test-token", false)
	if err != nil {
		t.Fatalf("NewClient() against fake NetBox: %v", err)
	}
	f.resetMutations()
	return f, c
}

func (f *fakeNetBox) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "api" {
		http.Error(w, `{"detail": "bad path"}`, http.StatusNotFound)
		return
	}
	key := parts[1] + "/" + parts[2]

	id := 0
	hasID := false
	if len(parts) == 4 {
		var err error
		if id, err = strconv.Atoi(parts[3]); err != nil {
			http.Error(w, `{"detail": "bad id"}`, http.StatusNotFound)
			return
		}
		hasID = true
	}

	var body map[string]interface{}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if r.Method != "GET" {
		f.mutations = append(f.mutations, recordedRequest{r.Method, r.URL.Path, body})
	}

	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == "GET" && hasID:
		if obj := f.findLocked(key, id); obj != nil {
			json.NewEncoder(w).Encode(obj)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"detail": "Not found."}`)

	case r.Method == "GET":
		query := r.URL.Query()
		tagSlug := query.Get("tag")
		results := []client.Object{}
		for _, obj := range f.store[key] {
			if !matchesFilters(obj, query) {
				continue
			}
			if tagSlug != "" && !f.objectHasTagSlugLocked(obj, tagSlug) {
				continue
			}
			results = append(results, obj)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"results": results, "next": nil})

	case r.Method == "POST":
		f.nextID++
		obj := client.Object{"id": f.nextID}
		for k, v := range body {
			obj[k] = v
		}
		f.store[key] = append(f.store[key], obj)
		if key == "dcim/cables" {
			f.setCableRefLocked(obj, map[string]interface{}{"id": f.nextID})
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(obj)

	case r.Method == "PATCH" || r.Method == "PUT":
		obj := f.findLocked(key, id)
		if obj == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"detail": "Not found."}`)
			return
		}
		for k, v := range body {
			obj[k] = v
		}
		json.NewEncoder(w).Encode(obj)

	case r.Method == "DELETE":
		if key == "dcim/cables" {
			if cable := f.findLocked(key, id); cable != nil {
				f.setCableRefLocked(cable, nil)
			}
		}
		objs := f.store[key]
		for i, obj := range objs {
			if utils.GetIDFromObject(obj) == id {
				f.store[key] = append(objs[:i], objs[i+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"detail": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// matchesFilters applies NetBox-style query filters to an object:
// "id" matches the object ID, "{field}_id" matches the ID of a (possibly
// nested) reference field, "{field}_id=null" matches a missing reference,
// and anything else is a direct field comparison. Pagination params are
// ignored.
func matchesFilters(obj client.Object, query url.Values) bool {
	for k, vals := range query {
		if k == "limit" || k == "page" || k == "offset" || k == "tag" {
			// "tag" is a slug filter resolved against the tag store by the
			// caller (objectHasTagSlugLocked), not a plain field comparison.
			continue
		}
		want := vals[0]

		if k == "id" {
			if strconv.Itoa(utils.GetIDFromObject(obj)) != want {
				return false
			}
			continue
		}

		if val, ok := obj[k]; ok {
			if fmt.Sprintf("%v", val) != want {
				return false
			}
			continue
		}

		if base, isRef := strings.CutSuffix(k, "_id"); isRef {
			ref := obj[base]
			if want == "null" {
				if ref != nil {
					return false
				}
				continue
			}
			if ref == nil || strconv.Itoa(utils.GetIDFromObject(ref)) != want {
				return false
			}
			continue
		}

		return false
	}
	return true
}

// terminationStoreKeys maps cable termination object types to the store
// key of the corresponding port endpoint.
var terminationStoreKeys = map[string]string{
	"dcim.interface": "dcim/interfaces",
	"dcim.frontport": "dcim/front-ports",
	"dcim.rearport":  "dcim/rear-ports",
}

// setCableRefLocked sets (or clears, when ref is nil) the "cable" field
// on every port terminated by the given cable.
func (f *fakeNetBox) setCableRefLocked(cable client.Object, ref interface{}) {
	for _, side := range []string{"a_terminations", "b_terminations"} {
		terms, ok := cable[side].([]interface{})
		if !ok {
			continue
		}
		for _, term := range terms {
			tm, ok := term.(map[string]interface{})
			if !ok {
				continue
			}
			objType, _ := tm["object_type"].(string)
			storeKey, ok := terminationStoreKeys[objType]
			if !ok {
				continue
			}
			if port := f.findLocked(storeKey, utils.GetIDFromObject(tm["object_id"])); port != nil {
				port["cable"] = ref
			}
		}
	}
}

// objectHasTagSlugLocked emulates NetBox's ?tag=<slug> filter: it resolves
// the slug to a tag ID via the tag store, then reports whether the object's
// "tags" reference that ID. Objects are created with integer tag IDs and
// seeded with nested tag objects, both of which GetIDFromObject handles.
func (f *fakeNetBox) objectHasTagSlugLocked(obj client.Object, slug string) bool {
	tagID := 0
	for _, t := range f.store["extras/tags"] {
		if s, _ := t["slug"].(string); s == slug {
			tagID = utils.GetIDFromObject(t)
			break
		}
	}
	if tagID == 0 {
		return false
	}

	tags, ok := obj["tags"].([]interface{})
	if !ok {
		return false
	}
	for _, t := range tags {
		if utils.GetIDFromObject(t) == tagID {
			return true
		}
	}
	return false
}

func (f *fakeNetBox) findLocked(key string, id int) client.Object {
	for _, obj := range f.store[key] {
		if utils.GetIDFromObject(obj) == id {
			return obj
		}
	}
	return nil
}

// seed inserts an object directly into the store, assigning an ID if none
// is set, and returns it.
func (f *fakeNetBox) seed(app, endpoint string, obj client.Object) client.Object {
	f.mu.Lock()
	defer f.mu.Unlock()

	if id := utils.GetIDFromObject(obj["id"]); id == 0 {
		f.nextID++
		obj["id"] = f.nextID
	} else if id > f.nextID {
		f.nextID = id
	}
	key := app + "/" + endpoint
	f.store[key] = append(f.store[key], obj)
	return obj
}

// objects returns a copy of the stored object list for an endpoint.
func (f *fakeNetBox) objects(app, endpoint string) []client.Object {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]client.Object{}, f.store[app+"/"+endpoint]...)
}

// mutationLog returns a copy of all mutating requests received so far.
func (f *fakeNetBox) mutationLog() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest{}, f.mutations...)
}

func (f *fakeNetBox) resetMutations() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutations = nil
}

// requireMutationCount fails the test unless exactly n mutating requests
// were recorded, and returns them.
func (f *fakeNetBox) requireMutationCount(t *testing.T, n int) []recordedRequest {
	t.Helper()
	muts := f.mutationLog()
	if len(muts) != n {
		t.Fatalf("expected %d mutating request(s), got %d: %+v", n, len(muts), muts)
	}
	return muts
}
