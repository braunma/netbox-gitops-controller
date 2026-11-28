// SPDX-License-Identifier: Apache-2.0

// Package changeset compares two loaded datasets — the data directory as it
// was at the last completed sync and as it is now — and says which objects a
// sync has to look at again.
//
// The comparison is made on what the loader resolved, not on the files: a
// device whose file was not touched still counts as changed when a `defaults`
// block, a derived value or a cable path gave it a different result, and a
// file that was only reformatted changes nothing.
package changeset

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// The definition groups a change-based sync runs or skips as a whole. The
// first three are sync phases; GroupClusters is the definitions half of the
// virtualization phase, whose other half (the VMs) is selected one by one.
const (
	GroupFoundation  = loader.PhaseFoundation
	GroupNetwork     = loader.PhaseNetwork
	GroupDeviceTypes = loader.PhaseDeviceTypes
	GroupClusters    = "clusters"
)

// Entry is one object the sync will look at, and why.
type Entry struct {
	// Kind and Name identify the object, e.g. "device" and "ceph-01".
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Why is a sentence fragment: "new", "changed: interfaces, rack_slug",
	// "its device type r650 changed".
	Why string `json:"why"`
	// File is where the object is declared now. Touched says whether that
	// file itself differs from the base; when it does not, the change came
	// from somewhere else (a defaults block, a derived value).
	File    string `json:"file,omitempty"`
	Touched bool   `json:"touched"`
}

// Result is what differs between a base and a head dataset.
type Result struct {
	// Groups names the definition groups in which at least one object was
	// added or changed.
	Groups map[string]bool
	// Devices and VMs name the inventory objects to reconcile.
	Devices map[string]bool
	VMs     map[string]bool
	// Entries explains every selection above, in a stable order.
	Entries []Entry
	// Removed lists what the base declared and the head no longer does. A
	// change-based sync deletes nothing; this is what the next pruning full
	// sync will remove.
	Removed []string
}

// Empty reports whether nothing at all was added or changed.
func (r Result) Empty() bool {
	return len(r.Groups) == 0 && len(r.Devices) == 0 && len(r.VMs) == 0
}

// kind describes one list of a dataset for the comparison.
type kind struct {
	label string
	group string
	// objects returns the list as (identity, object) pairs.
	objects func(ds *loader.Dataset) []object
	// removable is false for kinds a prune never deletes (tags, custom
	// fields), so they are not announced as pending deletions.
	removable bool
}

type object struct {
	key string
	// name is what the object is called in the output when its identity is
	// not what a person would call it (a VLAN is identified by its vid).
	name string
	// previous is the identity the object declares it was renamed from.
	previous string
	value    any
}

// display is the name the object goes by in the output.
func (o object) display() string {
	if o.name != "" {
		return o.name
	}
	return o.key
}

// each adapts a typed list to []object.
func each[T any](list []*T, key func(*T) string, previous func(*T) string) []object {
	out := make([]object, 0, len(list))
	for _, item := range list {
		o := object{key: key(item), value: item}
		if previous != nil {
			o.previous = previous(item)
		}
		out = append(out, o)
	}
	return out
}

// scoped joins an identity with what it is unique within (a site, a group, a
// VRF), leaving out whatever is not set.
func scoped(parts ...string) string {
	var set []string
	for _, p := range parts {
		if p != "" {
			set = append(set, p)
		}
	}
	return strings.Join(set, "/")
}

