// SPDX-License-Identifier: Apache-2.0

// Package schema derives a JSON Schema for every kind of YAML file in the data
// directory from the Go models the loader decodes them into, so an editor can
// offer the field names, flag an unknown key and list the allowed values
// while the file is being typed — the same checks the loader makes, a few
// seconds earlier.
//
// A schema is generated, never written by hand: the models are the one source
// of what a file may contain. `go run ./cmd/schemagen` writes them to schema/,
// and a test fails when the committed files are behind the models.
//
// The file shapes the loader accepts are all described — a list of objects,
// a single object, or the grouped form with `defaults:` and `items:` — as are
// the short field names, the `interfaces:` mapping form, the `ip:` and
// `via:` string shorthands and `always:` on a per-interface default.
package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// Kind is one kind of data file: its schema name, its model, and the folders
// under definitions/ or inventory/ its files live in (for the editor header).
type Kind struct {
	Name    string
	Model   reflect.Type
	Folders []string
	// ListKeys are the keys the grouped form accepts for its list.
	ListKeys []string
}

// Kinds lists every kind of file the loader reads.
var Kinds = []Kind{
	{"device", reflect.TypeOf(models.DeviceConfig{}), []string{"inventory/hardware", "inventory/discovered/hardware"}, []string{"items", "devices"}},
	{"virtual-machine", reflect.TypeOf(models.VMConfig{}), []string{"inventory/virtual", "inventory/discovered/virtual"}, []string{"items", "devices"}},
	{"device-type", reflect.TypeOf(models.DeviceType{}), []string{"definitions/device_types"}, []string{"items", "devices"}},
	{"module-type", reflect.TypeOf(models.ModuleType{}), []string{"definitions/module_types"}, []string{"items", "devices"}},
	{"site", reflect.TypeOf(models.Site{}), []string{"definitions/sites"}, []string{"items", "devices"}},
	{"rack", reflect.TypeOf(models.Rack{}), []string{"definitions/racks"}, []string{"items", "devices"}},
	{"role", reflect.TypeOf(models.Role{}), []string{"definitions/roles"}, []string{"items", "devices"}},
	{"tag", reflect.TypeOf(models.Tag{}), []string{"definitions/extras"}, []string{"items", "devices"}},
	{"vlan", reflect.TypeOf(models.VLAN{}), []string{"definitions/vlans"}, []string{"items", "devices"}},
	{"vlan-group", reflect.TypeOf(models.VLANGroup{}), []string{"definitions/vlan_groups"}, []string{"items", "devices"}},
	{"vrf", reflect.TypeOf(models.VRF{}), []string{"definitions/vrfs"}, []string{"items", "devices"}},
	{"prefix", reflect.TypeOf(models.Prefix{}), []string{"definitions/prefixes"}, []string{"items", "devices"}},
	{"platform", reflect.TypeOf(models.Platform{}), []string{"definitions/platforms"}, []string{"items", "devices"}},
	{"tenant-group", reflect.TypeOf(models.TenantGroup{}), []string{"definitions/tenant_groups"}, []string{"items", "devices"}},
	{"tenant", reflect.TypeOf(models.Tenant{}), []string{"definitions/tenants"}, []string{"items", "devices"}},
	{"custom-field", reflect.TypeOf(models.CustomField{}), []string{"definitions/custom_fields"}, []string{"items", "devices"}},
	{"cluster-type", reflect.TypeOf(models.ClusterType{}), []string{"definitions/virtualization/cluster_types"}, []string{"items", "devices"}},
	{"cluster-group", reflect.TypeOf(models.ClusterGroup{}), []string{"definitions/virtualization/cluster_groups"}, []string{"items", "devices"}},
	{"cluster", reflect.TypeOf(models.Cluster{}), []string{"definitions/virtualization/clusters"}, []string{"items", "devices"}},
}

