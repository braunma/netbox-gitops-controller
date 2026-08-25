// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"fmt"
	"path"
	"sort"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// deviceIndex holds everything the device phase joins in memory, so the whole
// phase costs a fixed set of list requests regardless of estate size.
type deviceIndex struct {
	devicesByID map[int]client.Object
	ifByDevice  map[int][]client.Object
	ifByID      map[int]client.Object
	frontByDev  map[int][]client.Object
	rearByDev   map[int][]client.Object
	portByID    map[int]client.Object // front+rear ports, for cable peers
	modsByDev   map[int][]client.Object
	baysByChild map[int]client.Object // device-bay keyed by installed_device id
	ipsByIface  map[int][]client.Object
	cables      []client.Object
	// device type interface templates: device_type id -> interface name -> template
	ifTmplByDT map[int]map[string]client.Object
	dtByID     map[int]client.Object
	deviceCF   map[string]bool // custom-field names applicable to dcim.device
}

// devices imports concrete devices with their interfaces, IPs, cables, modules
// and bay placement, partitioned into files by --split-by.
func (rc *runContext) devices() error {
	idx, err := rc.buildDeviceIndex()
	if err != nil {
		return err
	}
	devices, err := rc.f.list("dcim", "devices", nil)
	if err != nil {
		return err
	}

	// path -> device items, so defaults extraction runs per file (which is
	// what makes --split-by site able to hoist the shared site_slug/rack_slug).
	files := map[string][]interface{}{}
	passive := map[string]bool{}
	exported := 0

	for _, o := range devices {
		if !rc.keep(o) {
			continue
		}
		siteSlug := refSlug(o, "site")
		if !rc.siteAllowed(siteSlug) {
			continue
		}
		dev, isPassive := rc.mapDevice(o, idx)
		if dev == nil {
			continue
		}
		p := rc.devicePath(dev, o, isPassive, idx)
		files[p] = append(files[p], dev)
		passive[p] = isPassive
		exported++
	}
	rc.report.count("dcim/devices", len(devices), exported)

	for p, items := range files {
		sort.Slice(items, func(i, j int) bool {
			return items[i].(*models.DeviceConfig).Name < items[j].(*models.DeviceConfig).Name
		})
		header := "Devices in this site/rack."
		if passive[p] {
			header = "Passive devices (patch panels and the like) in this site/rack."
		}
		if err := rc.emit(p, genHeader(header), "devices", items); err != nil {
			return err
		}
	}
	return nil
}

// buildDeviceIndex fetches and indexes every endpoint the device phase joins.
func (rc *runContext) buildDeviceIndex() (deviceIndex, error) {
	var idx deviceIndex
	var err error

	load := func(endpoint string) ([]client.Object, error) {
		return rc.f.list("dcim", endpoint, nil)
	}

	devs, err := load("devices")
	if err != nil {
		return idx, err
	}
	idx.devicesByID = byID(devs)

	ifaces, err := load("interfaces")
	if err != nil {
		return idx, err
	}
	idx.ifByDevice = groupBy(ifaces, "device")
	idx.ifByID = byID(ifaces)

	fronts, err := load("front-ports")
	if err != nil {
		return idx, err
	}
	rears, err := load("rear-ports")
	if err != nil {
		return idx, err
	}
	idx.frontByDev = groupBy(fronts, "device")
	idx.rearByDev = groupBy(rears, "device")
	idx.portByID = map[int]client.Object{}
	for _, p := range append(append([]client.Object{}, fronts...), rears...) {
		idx.portByID[idOf(p)] = p
	}

	mods, err := load("modules")
	if err != nil {
		return idx, err
	}
	idx.modsByDev = groupBy(mods, "device")

	bays, err := load("device-bays")
	if err != nil {
		return idx, err
	}
	idx.baysByChild = map[int]client.Object{}
	for _, b := range bays {
		if child := nested(b, "installed_device"); child != nil {
			idx.baysByChild[idOf(client.Object(child))] = b
		}
	}

	ips, err := rc.f.list("ipam", "ip-addresses", nil)
	if err != nil {
		return idx, err
	}
	idx.ipsByIface = map[int][]client.Object{}
	for _, ip := range ips {
		if t, _ := ip["assigned_object_type"].(string); t == "dcim.interface" {
			id := utils.GetIDFromObject(ip["assigned_object_id"])
			idx.ipsByIface[id] = append(idx.ipsByIface[id], ip)
		}
	}

	idx.cables, err = load("cables")
	if err != nil {
		return idx, err
	}

	dts, err := load("device-types")
	if err != nil {
		return idx, err
	}
	idx.dtByID = byID(dts)
	ifTmpl, err := load("interface-templates")
	if err != nil {
		return idx, err
	}
	idx.ifTmplByDT = map[int]map[string]client.Object{}
	for dtID, list := range groupBy(ifTmpl, "device_type") {
		names := map[string]client.Object{}
		for _, tmpl := range list {
			names[str(tmpl, "name")] = tmpl
		}
		idx.ifTmplByDT[dtID] = names
	}

	cfs, err := rc.f.list("extras", "custom-fields", nil)
	if err != nil {
		return idx, err
	}
	idx.deviceCF = map[string]bool{}
	for _, cf := range cfs {
		for _, ot := range stringList(cf, "object_types") {
			if ot == "dcim.device" {
				idx.deviceCF[str(cf, "name")] = true
			}
		}
	}

	return idx, nil
}