var definitionKinds = []kind{
	{"tag", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.Tags, func(t *models.Tag) string { return t.Slug }, nil)
	}, false},
	{"role", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.Roles, func(r *models.Role) string { return r.Slug },
			func(r *models.Role) string { return r.RenameFrom })
	}, true},
	{"custom field", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.CustomFields, func(f *models.CustomField) string { return f.Name },
			func(f *models.CustomField) string { return f.RenameFrom })
	}, false},
	{"platform", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.Platforms, func(p *models.Platform) string { return p.Slug },
			func(p *models.Platform) string { return p.RenameFrom })
	}, true},
	{"tenant group", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.TenantGroups, func(g *models.TenantGroup) string { return g.Slug },
			func(g *models.TenantGroup) string { return g.RenameFrom })
	}, true},
	{"tenant", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.Tenants, func(t *models.Tenant) string { return t.Slug },
			func(t *models.Tenant) string { return t.RenameFrom })
	}, true},
	{"site", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.Sites, func(s *models.Site) string { return s.Slug },
			func(s *models.Site) string { return s.RenameFrom })
	}, true},
	{"rack", GroupFoundation, func(ds *loader.Dataset) []object {
		return each(ds.Racks, func(r *models.Rack) string { return scoped(r.SiteSlug, r.Slug) },
			func(r *models.Rack) string { return renamed(r.SiteSlug, r.RenameFrom) })
	}, true},

	{"VRF", GroupNetwork, func(ds *loader.Dataset) []object {
		return each(ds.VRFs, func(v *models.VRF) string { return v.Name },
			func(v *models.VRF) string { return v.RenameFrom })
	}, true},
	{"VLAN group", GroupNetwork, func(ds *loader.Dataset) []object {
		return each(ds.VLANGroups, func(g *models.VLANGroup) string { return scoped(g.SiteSlug, g.Slug) },
			func(g *models.VLANGroup) string { return renamed(g.SiteSlug, g.RenameFrom) })
	}, true},
	{"VLAN", GroupNetwork, func(ds *loader.Dataset) []object {
		// NetBox identity as the reconciler uses it: the vid within a site.
		// rename_from holds the previous vid.
		objs := each(ds.VLANs, func(v *models.VLAN) string { return scoped(v.SiteSlug, strconv.Itoa(v.VID)) },
			func(v *models.VLAN) string { return renamed(v.SiteSlug, v.RenameFrom) })
		for i, v := range ds.VLANs {
			objs[i].name = scoped(v.SiteSlug, fmt.Sprintf("%s (%d)", v.Name, v.VID))
		}
		return objs
	}, true},
	{"prefix", GroupNetwork, func(ds *loader.Dataset) []object {
		return each(ds.Prefixes, func(p *models.Prefix) string { return scoped(p.VRFName, p.Prefix) },
			func(p *models.Prefix) string { return renamed(p.VRFName, p.RenameFrom) })
	}, true},

	{"module type", GroupDeviceTypes, func(ds *loader.Dataset) []object {
		return each(ds.ModuleTypes, moduleTypeKey,
			func(m *models.ModuleType) string { return m.RenameFrom })
	}, true},
	{"device type", GroupDeviceTypes, func(ds *loader.Dataset) []object {
		return each(ds.DeviceTypes, func(d *models.DeviceType) string { return d.Slug },
			func(d *models.DeviceType) string { return d.RenameFrom })
	}, true},

	{"cluster type", GroupClusters, func(ds *loader.Dataset) []object {
		return each(ds.ClusterTypes, func(c *models.ClusterType) string { return c.Slug },
			func(c *models.ClusterType) string { return c.RenameFrom })
	}, true},
	{"cluster group", GroupClusters, func(ds *loader.Dataset) []object {
		return each(ds.ClusterGroups, func(c *models.ClusterGroup) string { return c.Slug },
			func(c *models.ClusterGroup) string { return c.RenameFrom })
	}, true},
	{"cluster", GroupClusters, func(ds *loader.Dataset) []object {
		return each(ds.Clusters, func(c *models.Cluster) string { return c.Name },
			func(c *models.Cluster) string { return c.RenameFrom })
	}, true},
}

func renamed(scope, from string) string {
	if from == "" {
		return ""
	}
	return scoped(scope, from)
}

// moduleTypeKey is the identity a device's module refers to: the slug, or the
// model for a type that declares none.
func moduleTypeKey(m *models.ModuleType) string {
	if m.Slug != "" {
		return m.Slug
	}
	return m.Model
}

// Compare returns what head adds to or changes in base. touched holds the
// files (as the loader names them in Dataset.Origins) that differ between the
// two; it only refines the explanations and may be nil.
func Compare(base, head *loader.Dataset, touched map[string]bool) Result {
	res := Result{
		Groups:  map[string]bool{},
		Devices: map[string]bool{},
		VMs:     map[string]bool{},
	}
	origin := func(obj any) (string, bool) {
		o, ok := head.Origins[obj]
		if !ok {
			return "", false
		}
		return o.File, touched[o.File]
	}
	add := func(kindLabel, name, why string, obj any) {
		file, wasTouched := origin(obj)
		res.Entries = append(res.Entries, Entry{Kind: kindLabel, Name: name, Why: why, File: file, Touched: wasTouched})
	}

	// Definitions: any added or changed object selects its whole group. The
	// device and module types that changed are remembered by identity, for
	// the devices built from them.
	changedTypes := map[string]map[string]bool{}
	for _, k := range definitionKinds {
		changed, removed := diffKind(k, base, head)
		for _, c := range changed {
			res.Groups[k.group] = true
			add(k.label, c.name, c.why, c.value)
			if changedTypes[k.label] == nil {
				changedTypes[k.label] = map[string]bool{}
			}
			changedTypes[k.label][c.key] = true
		}
		if k.removable {
			for _, key := range removed {
				res.Removed = append(res.Removed, k.label+" "+key)
			}
		}
	}

	compareDevices(base, head, changedTypes, &res, add)
	compareVMs(base, head, changedTypes["cluster"], &res, add)

	res.Removed = append(res.Removed, removedParts(base, head)...)
	sort.Strings(res.Removed)
	return res
}

