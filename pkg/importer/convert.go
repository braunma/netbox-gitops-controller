// SPDX-License-Identifier: Apache-2.0

// Package importer reads a live NetBox instance and writes this repository's
// native YAML — the reverse of the sync. It is the brownfield-adoption path:
// point it at a populated NetBox and it emits definitions and inventory that,
// applied back, converge with no changes.
//
// Two rules hold throughout, mirroring pkg/ingest in the opposite direction:
//
//   - It never writes to NetBox. It issues GETs (and OPTIONS) only; every
//     effect on the world is a set of files in a target directory.
//   - It never invents a value. Anything NetBox does not hold is omitted;
//     anything NetBox holds that this schema cannot express is recorded in the
//     coverage report, never silently dropped.
//
// Output is deterministic: no timestamps, run ids, instance URLs or NetBox ids
// appear in it, and everything is sorted by a stable key, so two imports of an
// unchanged instance produce identical bytes and a re-import is a reviewable
// diff.
package importer

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// str returns a string-valued field, or "".
func str(obj client.Object, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}

// intOf returns an integer-valued field (NetBox numbers arrive as float64).
func intOf(obj client.Object, key string) int {
	switch v := obj[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// floatOf returns a float-valued field.
func floatOf(obj client.Object, key string) float64 {
	switch v := obj[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

// boolOf returns a boolean-valued field.
func boolOf(obj client.Object, key string) bool {
	b, _ := obj[key].(bool)
	return b
}

// choiceValue reads a NetBox choice field, which serialises as
// {"value": "...", "label": "..."}. A plain string is tolerated for older
// releases. Returns "" when absent.
func choiceValue(obj client.Object, key string) string {
	switch v := obj[key].(type) {
	case map[string]interface{}:
		if s, ok := v["value"].(string); ok {
			return s
		}
	case string:
		return v
	}
	return ""
}

// nested returns a nested reference object ({"id","slug","name",...}), or nil.
func nested(obj client.Object, key string) map[string]interface{} {
	m, _ := obj[key].(map[string]interface{})
	return m
}

// refSlug resolves a nested reference to a slug: the reference's own slug when
// it has one, else a slug derived from its name. Returns "" when the reference
// is absent. This is how every "*_slug" field in the models is populated from
// NetBox's nested objects.
func refSlug(obj client.Object, key string) string {
	ref := nested(obj, key)
	if ref == nil {
		return ""
	}
	if s, ok := ref["slug"].(string); ok && s != "" {
		return s
	}
	if n, ok := ref["name"].(string); ok && n != "" {
		return utils.Slugify(n)
	}
	return ""
}

// refName resolves a nested reference to its name (for fields the models key by
// name rather than slug, e.g. a VM's cluster or tenant). Returns "".
func refName(obj client.Object, key string) string {
	ref := nested(obj, key)
	if ref == nil {
		return ""
	}
	if n, ok := ref["name"].(string); ok {
		return n
	}
	return ""
}

// slugOrName is refSlug's rule applied to a bare nested object.
func slugOrName(ref map[string]interface{}) string {
	if ref == nil {
		return ""
	}
	if s, ok := ref["slug"].(string); ok && s != "" {
		return s
	}
	if n, ok := ref["name"].(string); ok && n != "" {
		return utils.Slugify(n)
	}
	return ""
}

// tagSlugs returns an object's tag slugs, sorted for determinism and with the
// excluded slugs (the managed tag, and any sandbox tag) removed. NetBox returns
// tags as nested objects; the managed tag is injected by the sync, so emitting
// it would be noise that also makes pre- and post-adoption imports differ.
func tagSlugs(obj client.Object, exclude map[string]bool) []string {
	raw, ok := obj["tags"].([]interface{})
	if !ok {
		return nil
	}
	var slugs []string
	for _, t := range raw {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		slug, _ := tm["slug"].(string)
		if slug == "" {
			continue
		}
		if exclude[slug] {
			continue
		}
		slugs = append(slugs, slug)
	}
	return sortedUnique(slugs)
}

// objName returns an object's name for error and report messages.
func objName(obj client.Object) string {
	for _, key := range []string{"name", "display", "slug", "model", "prefix", "address"} {
		if v, ok := obj[key].(string); ok && v != "" {
			return v
		}
	}
	return fmt.Sprintf("#%d", utils.GetIDFromObject(obj))
}
