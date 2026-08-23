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