type change struct {
	key string
	// name is the key, or the object's display name when it has one.
	name  string
	why   string
	value any
}

// diffKind compares one kind of object between the two datasets.
func diffKind(k kind, base, head *loader.Dataset) (changed []change, removed []string) {
	return diffObjects(k.objects(base), k.objects(head))
}

// diffObjects returns the objects of head that are new or differ from base,
// and the identities only base holds. An identity declared more than once is
// compared as the list of its declarations, so a duplicate can never hide a
// change.
func diffObjects(base, head []object) (changed []change, removed []string) {
	index := func(list []object) map[string][]any {
		m := make(map[string][]any, len(list))
		for _, o := range list {
			m[o.key] = append(m[o.key], o.value)
		}
		return m
	}
	baseByKey, headByKey := index(base), index(head)

	renamedFrom := map[string]bool{}
	done := map[string]bool{}
	for _, o := range head {
		if o.previous != "" {
			renamedFrom[o.previous] = true
		}
		if done[o.key] {
			continue
		}
		done[o.key] = true

		was, existed := baseByKey[o.key]
		now := headByKey[o.key]
		switch {
		case !existed && o.previous != "" && len(baseByKey[o.previous]) > 0:
			changed = append(changed, change{key: o.key, name: o.display(), why: "renamed from " + o.previous, value: o.value})
		case !existed:
			changed = append(changed, change{key: o.key, name: o.display(), why: "new", value: o.value})
		case len(was) != len(now):
			changed = append(changed, change{key: o.key, name: o.display(), why: "declared a different number of times", value: o.value})
		default:
			var fields []string
			for i := range now {
				fields = append(fields, differingFields(was[i], now[i])...)
			}
			if len(fields) > 0 {
				changed = append(changed, change{key: o.key, name: o.display(), why: "changed: " + strings.Join(dedupe(fields), ", "), value: o.value})
			}
		}
	}

	for _, o := range base {
		if _, still := headByKey[o.key]; still || renamedFrom[o.key] || done["-"+o.key] {
			continue
		}
		done["-"+o.key] = true
		removed = append(removed, o.display())
	}
	return changed, removed
}

// differingFields names the top-level fields in which two objects of the same
// type differ, by their YAML names.
func differingFields(a, b any) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for va.Kind() == reflect.Pointer && vb.Kind() == reflect.Pointer && !va.IsNil() && !vb.IsNil() {
		va, vb = va.Elem(), vb.Elem()
	}
	if va.Kind() != reflect.Struct || va.Type() != vb.Type() {
		if reflect.DeepEqual(a, b) {
			return nil
		}
		return []string{"value"}
	}
	var fields []string
	t := va.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			fields = append(fields, yamlName(f))
		}
	}
	return fields
}

