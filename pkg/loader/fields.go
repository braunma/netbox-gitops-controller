// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// normalizeFields rewrites short field names (see models.FieldAliases) to
// their canonical keys and rejects every key the model does not have, walking
// nested mappings and lists of mappings. It edits raw in place, and reports
// every problem it finds rather than stopping at the first, so one run of
// yamlcheck lists all of them.
//
// The YAML decoder ignores an unknown key, so before this a misspelled field —
// `site:` where `site_slug:` was meant, `vfr:` for `vrf:` — was dropped without
// a word and the object synced without it. Checking here, against the struct
// the mapping decodes into, is also what makes the short names safe: an alias
// that did not map onto a real field would be rejected rather than vanish.
//
// The check is done by hand rather than with the decoder's KnownFields,
// because a model with its own UnmarshalYAML (IPConfig, for its string
// shorthand) decodes its body without the decoder's strictness.
func normalizeFields(raw map[string]interface{}, t reflect.Type, path string) error {
	t = structType(t)
	if t == nil {
		return nil
	}
	fields := yamlFields(t)
	prefix := ""
	if path != "" {
		prefix = path + ": "
	}

	var errs []error
	aliases := modelAliases(t)
	for _, short := range sortedKeys(aliases) {
		value, present := raw[short]
		if !present {
			continue
		}
		canonical := aliases[short]
		if _, both := raw[canonical]; both {
			errs = append(errs, fmt.Errorf("%sboth %q and %q are set; they are the same field, keep one", prefix, short, canonical))
			// Dropped so it is not reported a second time as unknown.
			delete(raw, short)
			continue
		}
		delete(raw, short)
		raw[canonical] = value
	}

	for _, key := range sortedKeys(raw) {
		field, known := fields[key]
		if !known {
			errs = append(errs, fmt.Errorf("%sunknown field %q%s", prefix, key, suggestField(key, t, fields, aliases)))
			continue
		}
		if err := normalizeValue(raw[key], field, joinPath(path, key)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// normalizeValue descends into a field's value when it is a mapping or a list
// of mappings for a struct-typed field. Anything else — a scalar, a free-form
// map such as custom_fields, the string shorthand of an IP — is left alone.
func normalizeValue(value interface{}, t reflect.Type, path string) error {
	switch v := value.(type) {
	case map[string]interface{}:
		if structType(t) != nil {
			return normalizeFields(v, t, path)
		}
	case []interface{}:
		for t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Kind() != reflect.Slice || structType(t.Elem()) == nil {
			return nil
		}
		var errs []error
		for i, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if err := normalizeFields(m, t.Elem(), fmt.Sprintf("%s[%d]", path, i+1)); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return errors.Join(errs...)
	}
	return nil
}

// joinPath extends a field path such as `interfaces[2]` by one key.
func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// structType returns the struct a type decodes into, through pointers, or nil.
func structType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

// yamlFields maps each YAML key of a struct to its field type.
func yamlFields(t reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		fields[name] = f.Type
	}
	return fields
}

// modelAliases returns a model's short-name table, nil when it has none.
func modelAliases(t reflect.Type) map[string]string {
	if aliased, ok := reflect.New(t).Interface().(models.FieldAliases); ok {
		return aliased.YAMLAliases()
	}
	return nil
}

// suggestField names the key an unknown one was probably meant to be: a
// field or short name it is a `_slug`/`_name` variant of (`rack_name` →
// `rack`, `vrf_name` → `vrf` on an IP), or else a near-miss spelling
// (`vfr` → `vrf`).
func suggestField(key string, t reflect.Type, fields map[string]reflect.Type, aliases map[string]string) string {
	for _, candidate := range []string{strings.TrimSuffix(key, "_slug"), strings.TrimSuffix(key, "_name"), key + "_slug", key + "_name"} {
		if candidate == key {
			continue
		}
		if _, ok := aliases[candidate]; ok {
			return fmt.Sprintf(" (did you mean %q?)", candidate)
		}
		if _, ok := fields[candidate]; ok {
			return fmt.Sprintf(" (did you mean %q?)", candidate)
		}
	}
	// Otherwise the one key a slip of the finger away: at most two edits, and
	// only when no other key is as close, so the hint is never a coin toss.
	best, bestDist, tie := "", 3, false
	for _, candidate := range append(sortedKeys(fields), sortedKeys(aliases)...) {
		d := editDistance(key, candidate)
		switch {
		case d < bestDist:
			best, bestDist, tie = candidate, d, false
		case d == bestDist && candidate != best:
			tie = true
		}
	}
	if best != "" && !tie {
		return fmt.Sprintf(" (did you mean %q?)", best)
	}
	return fmt.Sprintf(" (not a field of %s)", modelLabel(t))
}

// editDistance is the Levenshtein distance between two keys.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// modelLabel names a model the way the YAML talks about it: DeviceConfig is a
// "device", IPConfig an "ip".
func modelLabel(t reflect.Type) string {
	return strings.ToLower(strings.TrimSuffix(t.Name(), "Config"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// interfaceDefaults takes the per-interface defaults out of a file's defaults
// block. Under `defaults`, `interfaces` is a mapping from interface name to
// the fields every interface of that name gets:
//
//	defaults:
//	  interfaces:
//	    idrac: { description: "Dell iDRAC", ip: { vrf: "Management" } }
//
// They apply to the interfaces an object lists. An entry with `always: true`
// is also added to every object of the file that does not list it, so an
// interface whose whole configuration is shared (a PXE port with its VLAN)
// need not be repeated by name on each object; the returned always set names
// those entries. The entries are normalized against the interface type of
// the model, so a typo inside them is reported like one on the object itself.
func interfaceDefaults(defaults map[string]interface{}, t reflect.Type) (map[string]map[string]interface{}, map[string]bool, error) {
	value, ok := defaults["interfaces"]
	if !ok {
		return nil, nil, nil
	}
	delete(defaults, "interfaces")

	field, known := yamlFields(structType(t))["interfaces"]
	if !known {
		return nil, nil, fmt.Errorf("defaults: unknown field \"interfaces\" (not a field of %s)", modelLabel(structType(t)))
	}
	byName, ok := value.(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("defaults.interfaces must map an interface name to its defaults, e.g. `idrac: { description: ... }`")
	}
	elem := structType(field)
	if elem == nil && field.Kind() == reflect.Slice {
		elem = structType(field.Elem())
	}

	out := make(map[string]map[string]interface{}, len(byName))
	always := make(map[string]bool)
	var errs []error
	for _, name := range sortedKeys(byName) {
		entry, ok := byName[name].(map[string]interface{})
		if !ok {
			errs = append(errs, fmt.Errorf("defaults.interfaces.%s must be a mapping of interface fields", name))
			continue
		}
		if _, set := entry["name"]; set {
			errs = append(errs, fmt.Errorf("defaults.interfaces.%s: `name` comes from the key and cannot be set here", name))
			delete(entry, "name")
		}
		if v, set := entry["always"]; set {
			// Not an interface field: it says the entry applies to every
			// object, listed or not. Taken out before the fields are checked.
			b, ok := v.(bool)
			if !ok {
				errs = append(errs, fmt.Errorf("defaults.interfaces.%s: `always` must be true or false", name))
			} else if b {
				always[name] = true
			}
			delete(entry, "always")
		}
		if err := normalizeFields(entry, elem, "defaults.interfaces."+name); err != nil {
			errs = append(errs, err)
		}
		out[name] = entry
	}
	return out, always, errors.Join(errs...)
}

// applyInterfaceDefaults merges the per-interface defaults into every listed
// interface of that name, and adds the `always` ones the object does not
// list. The interface's own values win, nested mappings (`ip`, `link`) are
// merged key by key, and tags are combined. An `ip` default only applies to
// an interface that declares an address, a `link` default to one that
// declares a peer (see nestedDefaultRequires). It records the default names
// it used in used, so the caller can flag one that matched nothing.
func applyInterfaceDefaults(item map[string]interface{}, defaults map[string]map[string]interface{}, always map[string]bool, used map[string]bool) {
	if len(defaults) == 0 {
		return
	}
	list, _ := item["interfaces"].([]interface{})
	if list == nil && len(always) == 0 {
		return
	}
	merged := make([]interface{}, 0, len(list)+len(always))
	listed := make(map[string]bool, len(list))
	for _, entry := range list {
		iface, ok := entry.(map[string]interface{})
		if !ok {
			merged = append(merged, entry)
			continue
		}
		name, _ := iface["name"].(string)
		listed[name] = true
		def, ok := defaults[name]
		if !ok {
			merged = append(merged, entry)
			continue
		}
		used[name] = true
		merged = append(merged, withoutIncompleteBlocks(deepMerge(def, iface)))
	}
	// An `always` interface the object did not list is added from the
	// default alone: its name and whatever the default says.
	for _, name := range sortedKeys(always) {
		if listed[name] {
			continue
		}
		used[name] = true
		merged = append(merged, withoutIncompleteBlocks(deepMerge(defaults[name], map[string]interface{}{"name": name})))
	}
	item["interfaces"] = merged
}

// withoutIncompleteBlocks drops a nested block a default supplied when the
// interface itself gives it no identity. A default may carry `ip` fields (a
// VRF, a status) for the interfaces that have an address, or `link` fields
// (a cable type) for those that are cabled. An interface that declares no
// address or no peer gets no such block at all, rather than one validation
// would reject for the field only the interface itself can supply.
func withoutIncompleteBlocks(out map[string]interface{}) map[string]interface{} {
	for block, required := range nestedDefaultRequires {
		if m, ok := out[block].(map[string]interface{}); ok {
			if value, _ := m[required].(string); value == "" {
				delete(out, block)
			}
		}
	}
	return out
}

// widenInterfaceMap accepts `interfaces:` written as a mapping keyed by
// interface name, the way `defaults.interfaces` is written, and turns it into
// the list the model decodes:
//
//	interfaces:
//	  idrac: "10.10.10.91/24"      # an address alone
//	  eth0: { description: "x" }    # fields
//	  eth1:                         # listed by name only, for its defaults
//
// A YAML mapping has no order the decoder keeps, so the interfaces are sorted
// by name. The list form stays as it is; the two may not be mixed in one
// object because a key is one or the other.
func widenInterfaceMap(raw map[string]interface{}) error {
	byName, ok := raw["interfaces"].(map[string]interface{})
	if !ok {
		return nil
	}
	list := make([]interface{}, 0, len(byName))
	var errs []error
	for _, name := range sortedKeys(byName) {
		var entry map[string]interface{}
		switch v := byName[name].(type) {
		case nil:
			entry = map[string]interface{}{}
		case string:
			entry = map[string]interface{}{"ip": v}
		case map[string]interface{}:
			if _, set := v["name"]; set {
				errs = append(errs, fmt.Errorf("interfaces.%s: `name` comes from the key and cannot be set here", name))
				delete(v, "name")
			}
			entry = v
		default:
			errs = append(errs, fmt.Errorf("interfaces.%s must be an address (\"10.0.0.1/24\"), a mapping of fields, or empty", name))
			continue
		}
		entry["name"] = name
		list = append(list, entry)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	raw["interfaces"] = list
	return nil
}

// nestedDefaultRequires names, for each nested block a per-interface default
// may fill in, the field only the interface itself can provide. A merged block
// without it is dropped: the default was meant for the interfaces that have
// one.
var nestedDefaultRequires = map[string]string{
	"ip":   "address",
	"link": "peer_device",
}

// deepMerge returns base overlaid with over: over's values win, mappings on
// both sides are merged recursively, and tags lists are combined. Neither
// input is modified. An IP given as the string shorthand on one side is
// widened to its mapping form so it can merge with a mapping on the other.
func deepMerge(base, over map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base)+len(over))
	for k, v := range base {
		out[k] = deepCopy(v)
	}
	for k, v := range over {
		if k == "tags" {
			out[k] = unionTags(out[k], v)
			continue
		}
		if k == "ip" {
			v = widenIP(v)
			out[k] = widenIP(out[k])
		}
		baseMap, bOK := out[k].(map[string]interface{})
		overMap, oOK := v.(map[string]interface{})
		if bOK && oOK {
			out[k] = deepMerge(baseMap, overMap)
			continue
		}
		out[k] = deepCopy(v)
	}
	return out
}

// widenIP turns the `ip: "10.0.0.1/24"` shorthand into `{address: ...}`.
func widenIP(v interface{}) interface{} {
	if s, ok := v.(string); ok {
		return map[string]interface{}{"address": s}
	}
	return v
}

// deepCopy copies the mappings and lists of a decoded YAML value, so a merge
// never shares structure between the objects of one file.
func deepCopy(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, inner := range t {
			out[k] = deepCopy(inner)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, inner := range t {
			out[i] = deepCopy(inner)
		}
		return out
	}
	return v
}
