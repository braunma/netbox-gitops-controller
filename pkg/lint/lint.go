// SPDX-License-Identifier: Apache-2.0

// Package lint checks a loaded YAML dataset for the mistakes that the typed
// model validation cannot see: a reference that resolves to nothing, two
// devices claiming the same rack unit, an IP used twice, a switch port cabled
// twice. Everything here is decided from the repository alone — no NetBox
// connection — so it runs in CI on a merge request, before an apply has a
// chance to do the wrong thing.
package lint

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Severity distinguishes a finding that is provably wrong from the repository
// alone (Error) from one that depends on state this tool cannot see (Warning).
type Severity int

const (
	// Warning marks a finding that may be legitimate: a reference to an object
	// this repository does not manage, or a redundancy that costs nothing but
	// noise.
	Warning Severity = iota
	// Error marks a finding that is wrong no matter what NetBox contains.
	Error
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Finding is a single lint result. Object names what the finding is about, in
// the terms the YAML uses ("device berlin-srv-web-01 interface eth0"), because
// the loader flattens files into typed objects and no longer knows which file a
// device came from.
type Finding struct {
	Severity Severity
	// Check is the stable identifier of the rule that produced the finding,
	// e.g. "duplicate-ip". Suitable for grepping and for suppression tooling.
	Check   string
	Object  string
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("[%s] %s: %s — %s", f.Check, f.Severity, f.Object, f.Message)
}

// Dataset is everything the linter reasons over. It is the loader's type: the
// data is loaded once and checked by whoever cares — this package from the
// repository alone, pkg/validate against a live NetBox.
type Dataset = loader.Dataset

// Options tunes how strictly references are judged.
type Options struct {
	// AllowUndeclaredRefs demotes "referenced but not declared here" from an
	// error to a warning. Set it when the repository deliberately manages only
	// part of NetBox and references objects created elsewhere.
	AllowUndeclaredRefs bool
}

// checker carries the indexes built once from the dataset.
type checker struct {
	ds   Dataset
	opts Options

	sites       map[string]bool
	racks       map[string]bool
	roles       map[string]bool
	vrfs        map[string]bool
	deviceTypes map[string]*models.DeviceType
	moduleTypes map[string]bool
	platforms   map[string]bool
	tenants     map[string]bool
	clusters    map[string]bool
	devices     map[string]*models.DeviceConfig
	// vlansBySite maps a site slug to the VLAN names declared at that site;
	// NetBox resolves an interface's VLAN within the device's site, so the same
	// name at another site is not a match.
	vlansBySite map[string]map[string]bool
	prefixes    []netip.Prefix

	findings []Finding
}

// Check runs every rule over the dataset and returns the findings in a stable
// order: errors first, then by object, then by check.
func Check(ds Dataset, opts Options) []Finding {
	c := &checker{ds: ds, opts: opts}
	c.index()

	c.checkDefinitions()
	c.checkDuplicateDeviceNames()
	c.checkDeviceReferences()
	c.checkInterfaces()
	c.checkModules()
	c.checkRackPositions()
	c.checkIPAddresses()
	c.checkCables()
	c.checkVMs()

	sort.SliceStable(c.findings, func(i, j int) bool {
		a, b := c.findings[i], c.findings[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Object != b.Object {
			return a.Object < b.Object
		}
		return a.Check < b.Check
	})
	return c.findings
}

func (c *checker) index() {
	c.sites = make(map[string]bool, len(c.ds.Sites))
	for _, s := range c.ds.Sites {
		c.sites[s.Slug] = true
	}
	c.racks = make(map[string]bool, len(c.ds.Racks))
	for _, r := range c.ds.Racks {
		c.racks[r.Slug] = true
	}
	c.roles = make(map[string]bool, len(c.ds.Roles))
	for _, r := range c.ds.Roles {
		c.roles[r.Slug] = true
	}
	c.vrfs = make(map[string]bool, len(c.ds.VRFs))
	for _, v := range c.ds.VRFs {
		c.vrfs[v.Name] = true
	}
	c.deviceTypes = make(map[string]*models.DeviceType, len(c.ds.DeviceTypes))
	for _, dt := range c.ds.DeviceTypes {
		c.deviceTypes[dt.Slug] = dt
	}
	c.platforms = make(map[string]bool, len(c.ds.Platforms))
	for _, p := range c.ds.Platforms {
		c.platforms[p.Slug] = true
		c.platforms[p.Name] = true
	}
	c.tenants = make(map[string]bool, len(c.ds.Tenants))
	for _, t := range c.ds.Tenants {
		c.tenants[t.Slug] = true
		c.tenants[t.Name] = true
	}
	c.clusters = make(map[string]bool, len(c.ds.Clusters))
	for _, cl := range c.ds.Clusters {
		c.clusters[cl.Name] = true
	}
	c.devices = make(map[string]*models.DeviceConfig, len(c.ds.Devices))
	for _, d := range c.ds.Devices {
		if _, seen := c.devices[d.Name]; !seen {
			c.devices[d.Name] = d
		}
	}
	c.vlansBySite = make(map[string]map[string]bool)
	for _, v := range c.ds.VLANs {
		if c.vlansBySite[v.SiteSlug] == nil {
			c.vlansBySite[v.SiteSlug] = make(map[string]bool)
		}
		c.vlansBySite[v.SiteSlug][v.Name] = true
	}
	c.indexModuleTypes()
	for _, p := range c.ds.Prefixes {
		if parsed, err := netip.ParsePrefix(p.Prefix); err == nil {
			c.prefixes = append(c.prefixes, parsed)
		}
	}
}

// indexModuleTypes mirrors the reconciler's key registration: a module type is
// referable by its slug, its model and its manufacturer-qualified slug — except
// that the unqualified keys are withheld when two manufacturers claim the same
// name, so an ambiguous reference is reported rather than silently resolved.
func (c *checker) indexModuleTypes() {
	owners := make(map[string]map[string]bool)
	for _, mt := range c.ds.ModuleTypes {
		if owners[mt.Slug] == nil {
			owners[mt.Slug] = make(map[string]bool, 1)
		}
		owners[mt.Slug][mt.Manufacturer] = true
	}

	c.moduleTypes = make(map[string]bool, len(c.ds.ModuleTypes)*2)
	for _, mt := range c.ds.ModuleTypes {
		c.moduleTypes[utils.Slugify(mt.Manufacturer)+"/"+mt.Slug] = true
		if len(owners[mt.Slug]) < 2 {
			c.moduleTypes[mt.Slug] = true
			c.moduleTypes[mt.Model] = true
		}
	}
}

func (c *checker) add(sev Severity, check, object, format string, args ...interface{}) {
	c.findings = append(c.findings, Finding{
		Severity: sev,
		Check:    check,
		Object:   object,
		Message:  fmt.Sprintf(format, args...),
	})
}

// reference reports a reference that resolves to nothing declared here. It is
// silent when the repository declares no objects of that kind at all: such a
// repository manages only part of NetBox, and every reference of that kind
// points at an object created elsewhere.
func (c *checker) reference(object, check, kind, value string, declared int) {
	if declared == 0 {
		return
	}
	sev := Error
	if c.opts.AllowUndeclaredRefs {
		sev = Warning
	}
	c.add(sev, check, object, "%s %q is not declared in this repository", kind, value)
}

func deviceRef(name string) string { return "device " + name }

func ifaceRef(device, iface string) string {
	return fmt.Sprintf("device %s interface %s", device, iface)
}

// checkDefinitions reports definitions that cannot be referenced as written.
func (c *checker) checkDefinitions() {
	for _, mt := range c.ds.ModuleTypes {
		if mt.Slug != "" {
			continue
		}
		c.add(Warning, "module-type-without-slug",
			fmt.Sprintf("module type %s %s", mt.Manufacturer, mt.Model),
			"declares no slug, so module_type_slug can only reference it by its model (%q); give it a slug to reference it by a short key",
			mt.Model)
	}
}

func (c *checker) checkDuplicateDeviceNames() {
	seen := make(map[string]bool, len(c.ds.Devices))
	for _, d := range c.ds.Devices {
		if seen[d.Name] {
			c.add(Error, "duplicate-device", deviceRef(d.Name),
				"declared more than once; NetBox identifies a device by name, so the second declaration overwrites the first")
			continue
		}
		seen[d.Name] = true
	}
}

func (c *checker) checkDeviceReferences() {
	for _, d := range c.ds.Devices {
		ref := deviceRef(d.Name)
		if d.SiteSlug != "" && !c.sites[d.SiteSlug] {
			c.reference(ref, "unknown-site", "site", d.SiteSlug, len(c.ds.Sites))
		}
		if d.RoleSlug != "" && !c.roles[d.RoleSlug] {
			c.reference(ref, "unknown-role", "device role", d.RoleSlug, len(c.ds.Roles))
		}
		if d.RackSlug != "" && !c.racks[d.RackSlug] {
			c.reference(ref, "unknown-rack", "rack", d.RackSlug, len(c.ds.Racks))
		}
		if d.DeviceTypeSlug != "" && c.deviceTypes[d.DeviceTypeSlug] == nil {
			c.reference(ref, "unknown-device-type", "device type", d.DeviceTypeSlug, len(c.ds.DeviceTypes))
		}
		if d.RackSlug != "" && d.Position == 0 && d.ParentDevice == "" {
			c.add(Warning, "unracked-device", ref,
				"names a rack but no position, so NetBox files it as non-racked equipment in %s", d.RackSlug)
		}
		if d.RackSlug != "" && d.Position != 0 && d.Face == "" && d.ParentDevice == "" {
			// NetBox answers 400 "Must specify rack face when defining rack
			// position" — the device is never created, and every interface,
			// IP and cable that hangs off it is lost with it.
			c.add(Error, "position-without-face", ref,
				"is placed at U%d but names no face; NetBox rejects a rack position without one", d.Position)
		}
		c.checkDeviceBay(d)
	}
}

// checkDeviceBay validates a child device's installation into its parent.
func (c *checker) checkDeviceBay(d *models.DeviceConfig) {
	if d.ParentDevice == "" {
		return
	}
	ref := deviceRef(d.Name)
	parent, ok := c.devices[d.ParentDevice]
	if !ok {
		c.reference(ref, "unknown-parent-device", "parent device", d.ParentDevice, len(c.ds.Devices))
		return
	}
	if d.DeviceBay == "" {
		c.add(Error, "missing-device-bay", ref,
			"names parent device %s but no device_bay to install into", d.ParentDevice)
		return
	}
	parentType := c.deviceTypes[parent.DeviceTypeSlug]
	if parentType == nil {
		return
	}
	for _, bay := range parentType.DeviceBays {
		if bay.Name == d.DeviceBay {
			return
		}
	}
	c.add(Error, "unknown-device-bay", ref,
		"device bay %q does not exist on device type %s (parent %s)",
		d.DeviceBay, parent.DeviceTypeSlug, d.ParentDevice)
}

func (c *checker) checkInterfaces() {
	for _, d := range c.ds.Devices {
		dt := c.deviceTypes[d.DeviceTypeSlug]
		templates := interfaceTemplates(dt)

		seen := make(map[string]bool, len(d.Interfaces))
		for _, iface := range d.Interfaces {
			ref := ifaceRef(d.Name, iface.Name)
			if seen[iface.Name] {
				c.add(Error, "duplicate-interface", ref,
					"declared more than once on this device")
			}
			seen[iface.Name] = true

			tmpl, inTemplate := templates[iface.Name]
			switch {
			case !inTemplate && iface.Type == "" && dt != nil:
				c.add(Error, "untyped-interface", ref,
					"is not an interface template on device type %s and declares no type, so NetBox has nothing to create it from",
					d.DeviceTypeSlug)
			case !inTemplate && dt != nil && isPhysicalType(iface.Type) && len(d.Modules) == 0:
				// A physical port that the hardware model does not have is
				// usually a name that drifted from the template. Interfaces a
				// module contributes are not in the device type, so a device
				// with modules is left alone.
				c.add(Warning, "interface-not-in-template", ref,
					"is not an interface template on device type %s, so it is created in addition to the hardware's ports",
					d.DeviceTypeSlug)
			case inTemplate && iface.Type != "" && iface.Type == tmpl.Type:
				c.add(Warning, "redundant-interface-type", ref,
					"repeats type %q from the device type template; it can be deleted", iface.Type)
			case inTemplate && iface.Type != "" && iface.Type != tmpl.Type:
				c.add(Warning, "interface-type-override", ref,
					"declares type %q but the device type template says %q", iface.Type, tmpl.Type)
			}

			if iface.Enabled != nil && *iface.Enabled {
				c.add(Warning, "redundant-enabled", ref,
					"sets enabled: true, which is already the default; it can be deleted")
			}

			c.checkInterfaceVLANs(d, iface, ref)

			for _, member := range iface.Members {
				if !seen[member] && !hasInterface(d, member) && templates[member].Name == "" {
					c.add(Error, "unknown-lag-member", ref,
						"LAG member %q is neither declared on this device nor an interface template on %s",
						member, d.DeviceTypeSlug)
				}
			}
		}
	}
}

func (c *checker) checkInterfaceVLANs(d *models.DeviceConfig, iface models.InterfaceConfig, ref string) {
	siteVLANs := c.vlansBySite[d.SiteSlug]
	declared := len(siteVLANs)
	if iface.UntaggedVLAN != "" && !siteVLANs[iface.UntaggedVLAN] {
		c.reference(ref, "unknown-vlan", "VLAN", iface.UntaggedVLAN+" (at site "+d.SiteSlug+")", declared)
	}
	for _, vlan := range iface.TaggedVLANs {
		if !siteVLANs[vlan] {
			c.reference(ref, "unknown-vlan", "VLAN", vlan+" (at site "+d.SiteSlug+")", declared)
		}
	}
	if iface.IP != nil && iface.IP.VRF != "" && !c.vrfs[iface.IP.VRF] {
		c.reference(ref, "unknown-vrf", "VRF", iface.IP.VRF, len(c.ds.VRFs))
	}
}

func (c *checker) checkModules() {
	for _, d := range c.ds.Devices {
		dt := c.deviceTypes[d.DeviceTypeSlug]
		bays := make(map[string]bool)
		if dt != nil {
			for _, bay := range dt.ModuleBays {
				bays[bay.Name] = true
			}
		}
		for _, mod := range d.Modules {
			ref := fmt.Sprintf("device %s module %s", d.Name, mod.Name)
			if dt != nil && !bays[mod.Name] {
				c.add(Error, "unknown-module-bay", ref,
					"is not a module bay on device type %s", d.DeviceTypeSlug)
			}
			if mod.ModuleTypeSlug != "" && !c.moduleTypes[mod.ModuleTypeSlug] {
				c.reference(ref, "unknown-module-type", "module type", mod.ModuleTypeSlug, len(c.ds.ModuleTypes))
			}
		}
	}
}

// rackSlot is a device's occupancy of a rack: the lowest unit it sits in and
// how many units it takes.
type rackSlot struct {
	device string
	from   float64
	to     float64
}

// checkRackPositions reports two devices occupying the same rack unit. NetBox
// rejects the overlap at apply time, one device at a time and only once the
// first has been created — cheaper to hear about here, for every collision at
// once.
func (c *checker) checkRackPositions() {
	occupancy := make(map[string][]rackSlot)
	for _, d := range c.ds.Devices {
		if d.RackSlug == "" || d.Position == 0 {
			continue
		}
		dt := c.deviceTypes[d.DeviceTypeSlug]
		height := 1.0
		if dt != nil {
			if dt.UHeight == 0 {
				// A child device consumes no rack units of its own.
				continue
			}
			height = dt.UHeight
		}
		// Rack units are numbered upwards from the device's position. A
		// device that names no face is mounted at the front, so it still
		// collides with a front-mounted neighbour.
		face := strings.ToLower(d.Face)
		if face == "" {
			face = "front"
		}
		key := d.RackSlug + "\x00" + face
		occupancy[key] = append(occupancy[key], rackSlot{
			device: d.Name,
			from:   float64(d.Position),
			to:     float64(d.Position) + height,
		})
	}

	keys := make([]string, 0, len(occupancy))
	for key := range occupancy {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		slots := occupancy[key]
		sort.SliceStable(slots, func(i, j int) bool { return slots[i].from < slots[j].from })
		rack := strings.SplitN(key, "\x00", 2)[0]
		for i := 1; i < len(slots); i++ {
			prev, cur := slots[i-1], slots[i]
			if cur.from < prev.to {
				c.add(Error, "rack-collision", deviceRef(cur.device),
					"occupies U%g in rack %s, which %s already fills (U%g–U%g)",
					cur.from, rack, prev.device, prev.from, prev.to-1)
			}
		}
	}
}

// ipOwner records where an address was declared, so a duplicate can name both
// ends of the collision.
type ipOwner struct {
	object string
}

func (c *checker) checkIPAddresses() {
	owners := make(map[string]ipOwner)

	record := func(object string, ip *models.IPConfig) {
		if ip == nil || ip.Address == "" {
			return
		}
		addr, err := netip.ParsePrefix(ip.Address)
		if err != nil {
			c.add(Error, "invalid-ip", object,
				"IP %q is not a valid address with prefix length (expected e.g. 10.0.0.1/24)", ip.Address)
			return
		}
		if addr.Bits() < addr.Addr().BitLen() && addr.Addr().Is4() {
			if isNetworkAddress(addr) {
				c.add(Error, "network-address", object,
					"IP %s is the network address of its own subnet, not a host address", ip.Address)
			} else if isBroadcastAddress(addr) {
				c.add(Error, "broadcast-address", object,
					"IP %s is the broadcast address of its own subnet, not a host address", ip.Address)
			}
		}

		// NetBox scopes uniqueness per VRF; the global table is a VRF of its own.
		key := ip.VRF + "\x00" + addr.Addr().String()
		if prev, dup := owners[key]; dup {
			where := "the global table"
			if ip.VRF != "" {
				where = "VRF " + ip.VRF
			}
			c.add(Error, "duplicate-ip", object,
				"IP %s is already declared on %s in %s", addr.Addr(), prev.object, where)
			return
		}
		owners[key] = ipOwner{object: object}

		if len(c.prefixes) > 0 && !c.withinDeclaredPrefix(addr.Addr()) {
			c.add(Warning, "ip-outside-prefix", object,
				"IP %s falls outside every prefix declared in definitions/prefixes", addr.Addr())
		}
	}

	for _, d := range c.ds.Devices {
		primaries := make(map[bool]string)
		for _, iface := range d.Interfaces {
			ref := ifaceRef(d.Name, iface.Name)
			record(ref, iface.IP)
			if !iface.IsPrimaryIP() || iface.IP == nil {
				continue
			}
			addr, err := netip.ParsePrefix(iface.IP.Address)
			if err != nil {
				continue
			}
			if prev, dup := primaries[addr.Addr().Is4()]; dup {
				c.add(Error, "duplicate-primary-ip", deviceRef(d.Name),
					"interfaces %s and %s are both marked primary for the same address family; NetBox holds one primary IP per family",
					prev, iface.Name)
				continue
			}
			primaries[addr.Addr().Is4()] = iface.Name
		}
	}

	for _, vm := range c.ds.VMs {
		for _, iface := range vm.Interfaces {
			record(fmt.Sprintf("virtual machine %s interface %s", vm.Name, iface.Name), iface.IP)
		}
	}
}

func (c *checker) withinDeclaredPrefix(addr netip.Addr) bool {
	for _, p := range c.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// cableEnd is one end of a declared cable.
type cableEnd struct {
	device string
	port   string
}

func (e cableEnd) String() string { return e.device + "[" + e.port + "]" }

// checkCables reports a port that two different cables claim, and notes the
// cables declared from both ends — legal, but it makes every re-patch a
// two-file edit.
func (c *checker) checkCables() {
	type declaration struct {
		local cableEnd
		peer  cableEnd
	}
	var declarations []declaration

	collect := func(device string, port string, link *models.LinkConfig) {
		if link == nil {
			return
		}
		declarations = append(declarations, declaration{
			local: cableEnd{device: device, port: port},
			peer:  cableEnd{device: link.PeerDevice, port: link.PeerPort},
		})
	}

	for _, d := range c.ds.Devices {
		for _, iface := range d.Interfaces {
			collect(d.Name, iface.Name, iface.Link)
		}
		for _, port := range d.FrontPorts {
			collect(d.Name, port.Name, port.Link)
		}
		for _, port := range d.RearPorts {
			collect(d.Name, port.Name, port.Link)
		}
	}

	// A cable is the unordered pair of its ends; declaring it from both sides
	// describes one cable, not two.
	pairs := make(map[string][]declaration)
	for _, decl := range declarations {
		ends := []string{decl.local.String(), decl.peer.String()}
		sort.Strings(ends)
		key := ends[0] + " <-> " + ends[1]
		pairs[key] = append(pairs[key], decl)
	}

	// Each end may belong to exactly one cable.
	endToPairs := make(map[string]map[string]bool)
	for key, decls := range pairs {
		for _, decl := range decls {
			for _, end := range []cableEnd{decl.local, decl.peer} {
				if endToPairs[end.String()] == nil {
					endToPairs[end.String()] = make(map[string]bool)
				}
				endToPairs[end.String()][key] = true
			}
		}
	}

	reported := make(map[string]bool)
	ends := make([]string, 0, len(endToPairs))
	for end := range endToPairs {
		ends = append(ends, end)
	}
	sort.Strings(ends)
	for _, end := range ends {
		if len(endToPairs[end]) < 2 {
			continue
		}
		cables := make([]string, 0, len(endToPairs[end]))
		for key := range endToPairs[end] {
			cables = append(cables, key)
		}
		sort.Strings(cables)
		if reported[strings.Join(cables, "|")] {
			continue
		}
		reported[strings.Join(cables, "|")] = true
		c.add(Error, "cable-conflict", "port "+end,
			"is claimed by %d different cables (%s); a port holds one cable",
			len(cables), strings.Join(cables, ", "))
	}

	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		decls := pairs[key]
		if len(decls) > 1 {
			c.add(Warning, "cable-declared-twice", "cable "+key,
				"is declared from both ends; one declaration wires both sides, and two means every re-patch is a two-place edit")
		}
		c.checkCablePeer(decls[0].peer, decls[0].local)
	}
}

// checkCablePeer validates the far end of a cable when this repository
// declares the peer device: a port that exists neither as a template nor as a
// declaration will not be found at apply time and the cable is silently skipped.
func (c *checker) checkCablePeer(peer, local cableEnd) {
	device, declared := c.devices[peer.device]
	if !declared {
		c.add(Warning, "undeclared-peer-device", "port "+local.String(),
			"is cabled to %s, which this repository does not declare; it must already exist in NetBox", peer.device)
		return
	}
	if hasPort(device, peer.port) {
		return
	}
	if dt := c.deviceTypes[device.DeviceTypeSlug]; dt != nil {
		if _, ok := interfaceTemplates(dt)[peer.port]; ok {
			return
		}
		for _, p := range dt.FrontPorts {
			if p.Name == peer.port {
				return
			}
		}
		for _, p := range dt.RearPorts {
			if p.Name == peer.port {
				return
			}
		}
	} else {
		// Without the device type there is no template to check against, and
		// the port may well be one of its components.
		return
	}
	c.add(Error, "unknown-peer-port", "port "+local.String(),
		"is cabled to %s, but %s has no such port on device type %s",
		peer, peer.device, device.DeviceTypeSlug)
}

func (c *checker) checkVMs() {
	seen := make(map[string]bool, len(c.ds.VMs))
	for _, vm := range c.ds.VMs {
		ref := "virtual machine " + vm.Name
		if seen[vm.Name] {
			c.add(Error, "duplicate-vm", ref, "declared more than once")
		}
		seen[vm.Name] = true

		if vm.SiteSlug != "" && !c.sites[vm.SiteSlug] {
			c.reference(ref, "unknown-site", "site", vm.SiteSlug, len(c.ds.Sites))
		}
		if vm.RoleSlug != "" && !c.roles[vm.RoleSlug] {
			c.reference(ref, "unknown-role", "device role", vm.RoleSlug, len(c.ds.Roles))
		}
		if vm.Cluster != "" && !c.clusters[vm.Cluster] {
			c.reference(ref, "unknown-cluster", "cluster", vm.Cluster, len(c.ds.Clusters))
		}
		if vm.Platform != "" && !c.platforms[vm.Platform] {
			c.reference(ref, "unknown-platform", "platform", vm.Platform, len(c.ds.Platforms))
		}
		if vm.Tenant != "" && !c.tenants[vm.Tenant] {
			c.reference(ref, "unknown-tenant", "tenant", vm.Tenant, len(c.ds.Tenants))
		}

		ifaces := make(map[string]bool, len(vm.Interfaces))
		for _, iface := range vm.Interfaces {
			iref := fmt.Sprintf("virtual machine %s interface %s", vm.Name, iface.Name)
			if ifaces[iface.Name] {
				c.add(Error, "duplicate-interface", iref, "declared more than once on this virtual machine")
			}
			ifaces[iface.Name] = true
			if iface.IP != nil && iface.IP.VRF != "" && !c.vrfs[iface.IP.VRF] {
				c.reference(iref, "unknown-vrf", "VRF", iface.IP.VRF, len(c.ds.VRFs))
			}
		}
	}
}

// interfaceTemplates indexes a device type's interface templates by name.
func interfaceTemplates(dt *models.DeviceType) map[string]models.InterfaceTemplate {
	templates := make(map[string]models.InterfaceTemplate)
	if dt == nil {
		return templates
	}
	for _, tmpl := range dt.Interfaces {
		templates[tmpl.Name] = tmpl
	}
	return templates
}

func hasInterface(d *models.DeviceConfig, name string) bool {
	for _, iface := range d.Interfaces {
		if iface.Name == name {
			return true
		}
	}
	return false
}

func hasPort(d *models.DeviceConfig, name string) bool {
	if hasInterface(d, name) {
		return true
	}
	for _, p := range d.FrontPorts {
		if p.Name == name {
			return true
		}
	}
	for _, p := range d.RearPorts {
		if p.Name == name {
			return true
		}
	}
	return false
}

// isPhysicalType reports whether an interface type describes a physical port,
// as opposed to one NetBox creates without hardware behind it.
func isPhysicalType(ifaceType string) bool {
	switch ifaceType {
	case "", "virtual", "bridge", "lag":
		return false
	}
	return true
}

// isNetworkAddress reports whether the address is the all-zero host of its own
// prefix. A /31 (RFC 3021 point-to-point link) and a /32 have no such address.
func isNetworkAddress(p netip.Prefix) bool {
	if p.Bits() >= p.Addr().BitLen()-1 {
		return false
	}
	return p.Masked().Addr() == p.Addr()
}

// isBroadcastAddress reports whether the address is the all-ones host of its
// own IPv4 prefix.
func isBroadcastAddress(p netip.Prefix) bool {
	if p.Bits() >= p.Addr().BitLen()-1 {
		return false
	}
	bytes := p.Masked().Addr().As4()
	hostBits := 32 - p.Bits()
	value := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	value |= (uint32(1) << hostBits) - 1
	broadcast := netip.AddrFrom4([4]byte{
		byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value),
	})
	return broadcast == p.Addr()
}

// HasErrors reports whether any finding is fatal at the given strictness.
func HasErrors(findings []Finding, strict bool) bool {
	for _, f := range findings {
		if f.Severity == Error || strict {
			return true
		}
	}
	return false
}