func yamlName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := list[:0]
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// compareDevices selects the devices to reconcile: those whose own resolved
// declaration changed, and those that depend on something that did.
func compareDevices(base, head *loader.Dataset, changedTypes map[string]map[string]bool, res *Result, add func(kind, name, why string, obj any)) {
	devices := func(ds *loader.Dataset) []object {
		return each(ds.Devices, func(d *models.DeviceConfig) string { return d.Name },
			func(d *models.DeviceConfig) string { return d.RenameFrom })
	}
	changed, removed := diffObjects(devices(base), devices(head))
	for _, c := range changed {
		res.Devices[c.key] = true
		add("device", c.key, c.why, c.value)
	}
	for _, name := range removed {
		res.Removed = append(res.Removed, "device "+name)
	}
	// NetBox identifies a device by its name within a site. A device whose
	// site changed is therefore created anew, and the one at the old site is
	// what the next pruning sync removes.
	was := map[string]*models.DeviceConfig{}
	for _, d := range base.Devices {
		was[d.Name] = d
	}
	for _, d := range head.Devices {
		if old, ok := was[d.Name]; ok && old.SiteSlug != d.SiteSlug {
			res.Removed = append(res.Removed, fmt.Sprintf("device %s at site %s (it moved to %s, where NetBox gets a new device)", d.Name, old.SiteSlug, d.SiteSlug))
		}
	}

	// Dependencies. A device is built from its device type and its modules'
	// types, sits in a bay its parent provides, and its cables end on ports
	// its peers own: when one of those was changed, what NetBox holds for
	// this device may no longer be what its unchanged YAML asks for. Parents
	// and peers can chain, so repeat until nothing more is selected.
	selectDependent := func(d *models.DeviceConfig) string {
		if changedTypes["device type"][d.DeviceTypeSlug] {
			return "its device type " + d.DeviceTypeSlug + " changed"
		}
		for _, m := range d.Modules {
			if changedTypes["module type"][m.ModuleTypeSlug] {
				return "its module type " + m.ModuleTypeSlug + " changed"
			}
		}
		if d.ParentDevice != "" && res.Devices[d.ParentDevice] {
			return "its parent device " + d.ParentDevice + " is reconciled"
		}
		return ""
	}
	for again := true; again; {
		again = false
		for _, d := range head.Devices {
			if res.Devices[d.Name] {
				continue
			}
			if why := selectDependent(d); why != "" {
				res.Devices[d.Name] = true
				add("device", d.Name, why, d)
				again = true
			}
		}
	}

	// Cable peers are taken from the directly changed devices only (not
	// chained): a cable is re-checked when the device at its far end was
	// rebuilt, not because that device's own neighbour was.
	direct := map[string]bool{}
	for _, c := range changed {
		direct[c.key] = true
	}
	for _, d := range head.Devices {
		if res.Devices[d.Name] {
			continue
		}
		if peer := changedPeer(d, direct); peer != "" {
			res.Devices[d.Name] = true
			add("device", d.Name, "it declares a cable to "+peer+", which changed", d)
		}
	}
}

// links returns every cable a device declares, with the port it starts on.
func links(d *models.DeviceConfig) map[string]*models.LinkConfig {
	out := map[string]*models.LinkConfig{}
	for i := range d.Interfaces {
		if l := d.Interfaces[i].Link; l != nil {
			out[d.Interfaces[i].Name] = l
		}
	}
	for i := range d.FrontPorts {
		if l := d.FrontPorts[i].Link; l != nil {
			out["front "+d.FrontPorts[i].Name] = l
		}
	}
	for i := range d.RearPorts {
		if l := d.RearPorts[i].Link; l != nil {
			out["rear "+d.RearPorts[i].Name] = l
		}
	}
	return out
}

// changedPeer returns a device at the far end of one of d's cables (or along
// its path) that is in the changed set, or "".
func changedPeer(d *models.DeviceConfig, changed map[string]bool) string {
	ports := links(d)
	names := make([]string, 0, len(ports))
	for name := range ports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		l := ports[name]
		if changed[l.PeerDevice] {
			return l.PeerDevice
		}
		for _, hop := range l.Via {
			if changed[hop.Device] {
				return hop.Device
			}
		}
	}
	return ""
}

// compareVMs selects the VMs to reconcile: changed ones, and those in a
// cluster that changed.
func compareVMs(base, head *loader.Dataset, changedClusters map[string]bool, res *Result, add func(kind, name, why string, obj any)) {
	vms := func(ds *loader.Dataset) []object {
		return each(ds.VMs, func(v *models.VMConfig) string { return v.Name },
			func(v *models.VMConfig) string { return v.RenameFrom })
	}
	changed, removed := diffObjects(vms(base), vms(head))
	for _, c := range changed {
		res.VMs[c.key] = true
		add("VM", c.key, c.why, c.value)
	}
	for _, name := range removed {
		res.Removed = append(res.Removed, "VM "+name)
	}
	// A clustered VM is identified by its name within the cluster, so one
	// that moved into another cluster is created anew as well. (A VM that
	// left its cluster is looked up by name alone and stays the same VM.)
	wasVM := map[string]*models.VMConfig{}
	for _, vm := range base.VMs {
		wasVM[vm.Name] = vm
	}
	for _, vm := range head.VMs {
		if old, ok := wasVM[vm.Name]; ok && vm.Cluster != "" && old.Cluster != vm.Cluster {
			res.Removed = append(res.Removed, fmt.Sprintf("VM %s in %s (it moved to %s, where NetBox gets a new VM)", vm.Name, place(old.Cluster), place(vm.Cluster)))
		}
	}
	for _, vm := range head.VMs {
		if !res.VMs[vm.Name] && vm.Cluster != "" && changedClusters[vm.Cluster] {
			res.VMs[vm.Name] = true
			add("VM", vm.Name, "its cluster "+vm.Cluster+" changed", vm)
		}
	}
}