// KindForPath returns the kind whose folder a data-directory-relative path
// falls under, or false.
func KindForPath(rel string) (Kind, bool) {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	var best Kind
	found := false
	for _, k := range Kinds {
		for _, f := range k.Folders {
			if strings.HasPrefix(rel, f+"/") && (!found || len(f) > len(best.Folders[0])) {
				best, found = k, true
			}
		}
	}
	return best, found
}

// enums names, per model field, the list of values the loader accepts, so the
// editor can offer them. The lists are the ones validation uses.
var enums = map[string][]string{
	"Site.status":               models.SiteStatuses,
	"Rack.status":               models.RackStatuses,
	"DeviceConfig.status":       models.DeviceStatuses,
	"DeviceConfig.face":         models.DeviceFaces,
	"ModuleConfig.status":       models.DeviceStatuses,
	"VLAN.status":               models.VLANStatuses,
	"Prefix.status":             models.PrefixStatuses,
	"VMConfig.status":           models.VMStatuses,
	"DeviceType.subdevice_role": models.SubdeviceRoles,
	"DeviceType.weight_unit":    models.WeightUnits,
	"ModuleType.weight_unit":    models.WeightUnits,
	"DeviceType.airflow":        models.DeviceTypeAirflows,
	"ModuleType.airflow":        models.DeviceTypeAirflows,
	"CableAttrs.length_unit":    {"km", "m", "cm", "mi", "ft", "in"},
	"LinkConfig.length_unit":    {"km", "m", "cm", "mi", "ft", "in"},
	"ViaHop.length_unit":        {"km", "m", "cm", "mi", "ft", "in"},
}

// descriptions are shown by the editor on hover, for the fields whose
// meaning is not obvious from the name.
var descriptions = map[string]string{
	"DeviceConfig.rename_from":     "The object's previous name. Set it to correct a typo so the existing object is renamed instead of a second one being created; remove it once the sync has run.",
	"DeviceConfig.face":            "Rack face. Implied as front when a position is given and no face.",
	"DeviceConfig.custom_fields":   "Values for NetBox custom fields, by field name. Only the fields named here are written.",
	"DeviceConfig.cable_defaults":  "On a patch panel: type and colour of the cables a path (link.via) gets at this panel. front: patch cords into the front ports, rear: backbones out of the rear ports.",
	"InterfaceConfig.address_role": "\"primary\" makes this interface's address the device's primary IP (one per address family).",
	"InterfaceConfig.ip":           "The address: a string \"10.0.0.1/24\" or a mapping with address, vrf, dns_name, status, tags.",
	"InterfaceConfig.link":         "The cable from this interface: peer_device and peer_port, or with via: the whole path through patch panels.",
	"InterfaceConfig.type":         "Only for an interface the device type has no template for (virtual, lag, ...). A template port's type is not repeated.",
	"LinkConfig.via":               "Patch panels the cable runs through, in order, each as the front port the cord lands on: \"pp-a01:2\" or { device, port, color }. One or two panels.",
	"LinkConfig.color":             "Colour name (yellow, purple, blue, ...) or hex without #.",
	"ViaHop.color":                 "Colour of the cable leaving this panel toward the peer.",
	"IPConfig.vrf":                 "VRF name. The lint checks it against the VRF of the prefix the address falls into.",
	"PortTemplate.rear_port":       "Front port only: the rear port it is bridged to. A range counts along with a range in name.",
	"FrontPortConfig.rear_port":    "The rear port this front port is bridged to; only needed when the device type does not define it.",
}

