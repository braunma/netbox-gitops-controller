// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// normalizeFields rewrites short field names (see models.FieldAliases) to
// their canonical keys and rejects every key the model does not have, walking
// nested mappings and lists of mappings. It edits raw in place.
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

	aliases := modelAliases(t)
	if len(aliases) > 0 {
		for _, short := range sortedKeys(aliases) {
			value, present := raw[short]
			if !present {
				continue
			}
			canonical := aliases[short]
			if _, both := raw[canonical]; both {
				return fmt.Errorf("%sboth %q and %q are set; they are the same field, keep one", prefix, short, canonical)
			}
			delete(raw, short)
			raw[canonical] = value
		}
	}

	for _, key := range sortedKeys(raw) {
		field, known := fields[key]
		if !known {
			return fmt.Errorf("%sunknown field %q%s", prefix, key, suggestField(key, t, fields, aliases))
		}
		if err := normalizeValue(raw[key], field, joinPath(path, key)); err != nil {
			return err
		}
	}
	return nil
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
		for i, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if err := normalizeFields(m, t.Elem(), fmt.Sprintf("%s[%d]", path, i+1)); err != nil {
					return err
				}
			}
		}
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
// `rack`, `vrf_name` → `vrf` on an IP).
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
	return fmt.Sprintf(" (not a field of %s)", modelLabel(t))
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
// They apply only to the interfaces an object lists; they never add one. The
// entries are normalized against the interface type of the model, so a typo
// inside them is reported like one on the object itself.
func interfaceDefaults(defaults map[string]interface{}, t reflect.Type) (map[string]map[string]interface{}, error) {
	value, ok := defaults["interfaces"]
	if !ok {
		return nil, nil
	}
	delete(defaults, "interfaces")

	field, known := yamlFields(structType(t))["interfaces"]
	if !known {
		return nil, fmt.Errorf("defaults: unknown field \"interfaces\" (not a field of %s)", modelLabel(structType(t)))
	}
	byName, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("defaults.interfaces must map an interface name to its defaults, e.g. `idrac: { description: ... }`")
	}
	elem := structType(field)
	if elem == nil && field.Kind() == reflect.Slice {
		elem = structType(field.Elem())
	}

	out := make(map[string]map[string]interface{}, len(byName))
	for _, name := range sortedKeys(byName) {
		entry, ok := byName[name].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("defaults.interfaces.%s must be a mapping of interface fields", name)
		}
		if _, set := entry["name"]; set {
			return nil, fmt.Errorf("defaults.interfaces.%s: `name` comes from the key and cannot be set here", name)
		}
		if err := normalizeFields(entry, elem, "defaults.interfaces."+name); err != nil {
			return nil, err
		}
		out[name] = entry
	}
	return out, nil
}

// applyInterfaceDefaults merges the per-interface defaults into every listed
// interface of that name. The interface's own values win, nested mappings
// (`ip`, `link`) are merged key by key, and tags are combined. It records the
// default names it used in used, so the caller can flag one that matched
// nothing.
func applyInterfaceDefaults(item map[string]interface{}, defaults map[string]map[string]interface{}, used map[string]bool) {
	if len(defaults) == 0 {
		return
	}
	list, ok := item["interfaces"].([]interface{})
	if !ok {
		return
	}
	merged := make([]interface{}, len(list))
	for i, entry := range list {
		merged[i] = entry
		iface, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := iface["name"].(string)
		def, ok := defaults[name]
		if !ok {
			continue
		}
		used[name] = true
		merged[i] = deepMerge(def, iface)
	}
	item["interfaces"] = merged
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