// removedParts lists the parts of devices and VMs that base declared and head
// no longer does, for objects that still exist: ports, modules, addresses and
// cables. (A removed device or VM is listed as a whole.)
func removedParts(base, head *loader.Dataset) []string {
	var removed []string

	headDevices := map[string]*models.DeviceConfig{}
	for _, d := range head.Devices {
		headDevices[d.Name] = d
		if d.RenameFrom != "" {
			headDevices[d.RenameFrom] = d
		}
	}
	for _, was := range base.Devices {
		now, ok := headDevices[was.Name]
		if !ok {
			continue
		}
		removed = append(removed, missing(deviceParts(was), deviceParts(now), now.Name)...)
	}

	headVMs := map[string]*models.VMConfig{}
	for _, vm := range head.VMs {
		headVMs[vm.Name] = vm
		if vm.RenameFrom != "" {
			headVMs[vm.RenameFrom] = vm
		}
	}
	for _, was := range base.VMs {
		now, ok := headVMs[was.Name]
		if !ok {
			continue
		}
		removed = append(removed, missing(vmParts(was), vmParts(now), now.Name)...)
	}
	return removed
}

// part is one prunable piece of a device or VM. kept lists the identities
// under which a later declaration still claims it (its name, and the name it
// says it was renamed from).
type part struct {
	label string
	keys  []string
}

func deviceParts(d *models.DeviceConfig) []part {
	var parts []part
	for _, m := range d.Modules {
		parts = append(parts, part{"module " + m.Name, []string{"module " + m.Name}})
	}
	for _, i := range d.Interfaces {
		p := part{"interface " + i.Name, []string{"interface " + i.Name}}
		if i.RenameFrom != "" {
			p.keys = append(p.keys, "interface "+i.RenameFrom)
		}
		parts = append(parts, p)
		if i.IP != nil && i.IP.Address != "" {
			parts = append(parts, ipPart(i.IP))
		}
		if i.Link != nil {
			parts = append(parts, cablePart(i.Name, i.Link))
		}
	}
	for _, p := range d.FrontPorts {
		parts = append(parts, part{"front port " + p.Name, []string{"front port " + p.Name}})
		if p.Link != nil {
			parts = append(parts, cablePart(p.Name, p.Link))
		}
	}
	for _, p := range d.RearPorts {
		parts = append(parts, part{"rear port " + p.Name, []string{"rear port " + p.Name}})
		if p.Link != nil {
			parts = append(parts, cablePart(p.Name, p.Link))
		}
	}
	return parts
}

func vmParts(vm *models.VMConfig) []part {
	var parts []part
	for _, i := range vm.Interfaces {
		p := part{"interface " + i.Name, []string{"interface " + i.Name}}
		if i.RenameFrom != "" {
			p.keys = append(p.keys, "interface "+i.RenameFrom)
		}
		parts = append(parts, p)
		if i.IP != nil && i.IP.Address != "" {
			parts = append(parts, ipPart(i.IP))
		}
	}
	return parts
}

func ipPart(ip *models.IPConfig) part {
	label := "address " + ip.Address
	if ip.VRF != "" {
		label += " (VRF " + ip.VRF + ")"
	}
	return part{label, []string{label}}
}

func cablePart(port string, l *models.LinkConfig) part {
	label := fmt.Sprintf("cable %s <-> %s[%s]", port, l.PeerDevice, l.PeerPort)
	return part{label, []string{label}}
}

// missing returns the parts of was that no part of now claims, as
// "<label> of <owner>".
func missing(was, now []part, owner string) []string {
	claimed := map[string]bool{}
	for _, p := range now {
		for _, k := range p.keys {
			claimed[k] = true
		}
	}
	var out []string
	for _, p := range was {
		if !claimed[p.keys[0]] {
			out = append(out, p.label+" of "+owner)
		}
	}
	return out
}

// place names where a VM lives, for the output.
func place(cluster string) string {
	if cluster == "" {
		return "no cluster"
	}
	return "cluster " + cluster
}