// Generate returns the JSON Schema document for one kind, indented.
func Generate(kind Kind) ([]byte, error) {
	g := &generator{defs: map[string]interface{}{}, model: kind.Model}
	objectRef := g.ref(kind.Model, full)
	looseRef := g.ref(kind.Model, loose)
	defaultsRef := g.ref(kind.Model, defaults)

	list := map[string]interface{}{"type": "array", "items": objectRef,
		"description": fmt.Sprintf("A list of %ss.", kind.Name)}
	groupedProps := map[string]interface{}{
		"defaults": map[string]interface{}{
			"allOf":       []interface{}{defaultsRef},
			"description": "Fields merged into every entry below. Entry values win; tags are combined.",
		},
	}
	for _, key := range kind.ListKeys {
		// An entry of the grouped form may leave to `defaults` what the
		// loader otherwise requires, so nothing is required of it here.
		groupedProps[key] = map[string]interface{}{"type": "array", "items": looseRef}
	}
	grouped := map[string]interface{}{
		"type": "object", "properties": groupedProps, "additionalProperties": false,
		"description": "Grouped form: shared fields under defaults, the objects under items (or devices).",
	}
	doc := map[string]interface{}{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"$id":         "https://github.com/braunma/netbox-gitops-controller/schema/" + kind.Name + ".json",
		"title":       kind.Name + " file",
		"description": fmt.Sprintf("netbox-gitops: a file under %s. Generated from the Go models by `go run ./cmd/schemagen`; do not edit.", strings.Join(kind.Folders, " or ")),
		"anyOf":       []interface{}{list, grouped, objectRef},
		"definitions": g.defs,
	}
	return json.MarshalIndent(doc, "", "  ")
}

type generator struct {
	defs  map[string]interface{}
	model reflect.Type
}

// variant is how strictly an object is described. A listed object must carry
// its required fields (full). An entry of the grouped form may get them from
// `defaults:` (loose). The `defaults:` block itself requires nothing and
// describes `interfaces` as per-name defaults (defaults).
type variant int

const (
	full variant = iota
	loose
	defaults
)

func (v variant) suffix() string {
	switch v {
	case loose:
		return "Entry"
	case defaults:
		return "Defaults"
	}
	return ""
}

// ref returns a $ref to the definition of a struct type in a variant,
// generating it on first use.
func (g *generator) ref(t reflect.Type, v variant) map[string]interface{} {
	t = deref(t)
	name := t.Name() + v.suffix()
	if _, done := g.defs[name]; !done {
		g.defs[name] = nil // placeholder against recursion
		g.defs[name] = g.object(t, v)
	}
	return map[string]interface{}{"$ref": "#/definitions/" + name}
}

func (g *generator) object(t reflect.Type, v variant) map[string]interface{} {
	props := map[string]interface{}{}
	var required []string
	var requiredAny []interface{}
	aliases := aliasesOf(t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("yaml")
		key := strings.Split(tag, ",")[0]
		if key == "-" {
			continue
		}
		if key == "" {
			key = strings.ToLower(f.Name)
		}
		schema := g.field(t, f, key, v)
		props[key] = schema
		short := ""
		for s, canonical := range aliases {
			if canonical == key {
				short = s
			}
		}
		if short != "" {
			alias := map[string]interface{}{}
			for k, v := range schema {
				alias[k] = v
			}
			alias["description"] = fmt.Sprintf("Short for %s.", key)
			props[short] = alias
		}
		if v == full && !strings.Contains(tag, "omitempty") {
			if short != "" {
				requiredAny = append(requiredAny, map[string]interface{}{
					"anyOf": []interface{}{map[string]interface{}{"required": []string{key}}, map[string]interface{}{"required": []string{short}}},
				})
			} else {
				required = append(required, key)
			}
		}
	}
	// Both spellings of one field in one mapping are rejected by the loader.
	shorts := make([]string, 0, len(aliases))
	for short := range aliases {
		shorts = append(shorts, short)
	}
	sort.Strings(shorts)
	for _, short := range shorts {
		requiredAny = append(requiredAny, map[string]interface{}{
			"not": map[string]interface{}{"required": []string{short, aliases[short]}},
		})
	}
	out := map[string]interface{}{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		sort.Strings(required)
		out["required"] = required
	}
	if len(requiredAny) > 0 {
		out["allOf"] = requiredAny
	}
	return out
}

// field returns the schema of one struct field, with the shorthands the
// loader accepts and the enum of a choice field.
func (g *generator) field(owner reflect.Type, f reflect.StructField, key string, v variant) map[string]interface{} {
	out := g.typeSchema(f.Type, owner, key, v)
	if d, ok := descriptions[owner.Name()+"."+key]; ok {
		out["description"] = d
	}
	return out
}

