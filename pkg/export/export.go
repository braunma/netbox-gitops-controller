// SPDX-License-Identifier: Apache-2.0

// Package export reads what a NetBox instance already holds and writes it as
// this repository's YAML, so that objects created by hand (or by another tool)
// can be brought under GitOps management without retyping them.
//
// The output is built so that the first sync after it is merged changes
// nothing but the managed tag: every object is declared under the identity it
// already has (so the sync adopts it instead of creating a second one), and
// every field the reconciler would send carries the value NetBox already
// holds. In particular an object's existing tags are exported and defined,
// because a sync sets an object's tag list to exactly what the YAML declares.
//
// Nothing here writes to NetBox: an export only issues GET requests.
//
// Objects the repository already declares are skipped, so an export can be
// run again after part of it was merged, and against a repository that
// already manages some of the instance.
package export

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Lister is the one call an export needs: a paginated GET of an endpoint.
// *client.NetBoxClient satisfies it.
type Lister interface {
	List(path string, filters map[string]interface{}) ([]client.Object, error)
}

// FileStem is the base name of the files an export writes.
const FileStem = "imported"

// Options configure an export.
type Options struct {
	// Declared is what the repository already declares. Those objects are
	// not exported again. Nil means the repository is empty.
	Declared *loader.Dataset
	// All also exports the objects Declared holds: a reference of everything
	// the instance holds, to compare the repository against (a field added
	// to the YAML after an object was adopted, such as `tenant`, shows up
	// there). Declared still decides how references are spelled, so a
	// device's `rack` uses the slug the repository gave that rack. Such an
	// export declares objects a second time and must not be written into
	// the data directory.
	All bool
	// Sites, when set, limits the export to objects at these site slugs (and
	// to the shared definitions — tags, roles, types, ... — that every site
	// uses).
	Sites []string
	// Progress, when set, is told what is being fetched.
	Progress func(format string, args ...interface{})
}

// File is one YAML file of the export, relative to the data directory.
type File struct {
	Path    string
	Kind    string
	Objects int
	Data    []byte
}

// Result is what an export produced.
type Result struct {
	Files []File
	// Exported and AlreadyDeclared count objects per kind.
	Exported        map[string]int
	AlreadyDeclared map[string]int
	// Warnings describe what NetBox holds that the YAML cannot carry, and
	// what was therefore left out. Each one is worth reading before merging.
	Warnings []string
}

// endpoints is everything an export reads, in the order it is fetched.
var endpoints = []string{
	"extras/tags", "extras/custom-fields",
	"dcim/device-roles", "dcim/platforms",
	"tenancy/tenant-groups", "tenancy/tenants",
	"dcim/sites", "dcim/racks",
	"ipam/vrfs", "ipam/vlan-groups", "ipam/vlans", "ipam/prefixes",
	"dcim/device-types", "dcim/module-types",
	"dcim/interface-templates", "dcim/front-port-templates", "dcim/rear-port-templates",
	"dcim/console-port-templates", "dcim/console-server-port-templates",
	"dcim/power-port-templates", "dcim/power-outlet-templates",
	"dcim/module-bay-templates", "dcim/device-bay-templates",
	"virtualization/cluster-types", "virtualization/cluster-groups", "virtualization/clusters",
	"dcim/devices", "dcim/interfaces", "dcim/front-ports", "dcim/rear-ports", "dcim/modules",
	"dcim/cables", "ipam/ip-addresses",
	"virtualization/virtual-machines", "virtualization/interfaces",
}

type exporter struct {
	opts     Options
	data     map[string][]client.Object
	declared declaredIndex
	res      *Result
	sites    map[string]bool // the --site filter; nil means every site

	siteSlugByID  map[int]string
	deviceByID    map[int]client.Object
	roleOfDevice  map[int]string
	vlanByID      map[int]client.Object
	moduleTypeRef map[int]string             // module type ID -> the reference a module uses
	ipsByIface    map[string][]client.Object // "dcim.interface:12" -> IPs
	uncarried     map[string]int
}