// mapDevice maps one NetBox device to a DeviceConfig, reporting whether it is a
// passive device (ports but no interfaces). Returns nil to skip.
func (rc *runContext) mapDevice(o client.Object, idx deviceIndex) (*models.DeviceConfig, bool) {
	id := idOf(o)
	dev := &models.DeviceConfig{
		Name:           rc.nameOut(str(o, "name")),
		SiteSlug:       rc.siteOut(refSlug(o, "site")),
		DeviceTypeSlug: refSlug(o, "device_type"),
		RoleSlug:       deviceRoleSlug(o),
		Status:         choiceValue(o, "status"),
		Serial:         str(o, "serial"),
		AssetTag:       str(o, "asset_tag"),
		Tags:           rc.tags(o),
	}

	// Child device (installed in a parent's device bay) vs racked device.
	if bay, ok := idx.baysByChild[id]; ok {
		dev.ParentDevice = rc.nameOut(refName(bay, "device"))
		dev.DeviceBay = str(bay, "name")
	} else {
		if rackName := refName(o, "rack"); rackName != "" {
			dev.RackSlug = rc.rackSlugOut(rackName)
		}
		dev.Position = int(floatOf(o, "position"))
		dev.Face = choiceValue(o, "face")
	}

	dev.CustomFields = rc.deviceCustomFields(o, idx)

	primary := primaryIPIDs(o)
	dev.Interfaces = rc.mapInterfaces(o, idx, primary)
	dev.Modules = mapModules(idx.modsByDev[id])

	frontPorts, rearPorts := rc.mapPassivePorts(id, idx)
	dev.FrontPorts = frontPorts
	dev.RearPorts = rearPorts

	isPassive := len(idx.ifByDevice[id]) == 0 && (len(idx.frontByDev[id])+len(idx.rearByDev[id])) > 0
	return dev, isPassive
}

// deviceRoleSlug reads a device's role, tolerating the field's rename from
// "device_role" to "role" across NetBox releases.
func deviceRoleSlug(o client.Object) string {
	if s := refSlug(o, "role"); s != "" {
		return s
	}
	return refSlug(o, "device_role")
}

