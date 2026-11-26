// SPDX-License-Identifier: Apache-2.0

package client

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// componentIndex holds snapshots of the components that hang off one parent
// object (the interfaces of a device, the rear port templates of a device
// type, ...), so that reconciling them does not cost one lookup request per
// component.
//
// Without it, Apply asks NetBox "does <parent>/<name> exist?" once for every
// interface, port and template it reconciles. A device with 54 interfaces cost
// 54 sequential GETs before anything was compared; listing the device's
// interfaces once answers all of them.
//
// The snapshot only ever replaces a lookup it can answer exactly as NetBox
// would, and steps aside for everything else:
//
//   - It answers only a lookup of the exact form {<parentKey>: <id>, "name": X}
//     for a loaded scope. Anything else goes to NetBox as before.
//   - It answers only when exactly one object in the snapshot has that exact
//     name. A miss is never taken to mean "does not exist": the lookup falls
//     back to a live request, so a snapshot can never cause a duplicate create.
//   - Any change this client makes to an object (PATCH, PUT, DELETE) removes
//     that object from every snapshot, so a later lookup of it is live again
//     and never diffs against a value the run itself has already changed.
//     Objects this run creates are not added: the next lookup of one is live.
//
// Snapshots are scoped to the reconciliation of one parent and released when
// it is done; they are not a cache across the run.
type componentIndex struct {
	mu sync.Mutex
	// scopes is keyed by "app/endpoint", then by "parentKey=id".
	scopes map[string]map[string]*componentScope
}

type componentScope struct {
	parentKey string
	parentID  int
	// all is the list as NetBox returned it when the snapshot was taken.
	all []Object
	// byName holds the objects still safe to answer a lookup with: those the
	// run has not changed since the snapshot.
	byName map[string][]Object
}

func scopeID(parentKey string, parentID int) string {
	return parentKey + "=" + strconv.Itoa(parentID)
}

func (ci *componentIndex) put(app, endpoint, parentKey string, parentID int, objs []Object) {
	scope := &componentScope{
		parentKey: parentKey,
		parentID:  parentID,
		all:       objs,
		byName:    make(map[string][]Object, len(objs)),
	}
	for _, obj := range objs {
		if name, ok := obj["name"].(string); ok {
			scope.byName[name] = append(scope.byName[name], obj)
		}
	}

	ci.mu.Lock()
	defer ci.mu.Unlock()
	if ci.scopes == nil {
		ci.scopes = make(map[string]map[string]*componentScope)
	}
	key := app + "/" + endpoint
	if ci.scopes[key] == nil {
		ci.scopes[key] = make(map[string]*componentScope)
	}
	ci.scopes[key][scopeID(parentKey, parentID)] = scope
}

func (ci *componentIndex) release(app, endpoint, parentKey string, parentID int) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	key := app + "/" + endpoint
	delete(ci.scopes[key], scopeID(parentKey, parentID))
	if len(ci.scopes[key]) == 0 {
		delete(ci.scopes, key)
	}
}

// lookup answers a {parentKey: id, "name": X} lookup from a loaded snapshot.
// ok is false whenever the snapshot cannot answer with certainty; the caller
// then asks NetBox.
func (ci *componentIndex) lookup(app, endpoint string, filters map[string]interface{}) ([]Object, bool) {
	if len(filters) != 2 {
		return nil, false
	}
	name, ok := filters["name"].(string)
	if !ok {
		return nil, false
	}

	ci.mu.Lock()
	defer ci.mu.Unlock()
	for _, scope := range ci.scopes[app+"/"+endpoint] {
		parent, ok := filters[scope.parentKey]
		if !ok || fmt.Sprintf("%v", parent) != strconv.Itoa(scope.parentID) {
			continue
		}
		if matches := scope.byName[name]; len(matches) == 1 {
			return []Object{matches[0]}, true
		}
		return nil, false
	}
	return nil, false
}

// all returns the snapshot of a scope as it was taken.
func (ci *componentIndex) all(app, endpoint, parentKey string, parentID int) ([]Object, bool) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	scope, ok := ci.scopes[app+"/"+endpoint][scopeID(parentKey, parentID)]
	if !ok {
		return nil, false
	}
	return scope.all, true
}

// forget removes an object from every snapshot of its endpoint, so the next
// lookup of it goes to NetBox.
func (ci *componentIndex) forget(app, endpoint string, id int) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	for _, scope := range ci.scopes[app+"/"+endpoint] {
		for name, objs := range scope.byName {
			kept := objs[:0:0]
			for _, obj := range objs {
				if utils.GetIDFromObject(obj) != id {
					kept = append(kept, obj)
				}
			}
			if len(kept) == 0 {
				delete(scope.byName, name)
			} else {
				scope.byName[name] = kept
			}
		}
	}
}

// forgetPath is called after a successful write to an object's detail path
// (/api/{app}/{endpoint}/{id}/).
func (ci *componentIndex) forgetPath(path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" {
		return
	}
	id, err := strconv.Atoi(parts[3])
	if err != nil {
		return
	}
	ci.forget(parts[1], parts[2], id)
}

// PreloadComponents lists every object of app/endpoint that belongs to one
// parent (parentKey is the filter that selects it, e.g. "device_id") in a
// single request, and lets Apply and FindComponent answer the per-component
// lookups of that parent from the result. Call ReleaseComponents when the
// parent is done.
//
// It is an optimisation only. If the listing fails, nothing is loaded and
// every lookup goes to NetBox exactly as it would without it; the failure is
// not an error for the run. A parent that exists only as a plan (id 0) is
// never loaded.
func (c *NetBoxClient) PreloadComponents(app, endpoint, parentKey string, parentID int) {
	if parentID <= 0 {
		return
	}
	objs, err := c.Filter(app, endpoint, map[string]interface{}{parentKey: parentID})
	if err != nil {
		c.logger.Debug("  → could not preload %s for %s=%d, looking them up one by one: %v",
			endpoint, parentKey, parentID, err)
		return
	}
	c.components.put(app, endpoint, parentKey, parentID, objs)
}

// ReleaseComponents drops a snapshot taken by PreloadComponents.
func (c *NetBoxClient) ReleaseComponents(app, endpoint, parentKey string, parentID int) {
	c.components.release(app, endpoint, parentKey, parentID)
}

// PreloadedComponents returns the list PreloadComponents fetched for a parent,
// as NetBox returned it at that time. ok is false when it is not loaded.
func (c *NetBoxClient) PreloadedComponents(app, endpoint, parentKey string, parentID int) ([]Object, bool) {
	return c.components.all(app, endpoint, parentKey, parentID)
}

// FindComponent is Filter for a {parentKey: id, "name": X} lookup: it is
// answered from a preloaded snapshot when that can be done exactly, and from
// NetBox otherwise.
func (c *NetBoxClient) FindComponent(app, endpoint string, lookup map[string]interface{}) ([]Object, error) {
	if objs, ok := c.components.lookup(app, endpoint, lookup); ok {
		return objs, nil
	}
	return c.Filter(app, endpoint, lookup)
}
