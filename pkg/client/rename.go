// SPDX-License-Identifier: Apache-2.0

package client

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// RenameLookup picks the lookup Apply should use for an object whose identity
// may have changed in the YAML.
//
// Every object here is matched against NetBox by an identifying field — a slug
// where NetBox gives one, otherwise a name, model, prefix, address or VID. That
// is what makes the sync idempotent, and it is also why correcting a typo in
// such a field is not an edit: the old value no longer matches anything, so the
// object is created a second time and the original is left behind as an orphan
// (deleted only if someone later runs --prune, having meanwhile been the object
// everything else still points at).
//
// Declaring `rename_from: <old value>` closes that gap. Rather than issuing a
// separate rename call, this returns the *previous* lookup so the object is
// found where it actually is, and the payload — which already carries the new
// identity — renames it through the ordinary diff. Everything downstream is
// inherited unchanged: the change shows up as a normal update, --dry-run
// reports it without writing, the object counts as reconciled so --prune does
// not reap it, and once NetBox holds the new value the current lookup matches
// and the declaration becomes a no-op that can be deleted from the YAML.
//
// current and previous must identify the same kind of object in the same scope:
// a rename changes what an object is called, not which site or device it
// belongs to. Moving an object between scopes is a different operation and is
// not what this does.
func (c *NetBoxClient) RenameLookup(app, endpoint, label string, current, previous map[string]interface{}) (map[string]interface{}, error) {
	if len(previous) == 0 {
		return current, nil
	}
	// The rename already happened, or was declared against the value the object
	// already has. Either way there is nothing to look up under an old name.
	if lookupsEqual(current, previous) {
		return current, nil
	}
	// Scoped by something that only exists as a plan in this run (--dry-run
	// against an empty NetBox), so neither lookup can match and the object is
	// going to be created rather than renamed.
	if _, unresolved := unresolvedReference(previous); unresolved {
		return current, nil
	}

	// If the new identity already resolves, the rename is done (or the object
	// was created under the new name). Touching the old one now would be wrong:
	// two objects exist and the user has to say which survives.
	found, err := c.Filter(app, endpoint, current)
	if err != nil {
		return nil, fmt.Errorf("failed to look up %s %s: %w", endpoint, label, err)
	}
	if len(found) > 0 {
		if stale, err := c.Filter(app, endpoint, previous); err == nil && len(stale) > 0 {
			c.logger.Warning(
				"  ⚠ %s %s declares rename_from, but objects exist under both the old and the new identity;"+
					" renaming nothing. Delete the stale one in NetBox, then drop rename_from",
				endpoint, label)
		}
		return current, nil
	}

	previousObjects, err := c.Filter(app, endpoint, previous)
	if err != nil {
		return nil, fmt.Errorf("failed to look up the previous identity of %s %s: %w", endpoint, label, err)
	}
	switch len(previousObjects) {
	case 0:
		// Nothing under the old identity either: a first-time create, or a
		// rename_from left in place long after it was applied.
		return current, nil
	case 1:
	default:
		return nil, fmt.Errorf(
			"%s %s: rename_from matches %d objects in NetBox, so it does not identify which one to rename;"+
				" narrow it or rename the object manually", endpoint, label, len(previousObjects))
	}

	obj := previousObjects[0]
	// Renaming is driven by the operator's claim about an object's past, which
	// is weaker evidence than a plain identity match — a typo in rename_from
	// can name a real object that has nothing to do with this declaration. So
	// only objects this tool manages may be renamed. Adoption still works the
	// ordinary way: declare the object under the name it already has, and the
	// next sync tags it.
	if !constants.UntaggableEndpoints[endpoint] && !c.hasManagedTag(obj) {
		return nil, fmt.Errorf(
			"%s %s: rename_from matches an object that is not managed by this tool (no %q tag), and renaming it"+
				" could rename something unrelated. Rename it in NetBox by hand, or declare it under its current"+
				" name first so it is adopted", endpoint, label, constants.ManagedTagSlug)
	}

	c.logger.Info("  ⟳ Renaming %s: %s → %s", endpoint, c.formatLookup(previous), c.formatLookup(current))
	return previous, nil
}

// hasManagedTag reports whether a NetBox object carries the gitops tag.
//
// A read from NetBox renders tags as nested objects carrying a slug, so that is
// the primary check. The bare ID is accepted too: it is the shape tags are
// *written* in, and matching it as well means this does not quietly depend on
// how a particular response chose to serialize them. An endpoint that does not
// support tags omits the field, which callers rule out before reaching here.
func (c *NetBoxClient) hasManagedTag(obj Object) bool {
	tags, ok := obj["tags"].([]interface{})
	if !ok {
		return false
	}
	for _, t := range tags {
		if tag, ok := t.(map[string]interface{}); ok {
			if slug, ok := tag["slug"].(string); ok && slug == constants.ManagedTagSlug {
				return true
			}
		}
		if c.managedTagID != 0 && utils.GetIDFromObject(t) == c.managedTagID {
			return true
		}
	}
	return false
}

// lookupsEqual compares two lookups by value. Lookup values are scalars —
// strings, ints and the occasional bool — so a formatted comparison is enough
// and avoids caring whether a VID arrived as an int or a string.
func lookupsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || fmt.Sprintf("%v", av) != fmt.Sprintf("%v", bv) {
			return false
		}
	}
	return true
}

// renameLookup builds the previous-identity lookup for an object by copying its
// current lookup and replacing the identifying field. The scope keys (site_id,
// device_id, cluster_id, ...) are carried over unchanged, which is what keeps a
// rename from silently moving an object somewhere else.
func renameLookup(current map[string]interface{}, field string, previous interface{}) map[string]interface{} {
	if previous == nil || previous == "" {
		return nil
	}
	out := make(map[string]interface{}, len(current))
	for k, v := range current {
		out[k] = v
	}
	out[field] = previous
	return out
}

// RenamedLookup resolves the lookup for an object that declares rename_from.
// It is the one entry point the reconcilers use: build the normal lookup, say
// which field carries the identity, and pass the declared previous value.
func (c *NetBoxClient) RenamedLookup(app, endpoint, label string, current map[string]interface{}, field string, previous interface{}) (map[string]interface{}, error) {
	return c.RenameLookup(app, endpoint, label, current, renameLookup(current, field, previous))
}

// SlugifiedRename returns the previous identity for a field that is stored as a
// slug, accepting either the old slug or the old human name. A manufacturer is
// keyed by a slug derived from its name, so `rename_from: "Dell Inc."` and
// `rename_from: dell-inc` must both work.
func SlugifiedRename(previous string) interface{} {
	if previous == "" {
		return nil
	}
	return utils.Slugify(previous)
}
