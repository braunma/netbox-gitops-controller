// SPDX-License-Identifier: Apache-2.0

package export

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Helpers that read one field of a NetBox API object. NetBox renders a
// related object as a nested mapping ({id, name, slug, ...}) and a choice
// field as {value, label}; these reduce either to the scalar the YAML uses.

func str(obj map[string]interface{}, key string) string {
	switch v := obj[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func nested(obj map[string]interface{}, key string) map[string]interface{} {
	return asMap(obj[key])
}

// asMap reads a nested object, which decoded JSON holds as a plain map and a
// caller may hand over as a client.Object.
func asMap(v interface{}) map[string]interface{} {
	switch m := v.(type) {
	case map[string]interface{}:
		return m
	case client.Object:
		return m
	}
	return nil
}

func nestedStr(obj map[string]interface{}, key, field string) string {
	if m := nested(obj, key); m != nil {
		return str(m, field)
	}
	return ""
}

func nestedID(obj map[string]interface{}, key string) int {
	return utils.GetIDFromObject(obj[key])
}

// choice reads a choice field, which NetBox returns as {value, label} (or,
// from some endpoints and older releases, as the bare value).
func choice(obj map[string]interface{}, key string) string {
	if m := asMap(obj[key]); m != nil {
		switch val := m["value"].(type) {
		case string:
			return val
		case float64:
			return strconv.FormatFloat(val, 'f', -1, 64)
		}
		return ""
	}
	switch v := obj[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func num(obj map[string]interface{}, key string) (float64, bool) {
	switch v := obj[key].(type) {
	case float64:
		return v, true
	case string:
		// Decimal fields (u_height, vcpus, cable length) arrive as strings
		// from some releases.
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	case map[string]interface{}, client.Object:
		return num(asMap(v), "value")
	}
	return 0, false
}

// whole returns a numeric field as an int when it is one; ok is false for an
// absent field and for a fractional value, which an int field in the YAML
// cannot carry.
func whole(obj map[string]interface{}, key string) (n int, present, ok bool) {
	f, present := num(obj, key)
	if !present {
		return 0, false, false
	}
	if f != math.Trunc(f) {
		return 0, true, false
	}
	return int(f), true, true
}

func boolean(obj map[string]interface{}, key string) bool {
	b, _ := obj[key].(bool)
	return b
}

// tagSlugs returns the slugs of an object's tags, without the managed tag:
// the controller stamps that one itself and it is never declared.
func tagSlugs(obj map[string]interface{}) []string {
	list, _ := obj["tags"].([]interface{})
	var slugs []string
	for _, t := range list {
		m := asMap(t)
		if m == nil {
			continue
		}
		slug := str(m, "slug")
		if slug == "" || slug == constants.ManagedTagSlug || slug == constants.StateTagSlug {
			continue
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

func hasTags(obj map[string]interface{}) bool { return len(tagSlugs(obj)) > 0 }

// siteFromScope returns the slug of the site an object is scoped to. NetBox
// 4.2 replaced the `site` field of prefixes, VLAN groups and clusters with
// scope_type/scope; older releases still send `site`. ok is false when the
// object is scoped to something other than a site (a region, a location),
// which the YAML cannot express.
func siteFromScope(obj map[string]interface{}, siteSlugByID map[int]string) (slug string, ok bool) {
	if st, present := obj["scope_type"]; present && st != nil {
		if st != "dcim.site" {
			return "", false
		}
		if s := nestedStr(obj, "scope", "slug"); s != "" {
			return s, true
		}
		id := utils.GetIDFromObject(obj["scope_id"])
		if id == 0 {
			id = nestedID(obj, "scope")
		}
		return siteSlugByID[id], true
	}
	if s := nestedStr(obj, "site", "slug"); s != "" {
		return s, true
	}
	return siteSlugByID[nestedID(obj, "site")], true
}

// byParent groups component objects by the ID of the object they belong to.
func byParent(objs []client.Object, key string) map[int][]client.Object {
	out := make(map[int][]client.Object)
	for _, o := range objs {
		if id := nestedID(o, key); id != 0 {
			out[id] = append(out[id], o)
		}
	}
	return out
}

func idOf(obj map[string]interface{}) int { return utils.GetIDFromObject(obj) }

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
