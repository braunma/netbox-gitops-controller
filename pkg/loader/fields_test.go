// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
	"gopkg.in/yaml.v3"
)

// loadedKinds is every model a native YAML file decodes into, the root of each
// load path. Nested models (interfaces, modules, IPs) are reached from these.
var loadedKinds = []reflect.Type{
	reflect.TypeOf(models.Site{}), reflect.TypeOf(models.Rack{}), reflect.TypeOf(models.Role{}),
	reflect.TypeOf(models.Tag{}), reflect.TypeOf(models.VLAN{}), reflect.TypeOf(models.VLANGroup{}),
	reflect.TypeOf(models.VRF{}), reflect.TypeOf(models.Prefix{}), reflect.TypeOf(models.DeviceType{}),
	reflect.TypeOf(models.ModuleType{}), reflect.TypeOf(models.DeviceConfig{}), reflect.TypeOf(models.CustomField{}),
	reflect.TypeOf(models.Platform{}), reflect.TypeOf(models.TenantGroup{}), reflect.TypeOf(models.Tenant{}),
	reflect.TypeOf(models.ClusterType{}), reflect.TypeOf(models.ClusterGroup{}), reflect.TypeOf(models.Cluster{}),
	reflect.TypeOf(models.VMConfig{}),
}

// reachableStructs returns every struct type a loaded kind decodes into,
// nested ones included.
func reachableStructs() []reflect.Type {
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for _, ft := range yamlFields(t) {
			walk(ft)
		}
	}
	for _, t := range loadedKinds {
		walk(t)
	}
	out := make([]reflect.Type, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// TestAliasesPointAtRealFields checks every alias of every model statically:
// the canonical key must be a field of that model, and the short key must not
// already be one, or the alias would shadow a real field.
func TestAliasesPointAtRealFields(t *testing.T) {
	for _, st := range reachableStructs() {
		aliased, ok := reflect.New(st).Interface().(models.FieldAliases)
		if !ok {
			continue
		}
		fields := yamlFields(st)
		for short, canonical := range aliased.YAMLAliases() {
			if _, ok := fields[canonical]; !ok {
				t.Errorf("%s: alias %q points at %q, which is not a field", st.Name(), short, canonical)
			}
			if _, ok := fields[short]; ok {
				t.Errorf("%s: alias %q is already a field of its own", st.Name(), short)
			}
			if short == canonical {
				t.Errorf("%s: alias %q maps to itself", st.Name(), short)
			}
		}
	}
}

// aliasCase is one model the end-to-end alias test loads: a minimal valid
// object in canonical form, how to load it, and how to reach the object whose
// field an alias sets (the device itself, or its first module).
type aliasCase struct {
	model  reflect.Type
	folder string
	base   map[string]interface{}
	load   func(dl *DataLoader, folder string) (interface{}, error)
	target func(loaded interface{}) interface{}
	// nested names the list the aliased object sits in, "" for the root.
	nested string
}

func first(v interface{}) interface{} { return reflect.ValueOf(v).Index(0).Interface() }

func aliasCases() []aliasCase {
	device := map[string]interface{}{
		"name": "srv-01", "site_slug": "s", "role_slug": "r", "device_type_slug": "dt",
		"rack_slug": "rk", "position": 1, "face": "front",
	}
	return []aliasCase{
		{model: reflect.TypeOf(models.DeviceConfig{}), folder: "devices", base: device,
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadDevices(f) },
			target: first},
		{model: reflect.TypeOf(models.ModuleConfig{}), folder: "devices", nested: "modules",
			base: map[string]interface{}{"name": "bay-1", "module_type_slug": "mt"},
			load: func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadDevices(f) },
			target: func(v interface{}) interface{} {
				return &first(v).(*models.DeviceConfig).Modules[0]
			}},
		{model: reflect.TypeOf(models.Rack{}), folder: "racks",
			base:   map[string]interface{}{"name": "R1", "slug": "r1", "site_slug": "s"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadRacks(f) },
			target: first},
		{model: reflect.TypeOf(models.TenantGroup{}), folder: "tenant_groups",
			base:   map[string]interface{}{"name": "G", "slug": "g", "parent_slug": "p"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadTenantGroups(f) },
			target: first},
		{model: reflect.TypeOf(models.Tenant{}), folder: "tenants",
			base:   map[string]interface{}{"name": "T", "slug": "t", "group_slug": "g"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadTenants(f) },
			target: first},
		{model: reflect.TypeOf(models.VLAN{}), folder: "vlans",
			base:   map[string]interface{}{"name": "V", "vid": 10, "site_slug": "s", "group_slug": "g"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadVLANs(f) },
			target: first},
		{model: reflect.TypeOf(models.VLANGroup{}), folder: "vlan_groups",
			base:   map[string]interface{}{"name": "G", "slug": "g", "site_slug": "s"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadVLANGroups(f) },
			target: first},
		{model: reflect.TypeOf(models.Prefix{}), folder: "prefixes",
			base:   map[string]interface{}{"prefix": "10.0.0.0/24", "site_slug": "s", "vrf_name": "v", "vlan_name": "l"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadPrefixes(f) },
			target: first},
		{model: reflect.TypeOf(models.Cluster{}), folder: "clusters",
			base:   map[string]interface{}{"name": "C", "type_slug": "t", "group_slug": "g", "site_slug": "s"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadClusters(f) },
			target: first},
		{model: reflect.TypeOf(models.VMConfig{}), folder: "vms",
			base:   map[string]interface{}{"name": "vm-01", "site_slug": "s", "role_slug": "r"},
			load:   func(dl *DataLoader, f string) (interface{}, error) { return dl.LoadVMs(f) },
			target: first},
	}
}

