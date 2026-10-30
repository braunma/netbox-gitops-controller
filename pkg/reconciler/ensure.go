// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// ensureSpec describes one object for ensure: the tail every simple reconcile
// loop shares once its payload is built. Payload building stays per-kind and
// explicit — only what happens after the payload exists is shared.
type ensureSpec struct {
	// kind is the human label for error wrapping, e.g. "site".
	kind string
	// name is the object's own identity, used in errors and rename logs.
	name string
	// lookup is the identity filter the object is found under.
	lookup map[string]interface{}
	// renameField is the identity field a declared rename_from refers to;
	// renameFrom is its previous value, nil when no rename is declared.
	renameField string
	renameFrom  interface{}
	payload     map[string]interface{}
	// tags are the object's declared tag slugs. ensure resolves them into the
	// payload; the managed tag is added by the client either way.
	tags []string
	// register, when set, is called with the applied object's ID (0 for an
	// object only planned in --dry-run) so the caller can seed the reference
	// cache for later phases. Registration differs per kind — global,
	// site-scoped, conditional — so a closure keeps ensure free of flags.
	register func(id int)
}

// ensure is the shared tail of the simple reconcilers: rename-aware lookup,
// apply, cache registration, error wrapping with the object's name. It
// returns the applied object's ID, which is 0 for an object only planned in
// --dry-run.
func ensure(c *client.NetBoxClient, app, endpoint string, spec ensureSpec) (int, error) {
	setTags(c, spec.payload, spec.tags, spec.kind, spec.name)
	lookup, err := c.RenamedLookup(app, endpoint, spec.name, spec.lookup, spec.renameField, spec.renameFrom)
	if err != nil {
		return 0, err
	}
	obj, err := c.Apply(app, endpoint, lookup, spec.payload)
	if err != nil {
		return 0, fmt.Errorf("failed to reconcile %s %s: %w", spec.kind, spec.name, err)
	}
	id := utils.GetIDFromObject(obj)
	if spec.register != nil {
		spec.register(id)
	}
	return id, nil
}

// reference names a required reference from one declared object to another,
// for resolveRef.
type reference struct {
	// app/endpoint is where the referenced object lives, e.g. dcim/sites;
	// cacheKind is its kind in the reference cache, e.g. "sites".
	app, endpoint string
	cacheKind     string
	// value is the declared reference: a slug, with a name accepted as
	// fallback.
	value string
	// kind labels the referenced object in the warning, e.g. "Site";
	// forKind/forName identify the referencing object, e.g. rack "R01".
	kind    string
	forKind string
	forName string
	// consumerApp/consumerEndpoint is the endpoint whose reconcile is marked
	// incomplete on a miss, which keeps pruning safe.
	consumerApp, consumerEndpoint string
}

// resolveRef resolves a required reference with one behavior everywhere: the
// run's reference cache first (it holds both the preloaded objects and
// everything registered earlier in this run, including objects only planned
// in --dry-run), then a live lookup by slug, then by name. On a miss it
// warns, marks the consuming endpoint's reconcile incomplete, and returns
// ok=false so the caller skips the object instead of failing the run.
func resolveRef(c *client.NetBoxClient, ref reference) (int, bool) {
	if id, ok := c.Cache().GetGlobalID(ref.cacheKind, ref.value); ok {
		return id, true
	}
	objs, err := c.Filter(ref.app, ref.endpoint, map[string]interface{}{"slug": ref.value})
	if err != nil || len(objs) == 0 {
		objs, err = c.Filter(ref.app, ref.endpoint, map[string]interface{}{"name": ref.value})
	}
	if err == nil && len(objs) > 0 {
		if id := utils.GetIDFromObject(objs[0]); id != 0 {
			return id, true
		}
	}
	c.Logger().Warning("%s %s not found for %s %s, skipping", ref.kind, ref.value, ref.forKind, ref.forName)
	c.MarkReconcileIncomplete(ref.consumerApp, ref.consumerEndpoint)
	return 0, false
}

// ensureManufacturer resolves a manufacturer by name, creating it on a miss:
// manufacturers are declared implicitly by the device types, module types and
// platforms that name them. A created manufacturer is registered so later
// objects in this same run reuse it instead of re-creating it, including in
// --dry-run.
func ensureManufacturer(c *client.NetBoxClient, name string) (int, error) {
	if id, ok := c.Cache().GetGlobalID("manufacturers", name); ok {
		return id, nil
	}
	slug := utils.Slugify(name)
	obj, err := c.Apply("dcim", "manufacturers",
		map[string]interface{}{"slug": slug},
		map[string]interface{}{"name": name, "slug": slug})
	if err != nil {
		return 0, fmt.Errorf("failed to create manufacturer %s: %w", name, err)
	}
	id := utils.GetIDFromObject(obj)
	c.Cache().Register("manufacturers", id, slug, name)
	return id, nil
}

// setTags resolves an object's declared tags into its payload as tag IDs. A tag
// is referenced by slug, with its name accepted as fallback, like every other
// reference. The client adds the managed tag on top, so the object ends up
// with exactly the declared tags plus `gitops`, and a tag removed from the
// YAML is removed from NetBox.
//
// An object that declares no tags gets no `tags` field here; the client still
// sends the managed tag alone.
//
// A tag that cannot be resolved is left off with a warning instead of failing
// the object: define it in definitions/extras and the next run adds it. A tag
// only planned in this --dry-run (ID 0) is left off too, since there is no ID
// to send yet.
func setTags(c *client.NetBoxClient, payload map[string]interface{}, slugs []string, forKind, forName string) {
	if len(slugs) == 0 {
		return
	}
	ids := make([]int, 0, len(slugs))
	seen := make(map[int]bool, len(slugs))
	for _, slug := range slugs {
		id, ok := resolveTag(c, slug)
		if !ok {
			c.Logger().Warning("Tag %s not found for %s %s, leaving it off (define it in definitions/extras)", slug, forKind, forName)
			continue
		}
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	payload["tags"] = ids
}

// resolveTag looks a tag up in the run's reference cache, then live by slug and
// by name. ok is true for a tag only planned in this --dry-run, with ID 0.
func resolveTag(c *client.NetBoxClient, slug string) (int, bool) {
	if id, ok := c.Cache().GetGlobalID("tags", slug); ok {
		return id, true
	}
	for _, field := range []string{"slug", "name"} {
		objs, err := c.Filter("extras", "tags", map[string]interface{}{field: slug})
		if err == nil && len(objs) > 0 {
			if id := utils.GetIDFromObject(objs[0]); id != 0 {
				c.Cache().Register("tags", id, slug)
				return id, true
			}
		}
	}
	return 0, false
}