// Export reads the instance and returns the files to write.
func Export(l Lister, opts Options) (*Result, error) {
	e := &exporter{
		opts:     opts,
		data:     make(map[string][]client.Object, len(endpoints)),
		declared: indexDeclared(opts.Declared, opts.All),
		res: &Result{
			Exported:        map[string]int{},
			AlreadyDeclared: map[string]int{},
		},
	}
	if len(opts.Sites) > 0 {
		e.sites = make(map[string]bool, len(opts.Sites))
		for _, s := range opts.Sites {
			e.sites[s] = true
		}
	}

	for _, ep := range endpoints {
		if opts.Progress != nil {
			opts.Progress("Reading %s...", ep)
		}
		objs, err := l.List("/api/"+ep+"/", nil)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", ep, err)
		}
		e.data[ep] = objs
	}
	e.index()

	// Inventory first: what it references decides which shared definitions
	// (tags in particular) an export needs to carry.
	devices := e.exportDevices()
	e.exportCables(devices)
	vms := e.exportVMs()

	e.writeVMs(vms)
	e.exportDefinitions()
	return e.res, nil
}

func (e *exporter) index() {
	e.siteSlugByID = map[int]string{}
	for _, s := range e.data["dcim/sites"] {
		e.siteSlugByID[idOf(s)] = str(s, "slug")
	}
	e.deviceByID = map[int]client.Object{}
	e.roleOfDevice = map[int]string{}
	for _, d := range e.data["dcim/devices"] {
		e.deviceByID[idOf(d)] = d
		e.roleOfDevice[idOf(d)] = deviceRole(d)
	}
	e.vlanByID = map[int]client.Object{}
	for _, v := range e.data["ipam/vlans"] {
		e.vlanByID[idOf(v)] = v
	}
	e.ipsByIface = map[string][]client.Object{}
	for _, ip := range e.data["ipam/ip-addresses"] {
		t := str(ip, "assigned_object_type")
		id := utils.GetIDFromObject(ip["assigned_object_id"])
		if t == "" || id == 0 {
			continue
		}
		key := t + ":" + strconv.Itoa(id)
		e.ipsByIface[key] = append(e.ipsByIface[key], ip)
	}
	e.indexModuleTypeRefs()
}

// deviceRole reads a device's role slug: `role` since NetBox 4.0,
// `device_role` before.
func deviceRole(d map[string]interface{}) string {
	if s := nestedStr(d, "role", "slug"); s != "" {
		return s
	}
	return nestedStr(d, "device_role", "slug")
}

// inScope reports whether a site slug passes the --site filter.
func (e *exporter) inScope(site string) bool {
	return e.sites == nil || e.sites[site]
}

func (e *exporter) warn(format string, args ...interface{}) {
	e.res.Warnings = append(e.res.Warnings, fmt.Sprintf(format, args...))
}

// tags returns an object's tags, without the managed one. Every tag the
// instance holds is exported as a definition, so each one resolves.
func (e *exporter) tags(obj map[string]interface{}) []string { return tagSlugs(obj) }

// untaggable warns about tags on an object whose model has no `tags` key.
// Adopting it would set its tags to the managed tag alone.
func (e *exporter) untaggable(kind, name string, obj map[string]interface{}) {
	if t := tagSlugs(obj); len(t) > 0 {
		e.warn("%s %s has tags %v, which this repository cannot declare for a %s; the first sync replaces them with the managed tag", kind, name, t, kind)
	}
}

// addFile renders a list of objects into a file, unless there are none.
func (e *exporter) addFile(p, kind string, objects interface{}, n int, header string) {
	if n == 0 {
		return
	}
	data, err := render(objects, header, e.opts.All)
	if err != nil {
		// The models are plain structs; marshalling them cannot fail short
		// of a programming error, and dropping a file silently would be worse.
		panic(fmt.Sprintf("export: rendering %s: %v", p, err))
	}
	e.res.Files = append(e.res.Files, File{Path: p, Kind: kind, Objects: n, Data: data})
	e.res.Exported[kind] += n
}

func (e *exporter) skipDeclared(kind string) { e.res.AlreadyDeclared[kind]++ }

// status drops "active", which is what an omitted status means everywhere.
func status(o map[string]interface{}) string {
	if s := choice(o, "status"); s != "active" {
		return s
	}
	return ""
}

// fileName makes a string safe and readable as a file name.
func fileName(s string) string {
	if f := utils.Slugify(s); f != "" {
		return f
	}
	return "unnamed"
}

func sortBy[T any](items []T, key func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return key(items[i]) < key(items[j]) })
}

func defsPath(folder string) string { return path.Join("definitions", folder, FileStem+".yaml") }

func joinSorted(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