// writeObject writes one object as a single-entry list file and loads it.
func loadObject(t *testing.T, c aliasCase, object map[string]interface{}) (interface{}, error) {
	t.Helper()
	root := object
	if c.nested != "" {
		root = map[string]interface{}{}
		for k, v := range aliasCases()[0].base {
			root[k] = v
		}
		root[c.nested] = []interface{}{object}
	}
	data, err := yaml.Marshal([]interface{}{root})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, c.folder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, c.folder, "x.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return c.load(NewDataLoader(dir, utils.NewLogger(false)), c.folder)
}

// fieldByYAML reads the field of a struct that a YAML key decodes into.
func fieldByYAML(obj interface{}, key string) interface{} {
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for i := 0; i < v.NumField(); i++ {
		if strings.Split(v.Type().Field(i).Tag.Get("yaml"), ",")[0] == key {
			return v.Field(i).Interface()
		}
	}
	return nil
}

// TestEveryAliasLoadsLikeItsCanonicalKey loads each model once in canonical
// form and once per alias with the short key, and requires the same object.
// The cases are driven by the alias tables themselves, and the test fails if
// any alias of any reachable model is left without a case.
func TestEveryAliasLoadsLikeItsCanonicalKey(t *testing.T) {
	covered := map[reflect.Type]bool{}
	for _, c := range aliasCases() {
		c := c
		covered[c.model] = true
		aliases := reflect.New(c.model).Interface().(models.FieldAliases).YAMLAliases()
		for _, short := range sortedKeys(aliases) {
			canonical := aliases[short]
			t.Run(c.model.Name()+"/"+short, func(t *testing.T) {
				if _, ok := c.base[canonical]; !ok {
					t.Fatalf("test case for %s does not set %q", c.model.Name(), canonical)
				}
				want, err := loadObject(t, c, c.base)
				if err != nil {
					t.Fatalf("canonical form failed to load: %v", err)
				}

				short := short
				object := map[string]interface{}{}
				for k, v := range c.base {
					object[k] = v
				}
				object[short] = "via-alias"
				delete(object, canonical)
				got, err := loadObject(t, c, object)
				if err != nil {
					t.Fatalf("short form %q failed to load: %v", short, err)
				}
				if value := fieldByYAML(c.target(got), canonical); value != "via-alias" {
					t.Errorf("%s: %q did not set %q, got %v", c.model.Name(), short, canonical, value)
				}

				// Same value under the short name decodes to the identical object.
				object[short] = c.base[canonical]
				got, err = loadObject(t, c, object)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: short form decoded to\n%+v\nwant\n%+v", c.model.Name(), c.target(got), c.target(want))
				}

				// Both spellings at once are rejected, naming both.
				object[canonical] = c.base[canonical]
				_, err = loadObject(t, c, object)
				if err == nil || !strings.Contains(err.Error(), `"`+short+`"`) || !strings.Contains(err.Error(), `"`+canonical+`"`) {
					t.Errorf("%s: both %q and %q set: err = %v, want both named", c.model.Name(), short, canonical, err)
				}
			})
		}
	}

	for _, st := range reachableStructs() {
		if _, ok := reflect.New(st).Interface().(models.FieldAliases); ok && !covered[st] {
			t.Errorf("%s declares aliases but has no case in aliasCases", st.Name())
		}
	}
}