// deviceCustomFields returns the non-null custom field values whose field is
// defined for the device content type. A value NetBox holds for a field defined
// on another type, or a null, is omitted — emitting it would draw an
// unknown-custom-field finding from yamlcheck.
func (rc *runContext) deviceCustomFields(o client.Object, idx deviceIndex) map[string]interface{} {
	cf, ok := o["custom_fields"].(map[string]interface{})
	if !ok {
		return nil
	}
	out := map[string]interface{}{}
	for name, val := range cf {
		if val == nil || !idx.deviceCF[name] {
			continue
		}
		out[name] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// primaryIPIDs returns the device's primary IPv4/IPv6 address ids.
func primaryIPIDs(o client.Object) map[int]bool {
	ids := map[int]bool{}
	for _, key := range []string{"primary_ip4", "primary_ip6"} {
		if ref := nested(o, key); ref != nil {
			ids[idOf(client.Object(ref))] = true
		}
	}
	return ids
}

// mapModules maps a device's installed modules.
func mapModules(objs []client.Object) []models.ModuleConfig {
	sort.SliceStable(objs, func(i, j int) bool {
		return refName(objs[i], "module_bay") < refName(objs[j], "module_bay")
	})
	var out []models.ModuleConfig
	for _, o := range objs {
		out = append(out, models.ModuleConfig{
			Name:           refName(o, "module_bay"),
			ModuleTypeSlug: refSlug(o, "module_type"),
			Status:         choiceValue(o, "status"),
			Serial:         str(o, "serial"),
			AssetTag:       str(o, "asset_tag"),
			Description:    str(o, "description"),
		})
	}
	return out
}

// devicePath assigns a device to an output file per --split-by.
func (rc *runContext) devicePath(dev *models.DeviceConfig, o client.Object, passive bool, idx deviceIndex) string {
	root := "inventory/hardware/active"
	if passive {
		root = "inventory/hardware/passive"
	}
	site := dev.SiteSlug
	if site == "" {
		site = "no-site"
	}
	switch rc.opts.SplitBy {
	case "none":
		return path.Join(root, "devices.yaml")
	case "role":
		role := dev.RoleSlug
		if role == "" {
			role = "no-role"
		}
		return path.Join(root, fmt.Sprintf("%s.yaml", role))
	case "rack", "site":
		fallthrough
	default:
		rack := dev.RackSlug
		if rack == "" {
			rack = "unracked"
		}
		return path.Join(root, site, rack+".yaml")
	}
}

// mapInterfaces maps a device's interfaces, applying the template diffing that
// keeps the output lint-clean: an interface is emitted only when it carries
// information the device type template does not already supply, and within it
// `type` and `enabled` are omitted when they match the template.
func (rc *runContext) mapInterfaces(o client.Object, idx deviceIndex, primary map[int]bool) []models.InterfaceConfig {
	devID := idOf(o)
	dtID := idOf(client.Object(nested(o, "device_type")))
	tmpl := idx.ifTmplByDT[dtID]

	ifaces := idx.ifByDevice[devID]
	sortByName(ifaces)

	// Invert NetBox's member->lag relation into the schema's lag->members.
	members := map[int][]string{} // lag interface id -> member names
	for _, iface := range ifaces {
		if lag := nested(iface, "lag"); lag != nil {
			members[idOf(client.Object(lag))] = append(members[idOf(client.Object(lag))], str(iface, "name"))
		}
	}

	var out []models.InterfaceConfig
	for _, iface := range ifaces {
		name := str(iface, "name")
		ifType := choiceValue(iface, "type")
		t := tmpl[name]
		inTemplate := t != nil
		tmplType := ""
		if inTemplate {
			tmplType = choiceValue(t, "type")
		}

		ic := models.InterfaceConfig{Name: name}

		// type: omit when it matches the template's type for this name.
		if !inTemplate || ifType != tmplType {
			ic.Type = ifType
		}
		// enabled: emit only when disabled (template/NetBox default is true).
		if en, ok := iface["enabled"].(bool); ok && !en {
			f := false
			ic.Enabled = &f
		}
		ic.Label = str(iface, "label")
		ic.Description = str(iface, "description")
		ic.MTU = intOf(iface, "mtu")
		ic.Mode = choiceValue(iface, "mode")
		ic.UntaggedVLAN = vlanKey(nested(iface, "untagged_vlan"))
		ic.TaggedVLANs = taggedVLANKeys(iface)
		if ms := members[idOf(iface)]; len(ms) > 0 {
			ic.Members = sortedUnique(ms)
		}

		// IPs: the model carries one; emit the primary, else the first. Extra
		// addresses are recorded in the report, not silently dropped.
		ic.IP, ic.AddressRole = rc.pickIP(iface, idx, primary)

		// Cable: emit the link on the end that sorts first, once.
		ic.Link = rc.cableFor(iface, idx, "dcim.interface")

		if interfaceCarriesInfo(ic, inTemplate) {
			out = append(out, ic)
		}
	}
	return out
}

// interfaceCarriesInfo reports whether an interface is worth emitting: it holds
// data beyond its name, or it is not in the device type template at all (a port
// the device has that the template does not, which the sync must be told to
// create).
func interfaceCarriesInfo(ic models.InterfaceConfig, inTemplate bool) bool {
	if !inTemplate {
		return true
	}
	return ic.Type != "" || ic.Enabled != nil || ic.Label != "" || ic.Description != "" ||
		ic.MTU != 0 || ic.Mode != "" || ic.UntaggedVLAN != "" || len(ic.TaggedVLANs) > 0 ||
		len(ic.Members) > 0 || ic.IP != nil || ic.AddressRole != "" || ic.Link != nil
}

// pickIP chooses the address to place on an interface (the primary if this
// interface holds it, else the lowest-sorted) and returns the role. Additional
// addresses on the same interface are reported.
func (rc *runContext) pickIP(iface client.Object, idx deviceIndex, primary map[int]bool) (*models.IPConfig, string) {
	ips := idx.ipsByIface[idOf(iface)]
	if len(ips) == 0 {
		return nil, ""
	}
	sort.SliceStable(ips, func(i, j int) bool { return str(ips[i], "address") < str(ips[j], "address") })

	chosen := ips[0]
	role := ""
	for _, ip := range ips {
		if primary[idOf(ip)] {
			chosen = ip
			role = "primary"
			break
		}
	}
	// Report every address on this interface that we could not place.
	for _, ip := range ips {
		if idOf(ip) == idOf(chosen) {
			continue
		}
		rc.report.skip("ipam/ip-addresses", str(ip, "address"),
			fmt.Sprintf("a second address on interface %s; the schema holds one IP per interface", str(iface, "name")))
	}

	cfg := &models.IPConfig{
		Address:     str(chosen, "address"),
		DNSName:     str(chosen, "dns_name"),
		Description: str(chosen, "description"),
		Status:      choiceValue(chosen, "status"),
		VRF:         rc.vrfOut(refName(chosen, "vrf")),
		Tags:        rc.tags(chosen),
	}
	return cfg, role
}

// mapPassivePorts maps the front and rear ports a device carries, emitting a
// port only when it carries a cable — the port itself comes from the device
// type template, so an uncabled port would be redundant to declare.
func (rc *runContext) mapPassivePorts(devID int, idx deviceIndex) ([]models.FrontPortConfig, []models.RearPortConfig) {
	var fronts []models.FrontPortConfig
	frontObjs := idx.frontByDev[devID]
	sortByName(frontObjs)
	for _, p := range frontObjs {
		link := rc.cableFor(p, idx, "dcim.frontport")
		if link == nil {
			continue
		}
		fronts = append(fronts, models.FrontPortConfig{
			Name:     str(p, "name"),
			Type:     choiceValue(p, "type"),
			RearPort: refName(p, "rear_port"),
			Link:     link,
		})
	}
	var rears []models.RearPortConfig
	rearObjs := idx.rearByDev[devID]
	sortByName(rearObjs)
	for _, p := range rearObjs {
		link := rc.cableFor(p, idx, "dcim.rearport")
		if link == nil {
			continue
		}
		rears = append(rears, models.RearPortConfig{
			Name: str(p, "name"),
			Type: choiceValue(p, "type"),
			Link: link,
		})
	}
	return fronts, rears
}

// cableFor returns the LinkConfig to emit on a termination, or nil. A cable is
// declared once: on the end whose (device, port) sorts first. The other end,
// and any cable that is not a simple 1:1 pair this schema can express, returns
// nil — the latter reported.
func (rc *runContext) cableFor(term client.Object, idx deviceIndex, thisType string) *models.LinkConfig {
	thisID := idOf(term)
	for _, cable := range idx.cables {
		a := terminations(cable, "a_terminations")
		b := terminations(cable, "b_terminations")
		// Only simple 1:1 cables are expressible.
		if len(a) != 1 || len(b) != 1 {
			continue
		}
		near, far, ok := orient(a[0], b[0], thisType, thisID)
		if !ok {
			continue
		}
		nearDev, nearPort := rc.endLabel(near, idx)
		farDev, farPort := rc.endLabel(far, idx)
		if nearDev == "" || farDev == "" {
			continue
		}
		// Emit on the lexicographically-first end only, so the cable is
		// declared exactly once across the two devices.
		if nearDev+"/"+nearPort > farDev+"/"+farPort {
			return nil
		}
		link := &models.LinkConfig{
			PeerDevice: rc.nameOut(farDev),
			PeerPort:   farPort,
			CableType:  choiceValue(cable, "type"),
			Color:      str(cable, "color"),
		}
		if l := floatOf(cable, "length"); l > 0 {
			link.Length = l
			link.LengthUnit = choiceValue(cable, "length_unit")
		}
		return link
	}
	return nil
}

// terminations returns a cable side's termination objects.
func terminations(cable client.Object, side string) []map[string]interface{} {
	raw, ok := cable[side].([]interface{})
	if !ok {
		return nil
	}
	var out []map[string]interface{}
	for _, t := range raw {
		if tm, ok := t.(map[string]interface{}); ok {
			out = append(out, tm)
		}
	}
	return out
}

// orient returns (near, far) so that near is the termination matching the given
// type+id, or ok=false when neither side is this termination.
func orient(a, b map[string]interface{}, thisType string, thisID int) (near, far map[string]interface{}, ok bool) {
	if termIs(a, thisType, thisID) {
		return a, b, true
	}
	if termIs(b, thisType, thisID) {
		return b, a, true
	}
	return nil, nil, false
}

func termIs(t map[string]interface{}, objType string, id int) bool {
	ot, _ := t["object_type"].(string)
	return ot == objType && utils.GetIDFromObject(t["object_id"]) == id
}

// endLabel resolves a termination to (device name, port name).
func (rc *runContext) endLabel(t map[string]interface{}, idx deviceIndex) (string, string) {
	ot, _ := t["object_type"].(string)
	id := utils.GetIDFromObject(t["object_id"])
	switch ot {
	case "dcim.interface":
		if iface, ok := idx.ifByID[id]; ok {
			return rc.deviceName(iface, idx), str(iface, "name")
		}
	case "dcim.frontport", "dcim.rearport":
		if port, ok := idx.portByID[id]; ok {
			return rc.deviceName(port, idx), str(port, "name")
		}
	}
	return "", ""
}

// vlanKey returns the identifier the schema uses to reference a VLAN from an
// interface: its name (the models resolve untagged/tagged VLANs by name).
func vlanKey(ref map[string]interface{}) string {
	if ref == nil {
		return ""
	}
	if n, ok := ref["name"].(string); ok {
		return n
	}
	return ""
}

// taggedVLANKeys returns an interface's tagged VLAN names, sorted.
func taggedVLANKeys(iface client.Object) []string {
	raw, ok := iface["tagged_vlans"].([]interface{})
	if !ok {
		return nil
	}
	var names []string
	for _, v := range raw {
		if m, ok := v.(map[string]interface{}); ok {
			if n, ok := m["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return sortedUnique(names)
}

// deviceName resolves the owning device's name for a component object: the
// nested device reference's name when present, else a lookup by id in the
// device index (nested refs do not always carry the name).
func (rc *runContext) deviceName(component client.Object, idx deviceIndex) string {
	if n := refName(component, "device"); n != "" {
		return n
	}
	ref := nested(component, "device")
	if ref == nil {
		return ""
	}
	if dev, ok := idx.devicesByID[idOf(client.Object(ref))]; ok {
		return str(dev, "name")
	}
	return ""
}