func (g *generator) typeSchema(t reflect.Type, owner reflect.Type, key string, v variant) map[string]interface{} {
	// Nested objects inherit the strictness: under `defaults:` an `ip` may
	// carry a VRF and no address, so nothing nested is required there either.
	nested := v
	if nested == defaults {
		nested = loose
	}
	switch deref(t) {
	case reflect.TypeOf(models.IPConfig{}):
		return map[string]interface{}{"anyOf": []interface{}{
			map[string]interface{}{"type": "string", "description": "The address with prefix length, e.g. 10.0.0.1/24."},
			g.ref(deref(t), nested),
		}}
	case reflect.TypeOf(models.ViaHop{}):
		return map[string]interface{}{"anyOf": []interface{}{
			map[string]interface{}{"type": "string", "pattern": "^[^:]+:.+$", "description": "device:port"},
			g.ref(deref(t), nested),
		}}
	}
	switch t.Kind() {
	case reflect.Ptr:
		return g.typeSchema(t.Elem(), owner, key, v)
	case reflect.String:
		out := map[string]interface{}{"type": "string"}
		if e, ok := enums[owner.Name()+"."+key]; ok {
			out["enum"] = toInterfaces(e)
		}
		return out
	case reflect.Bool:
		return map[string]interface{}{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]interface{}{"type": "integer"}
	case reflect.Float64, reflect.Float32:
		return map[string]interface{}{"type": "number"}
	case reflect.Map:
		return map[string]interface{}{"type": "object"}
	case reflect.Interface:
		return map[string]interface{}{}
	case reflect.Slice:
		elem := deref(t.Elem())
		if elem.Kind() == reflect.Struct && key == "interfaces" {
			return g.interfaces(elem, v)
		}
		return map[string]interface{}{"type": "array", "items": g.typeSchema(elem, owner, key, nested)}
	case reflect.Struct:
		return g.ref(t, nested)
	}
	return map[string]interface{}{}
}

// interfaces is the schema of an `interfaces:` field: on an object, a list or
// a mapping keyed by name (address string, fields, or nothing); under
// `defaults:`, a mapping keyed by name of per-interface defaults that may
// carry `always: true`.
func (g *generator) interfaces(elem reflect.Type, v variant) map[string]interface{} {
	if v == defaults {
		return map[string]interface{}{
			"type":                 "object",
			"description":          "Per-interface defaults, keyed by interface name. They apply to the interfaces an entry lists; with always: true the interface is added to every entry.",
			"additionalProperties": g.interfaceDefaults(elem),
		}
	}
	byName := map[string]interface{}{
		"type":        "object",
		"description": "Interfaces keyed by name: an address alone, a mapping of fields, or nothing (listed for its defaults).",
		"additionalProperties": map[string]interface{}{"anyOf": []interface{}{
			map[string]interface{}{"type": "string", "description": "The interface's address, e.g. 10.0.0.1/24."},
			map[string]interface{}{"type": "null"},
			g.ref(elem, loose),
		}},
	}
	return map[string]interface{}{"anyOf": []interface{}{
		map[string]interface{}{"type": "array", "items": g.ref(elem, v)},
		byName,
	}}
}

func (g *generator) interfaceDefaults(elem reflect.Type) map[string]interface{} {
	name := elem.Name() + "PerNameDefaults"
	if _, done := g.defs[name]; !done {
		g.defs[name] = nil
		obj := g.object(elem, defaults)
		props := obj["properties"].(map[string]interface{})
		delete(props, "name")
		props["always"] = map[string]interface{}{"type": "boolean",
			"description": "Add this interface to every entry of the file that does not list it."}
		g.defs[name] = obj
	}
	return map[string]interface{}{"$ref": "#/definitions/" + name}
}

func aliasesOf(t reflect.Type) map[string]string {
	if a, ok := reflect.New(t).Interface().(models.FieldAliases); ok {
		return a.YAMLAliases()
	}
	return nil
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

func toInterfaces(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