// writeFile writes content under dir/folder/name.
func writeFile(t *testing.T, dir, folder, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, folder, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUnknownFieldsAreRejected covers the places a typo used to vanish: the
// top level, inside an IP (decoded by its own UnmarshalYAML), inside a list
// of modules, and in the defaults block. Each error names where.
func TestUnknownFieldsAreRejected(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"top level, with a suggestion", `
- name: "srv-01"
  site: "s"
  role: "r"
  device_type: "dt"
  rack_name: "rk"
`, []string{"entry 1", `unknown field "rack_name"`, `did you mean "rack"`}},
		{"inside an ip mapping", `
- name: "srv-01"
  site: "s"
  role: "r"
  device_type: "dt"
  interfaces:
    - name: "eth0"
      ip: { address: "10.0.0.1/24", vfr: "Production" }
`, []string{"interfaces[1].ip", `unknown field "vfr"`, `did you mean "vrf"`}},
		{"inside a module", `
- name: "srv-01"
  site: "s"
  role: "r"
  device_type: "dt"
  modules:
    - { name: "bay", module_type: "mt", serail: "X" }
`, []string{"modules[1]", `unknown field "serail"`}},
		{"in defaults", `
defaults:
  sight: "s"
devices:
  - { name: "srv-01", site: "s", role: "r", device_type: "dt" }
`, []string{"defaults", `unknown field "sight"`}},
		{"in an interface default", `
defaults:
  interfaces:
    eth0: { descripton: "x" }
devices:
  - { name: "srv-01", site: "s", role: "r", device_type: "dt", interfaces: [{ name: "eth0" }] }
`, []string{"defaults.interfaces.eth0", `unknown field "descripton"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "devices", "x.yaml", tt.content)
			_, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDevices("devices")
			if err == nil {
				t.Fatal("LoadDevices() = nil, want the unknown field rejected")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestCustomFieldKeysAreFree verifies that custom_fields, a free-form map,
// is not checked against any struct.
func TestCustomFieldKeysAreFree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "devices", "x.yaml", `
- name: "srv-01"
  site: "s"
  role: "r"
  device_type: "dt"
  custom_fields: { anything_goes: 1, hw_cpu: "x" }
`)
	devices, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDevices("devices")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	if devices[0].CustomFields["anything_goes"] != 1 {
		t.Errorf("custom_fields = %v", devices[0].CustomFields)
	}
}

// TestInterfaceDefaults covers the merge: defaults apply by interface name,
// the device's own value wins, `ip` merges key by key (also from the string
// shorthand), `link` merges, tags combine, an unlisted interface is not
// added, and one device's override does not leak into the next.
func TestInterfaceDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "devices", "x.yaml", `
defaults:
  site: "s"
  role: "r"
  device_type: "dt"
  interfaces:
    idrac:
      description: "BMC"
      tags: ["oob"]
      ip: { status: "active", vrf: "Management" }
    eth0:
      address_role: "primary"
      ip: { vrf: "Production" }
      link: { cable_type: "dac-active" }
    eth1:
      description: "unused by srv-02"

devices:
  - name: "srv-01"
    interfaces:
      - name: "idrac"
        tags: ["bmc"]
        ip: "10.0.10.1/24"
      - name: "eth0"
        description: "own"
        ip: { address: "10.0.20.1/24", vrf: "Override" }
        link: { peer_device: "leaf", peer_port: "Eth1/1" }
      - name: "eth1"
  - name: "srv-02"
    interfaces:
      - name: "idrac"
        ip: { address: "10.0.10.2/24", dns_name: "srv-02-idrac" }
      - name: "eth0"
`)
	devices, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDevices("devices")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	srv1, srv2 := devices[0], devices[1]

	idrac := srv1.Interfaces[0]
	if idrac.Description != "BMC" || idrac.IP == nil || idrac.IP.Address != "10.0.10.1/24" ||
		idrac.IP.VRF != "Management" || idrac.IP.Status != "active" {
		t.Errorf("srv-01 idrac = %+v ip %+v, want defaults merged into the shorthand IP", idrac, idrac.IP)
	}
	if !reflect.DeepEqual(idrac.Tags, []string{"oob", "bmc"}) {
		t.Errorf("srv-01 idrac tags = %v, want [oob bmc]", idrac.Tags)
	}

	eth0 := srv1.Interfaces[1]
	if eth0.Description != "own" || eth0.IP.VRF != "Override" || eth0.AddressRole != "primary" {
		t.Errorf("srv-01 eth0 = %+v ip %+v, want own values to win over defaults", eth0, eth0.IP)
	}
	if eth0.Link == nil || eth0.Link.PeerDevice != "leaf" || eth0.Link.CableType != "dac-active" {
		t.Errorf("srv-01 eth0 link = %+v, want link merged", eth0.Link)
	}
	if srv1.Interfaces[2].Description != "unused by srv-02" {
		t.Errorf("srv-01 eth1 = %+v, want the default description", srv1.Interfaces[2])
	}

	if len(srv2.Interfaces) != 2 {
		t.Fatalf("srv-02 interfaces = %+v, want only the listed idrac and eth0", srv2.Interfaces)
	}
	idrac2 := srv2.Interfaces[0]
	if idrac2.IP.VRF != "Management" || idrac2.IP.DNSName != "srv-02-idrac" ||
		!reflect.DeepEqual(idrac2.Tags, []string{"oob"}) {
		t.Errorf("srv-02 idrac = %+v ip %+v, want defaults without srv-01's values", idrac2, idrac2.IP)
	}
	// Listing the name alone is enough to opt in: the `ip` default (a VRF,
	// no address) is for interfaces that have an address and the `link`
	// default (a cable type, no peer) for those that are cabled. This one
	// has neither, so it gets the other defaults and no ip or link block.
	eth0b := srv2.Interfaces[1]
	if eth0b.AddressRole != "primary" {
		t.Errorf("srv-02 eth0 = %+v, want the scalar defaults applied", eth0b)
	}
	if eth0b.IP != nil {
		t.Errorf("srv-02 eth0 ip = %+v, want none: it declares no address", eth0b.IP)
	}
	if eth0b.Link != nil {
		t.Errorf("srv-02 eth0 link = %+v, want none: it declares no peer", eth0b.Link)
	}
}

// TestInterfaceDefaultsOnVMs verifies the same block works for VM interfaces.
func TestInterfaceDefaultsOnVMs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "vms", "x.yaml", `
defaults:
  cluster: "c"
  interfaces:
    eth0: { mode: "access", untagged_vlan: "Servers" }
devices:
  - name: "vm-01"
    interfaces: [{ name: "eth0", ip: "10.0.0.5/24" }]
`)
	vms, err := NewDataLoader(dir, utils.NewLogger(false)).LoadVMs("vms")
	if err != nil {
		t.Fatalf("LoadVMs() error = %v", err)
	}
	eth0 := vms[0].Interfaces[0]
	if eth0.Mode != "access" || eth0.UntaggedVLAN != "Servers" || eth0.IP.Address != "10.0.0.5/24" {
		t.Errorf("vm-01 eth0 = %+v, want the default mode and VLAN", eth0)
	}
}

// TestInterfaceDefaultsMistakes covers the ways the block can be wrong.
func TestInterfaceDefaultsMistakes(t *testing.T) {
	tests := []struct {
		name, folder, content, want string
	}{
		{"name matches no interface", "devices", `
defaults:
  interfaces:
    idrca: { description: "BMC" }
devices:
  - { name: "srv-01", site: "s", role: "r", device_type: "dt", interfaces: [{ name: "idrac" }] }
`, "defaults.interfaces.idrca matches no interface"},
		{"written as a list", "devices", `
defaults:
  interfaces:
    - { name: "idrac", description: "BMC" }
devices:
  - { name: "srv-01", site: "s", role: "r", device_type: "dt" }
`, "must map an interface name"},
		{"sets name", "devices", `
defaults:
  interfaces:
    idrac: { name: "other" }
devices:
  - { name: "srv-01", site: "s", role: "r", device_type: "dt", interfaces: [{ name: "idrac" }] }
`, "`name` comes from the key"},
		{"kind without interfaces", "sites", `
defaults:
  interfaces:
    eth0: { description: "x" }
items:
  - { name: "Berlin", slug: "berlin" }
`, `unknown field "interfaces"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tt.folder, "x.yaml", tt.content)
			dl := NewDataLoader(dir, utils.NewLogger(false))
			var err error
			if tt.folder == "sites" {
				_, err = dl.LoadSites(tt.folder)
			} else {
				_, err = dl.LoadDevices(tt.folder)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestShortDefaultOverriddenByCanonicalItem verifies that the two spellings
// are one field across defaults and items: an item's site_slug wins over a
// default site, and an item's site over a default site_slug.
func TestShortDefaultOverriddenByCanonicalItem(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "devices", "x.yaml", `
defaults:
  site: "default-site"
  role_slug: "default-role"
  device_type: "dt"
devices:
  - { name: "srv-01", site_slug: "own-site", role: "own-role" }
`)
	devices, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDevices("devices")
	if err != nil {
		t.Fatalf("LoadDevices() error = %v", err)
	}
	if devices[0].SiteSlug != "own-site" || devices[0].RoleSlug != "own-role" {
		t.Errorf("device = %+v, want the item's values to win in either spelling", devices[0])
	}
}
