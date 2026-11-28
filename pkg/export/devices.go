// SPDX-License-Identifier: Apache-2.0

package export

import (
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// exportedDev is a device being exported, with the NetBox IDs of its ports so
// cables can be attached to the right YAML entry afterwards.
type exportedDev struct {
	cfg  *models.DeviceConfig
	id   int
	file string
	// ports maps "dcim.interface:12" (and front/rear ports alike) to the
	// position in the cfg list; listed tracks whether it is listed yet.
	ifaceIdx map[int]int
	frontIdx map[int]int
	rearIdx  map[int]int
	// the NetBox objects, for adding a port to the list on demand.
	ifaces map[int]client.Object
	fronts map[int]client.Object
	rears  map[int]client.Object
	rearNm map[int]string
}

// naturalKey sorts names the way people number ports: Eth1/2 before Eth1/10.
func naturalKey(s string) string {
	var b strings.Builder
	digits := ""
	flush := func() {
		if digits != "" {
			b.WriteString(strings.Repeat("0", max(0, 10-len(digits))))
			b.WriteString(digits)
			digits = ""
		}
	}
	for _, r := range s {
		if unicode.IsDigit(r) {
			digits += string(r)
			continue
		}
		flush()
		b.WriteRune(r)
	}
	flush()
	return b.String()
}

func (e *exporter) exportDevices() map[int]*exportedDev {
	ifacesBy := byParent(e.data["dcim/interfaces"], "device")
	frontsBy := byParent(e.data["dcim/front-ports"], "device")
	rearsBy := byParent(e.data["dcim/rear-ports"], "device")
	modulesBy := byParent(e.data["dcim/modules"], "device")

	// The names a device's type gives its ports: a port by one of these
	// names comes from the template and need not be listed unless it
	// carries something of its own.
	tmplNames := func(ep string) map[int]map[string]choiceTmpl {
		out := map[int]map[string]choiceTmpl{}
		for _, t := range e.data[ep] {
			dt := nestedID(t, "device_type")
			if dt == 0 {
				continue
			}
			if out[dt] == nil {
				out[dt] = map[string]choiceTmpl{}
			}
			out[dt][str(t, "name")] = choiceTmpl{typ: choice(t, "type"), label: str(t, "label"), description: str(t, "description")}
		}
		return out
	}
	ifaceTmpl := tmplNames("dcim/interface-templates")
	frontTmpl := tmplNames("dcim/front-port-templates")
	rearTmpl := tmplNames("dcim/rear-port-templates")

	// A LAG lists its members; NetBox records the reverse (member -> lag).
	members := map[int][]string{}
	for _, i := range e.data["dcim/interfaces"] {
		if lag := nestedID(i, "lag"); lag != 0 {
			members[lag] = append(members[lag], str(i, "name"))
		}
	}

	out := map[int]*exportedDev{}
	unnamed := 0
	for _, d := range e.data["dcim/devices"] {
		name := str(d, "name")
		site := nestedStr(d, "site", "slug")
		if !e.inScope(site) {
			continue
		}
		if name == "" {
			unnamed++
			continue
		}
		if e.declared.has(kindDevice, site+"/"+name) {
			e.skipDeclared(kindDevice)
			continue
		}
		id := idOf(d)
		dtID := nestedID(d, "device_type")
		role := e.roleOfDevice[id]
		cfg := &models.DeviceConfig{
			Name: name, SiteSlug: site, RoleSlug: role,
			DeviceTypeSlug: nestedStr(d, "device_type", "slug"),
			Status:         status(d),
			Serial:         str(d, "serial"), AssetTag: str(d, "asset_tag"),
			Tenant: nestedStr(d, "tenant", "slug"),
			Tags:   e.tags(d),
		}
		if pd := nested(d, "parent_device"); pd != nil {
			cfg.ParentDevice = str(pd, "name")
			cfg.DeviceBay = nestedStr(pd, "device_bay", "name")
		} else if rack := nested(d, "rack"); rack != nil {
			cfg.RackSlug = e.rackRef(site, str(rack, "name"))
			if p, present, ok := whole(d, "position"); ok {
				cfg.Position = p
			} else if present {
				e.warn("device %s sits at rack position %s, which is not a whole unit; the position is left out of the YAML and kept as it is", name, str(d, "position"))
			}
			if f := choice(d, "face"); f != "" && !(f == "front" && cfg.Position > 0) {
				cfg.Face = f
			}
		}
		cfg.CustomFields = e.customFieldValues("device "+name, d)
		for _, f := range []string{"platform", "cluster", "location", "virtual_chassis", "config_template"} {
			if nested(d, f) != nil {
				e.notCarried(f)
			}
		}
		if str(d, "description") != "" || str(d, "comments") != "" {
			e.notCarried("description")
		}
		if nested(d, "oob_ip") != nil {
			e.notCarried("oob_ip")
		}

		x := &exportedDev{
			cfg: cfg, id: id,
			ifaceIdx: map[int]int{}, frontIdx: map[int]int{}, rearIdx: map[int]int{},
			ifaces: map[int]client.Object{}, fronts: map[int]client.Object{}, rears: map[int]client.Object{},
			rearNm: map[int]string{},
		}
		x.file = path.Join("inventory/hardware", folderFor(role), FileStem+"-"+fileName(site)+".yaml")

		// Interfaces, in a stable order.
		ifaces := ifacesBy[id]
		sortBy(ifaces, func(o client.Object) string { return naturalKey(str(o, "name")) })
		for _, i := range ifaces {
			x.ifaces[idOf(i)] = i
		}
		for _, i := range ifaces {
			t, inTmpl := ifaceTmpl[dtID][str(i, "name")]
			fromModule := nested(i, "module") != nil
			cfgI, interesting := e.interfaceConfig(d, i, members[idOf(i)])
			if !inTmpl && !fromModule {
				interesting = true
			}
			if inTmpl && (cfgI.Type != t.typ || cfgI.Label != t.label || cfgI.Description != t.description) {
				interesting = true
			}
			if interesting {
				x.ifaceIdx[idOf(i)] = len(cfg.Interfaces)
				cfg.Interfaces = append(cfg.Interfaces, cfgI)
			}
		}

		rears := rearsBy[id]
		sortBy(rears, func(o client.Object) string { return naturalKey(str(o, "name")) })
		for _, r := range rears {
			x.rears[idOf(r)] = r
			x.rearNm[idOf(r)] = str(r, "name")
		}
		for _, r := range rears {
			t, inTmpl := rearTmpl[dtID][str(r, "name")]
			rc := rearPortConfig(r)
			if !inTmpl || hasTags(r) || rc.Type != t.typ || rc.Label != t.label || rc.Description != t.description {
				rc.Tags = e.tags(r)
				x.rearIdx[idOf(r)] = len(cfg.RearPorts)
				cfg.RearPorts = append(cfg.RearPorts, rc)
			}
		}
		fronts := frontsBy[id]
		sortBy(fronts, func(o client.Object) string { return naturalKey(str(o, "name")) })
		for _, f := range fronts {
			x.fronts[idOf(f)] = f
		}
		for _, f := range fronts {
			t, inTmpl := frontTmpl[dtID][str(f, "name")]
			fc := frontPortConfig(f, x.rearNm)
			if !inTmpl || hasTags(f) || fc.Type != t.typ || fc.Label != t.label || fc.Description != t.description {
				fc.Tags = e.tags(f)
				x.frontIdx[idOf(f)] = len(cfg.FrontPorts)
				cfg.FrontPorts = append(cfg.FrontPorts, fc)
			}
		}

		for _, m := range modulesBy[id] {
			mc := models.ModuleConfig{
				Name:           nestedStr(m, "module_bay", "name"),
				ModuleTypeSlug: e.moduleTypeRef[nestedID(m, "module_type")],
				Status:         status(m),
				Serial:         str(m, "serial"), AssetTag: str(m, "asset_tag"),
				Description: str(m, "description"), Tags: e.tags(m),
			}
			if nested(nested(m, "module_bay"), "module") != nil || nested(m, "module_bay") == nil {
				e.warn("device %s: the module in bay %s sits inside another module; nested modules are not exported", name, mc.Name)
				continue
			}
			cfg.Modules = append(cfg.Modules, mc)
		}
		sortBy(cfg.Modules, func(m models.ModuleConfig) string { return naturalKey(m.Name) })

		out[id] = x
	}
	if unnamed > 0 {
		e.warn("%s without a name were not exported: a device is matched by its name", plural(unnamed, "device"))
	}
	e.flushNotCarried("device")
	return out
}

type choiceTmpl struct{ typ, label, description string }

// folderFor puts patch panels with the passive inventory, the way the
// hand-written layout does.
func folderFor(role string) string {
	if role == models.PatchPanelRole {
		return "passive"
	}
	return "active"
}

// interfaceConfig converts a device interface. interesting reports whether
// it carries anything beyond what a template gives it.
func (e *exporter) interfaceConfig(dev, i client.Object, members []string) (models.InterfaceConfig, bool) {
	site := nestedStr(dev, "site", "slug")
	c := models.InterfaceConfig{
		Name: str(i, "name"), Type: choice(i, "type"), Label: str(i, "label"),
		Description: str(i, "description"), Mode: choice(i, "mode"),
		Tags: e.tags(i),
	}
	devName := str(dev, "name")
	if en, ok := i["enabled"].(bool); ok && !en {
		c.Enabled = &en
	}
	c.MTU, _, _ = whole(i, "mtu")
	if v := nestedID(i, "untagged_vlan"); v != 0 {
		c.UntaggedVLAN = e.vlanRef(site, v, "device "+devName+" interface "+c.Name)
	}
	if list, ok := i["tagged_vlans"].([]interface{}); ok {
		for _, v := range list {
			if ref := e.vlanRef(site, idOf(asMap(v)), "device "+devName+" interface "+c.Name); ref != "" {
				c.TaggedVLANs = append(c.TaggedVLANs, ref)
			}
		}
	}
	sortBy(members, naturalKey)
	c.Members = members
	if ip := e.pickIP("dcim.interface", idOf(i), dev, "device "+devName+" interface "+c.Name); ip != nil {
		c.IP = ip.cfg
		if ip.primary {
			c.AddressRole = "primary"
		}
	}
	interesting := c.Enabled != nil || c.MTU > 0 || c.Mode != "" || c.UntaggedVLAN != "" ||
		len(c.TaggedVLANs) > 0 || len(c.Members) > 0 || c.IP != nil || len(c.Tags) > 0
	return c, interesting
}

// vlanRef is how an interface names a VLAN: by name, looked up at the
// interface's own site. A VLAN elsewhere, or one whose name its site uses
// twice, cannot be named that way.
func (e *exporter) vlanRef(site string, vlanID int, what string) string {
	v := e.vlanByID[vlanID]
	if v == nil {
		return ""
	}
	name := str(v, "name")
	if vs := nestedStr(v, "site", "slug"); vs != site {
		e.warn("%s uses VLAN %s, which is not a VLAN of site %s; interfaces can only reference VLANs of their own site, so it is left out (and kept as it is)", what, name, site)
		return ""
	}
	for _, other := range e.data["ipam/vlans"] {
		if idOf(other) != vlanID && str(other, "name") == name && nestedStr(other, "site", "slug") == site {
			e.warn("%s uses VLAN %s, a name site %s has more than once; it is left out because it would be ambiguous", what, name, site)
			return ""
		}
	}
	return name
}

type pickedIP struct {
	cfg     *models.IPConfig
	primary bool
}

// pickIP returns the one IP address an interface declares. The YAML holds one
// per interface; when NetBox has several, the primary one wins and the rest
// are reported (they are left in NetBox, unmanaged).
func (e *exporter) pickIP(objType string, ifaceID int, owner client.Object, what string) *pickedIP {
	ips := e.ipsByIface[objType+":"+strconv.Itoa(ifaceID)]
	if len(ips) == 0 {
		return nil
	}
	primary := map[int]bool{
		nestedID(owner, "primary_ip4"): true,
		nestedID(owner, "primary_ip6"): true,
	}
	delete(primary, 0)
	chosen := ips[0]
	for _, ip := range ips {
		if primary[idOf(ip)] {
			chosen = ip
			break
		}
	}
	if len(ips) > 1 {
		var rest []string
		for _, ip := range ips {
			if idOf(ip) != idOf(chosen) {
				rest = append(rest, str(ip, "address"))
			}
		}
		e.warn("%s has %d IP addresses; the YAML holds one per interface, so %s is declared and %s stay in NetBox unmanaged",
			what, len(ips), str(chosen, "address"), strings.Join(rest, ", "))
	}
	if nested(chosen, "vrf") == nil {
		if vrf := e.globalIPInVRFPrefix(str(chosen, "address")); vrf != "" {
			e.warn("%s: address %s is in the global table but inside a prefix of VRF %s; the controller would look it up in that VRF and create a second copy, so it is not declared (it stays in NetBox, unmanaged)",
				what, str(chosen, "address"), vrf)
			return nil
		}
	}
	cfg := &models.IPConfig{
		Address: str(chosen, "address"), DNSName: str(chosen, "dns_name"),
		Description: str(chosen, "description"), Status: choice(chosen, "status"),
		VRF: nestedStr(chosen, "vrf", "name"), Tags: e.tags(chosen),
	}
	if cfg.Status == "active" {
		cfg.Status = ""
	}
	return &pickedIP{cfg: cfg, primary: primary[idOf(chosen)]}
}

func rearPortConfig(r client.Object) models.RearPortConfig {
	rc := models.RearPortConfig{
		Name: str(r, "name"), Type: choice(r, "type"),
		Label: str(r, "label"), Description: str(r, "description"),
	}
	rc.Positions, _, _ = whole(r, "positions")
	return rc
}

func frontPortConfig(f client.Object, rearName map[int]string) models.FrontPortConfig {
	rear, pos := frontPortRear(f, rearName)
	return models.FrontPortConfig{
		Name: str(f, "name"), Type: choice(f, "type"),
		Label: str(f, "label"), Description: str(f, "description"),
		RearPort: rear, RearPortPosition: pos,
	}
}

// customFieldValues returns the custom field values an object holds that the
// YAML can carry: scalars. Empty fields are left out, so a field nobody
// filled in stays out of the diff.
func (e *exporter) customFieldValues(what string, o client.Object) map[string]interface{} {
	cf, _ := o["custom_fields"].(map[string]interface{})
	out := map[string]interface{}{}
	for k, v := range cf {
		switch val := v.(type) {
		case nil:
		case string:
			if val != "" {
				out[k] = val
			}
		case bool:
			out[k] = val
		case float64:
			if val == float64(int64(val)) {
				out[k] = int64(val)
			} else {
				out[k] = val
			}
		default:
			e.warn("%s: custom field %s holds an object or list, which is not exported; it is kept as it is", what, k)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// notCarried counts device fields NetBox holds that the device model has no
// key for. They are not sent by a sync, so they survive adoption untouched;
// the summary only says that they exist.
func (e *exporter) notCarried(field string) {
	if e.uncarried == nil {
		e.uncarried = map[string]int{}
	}
	e.uncarried[field]++
}

func (e *exporter) flushNotCarried(kind string) {
	if len(e.uncarried) == 0 {
		return
	}
	var parts []string
	for _, f := range sortedKeys(e.uncarried) {
		parts = append(parts, f+" ("+strconv.Itoa(e.uncarried[f])+")")
	}
	e.warn("some %ss have fields this repository does not manage: %s. They are not in the YAML and a sync leaves them as they are", kind, strings.Join(parts, ", "))
	e.uncarried = nil
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortBy(keys, func(s string) string { return s })
	return keys
}

// writeDevices renders the exported devices, one file per site and folder.
func (e *exporter) writeDevices(devs map[int]*exportedDev) {
	files := map[string][]*models.DeviceConfig{}
	for _, x := range devs {
		files[x.file] = append(files[x.file], x.cfg)
	}
	for _, p := range sortedFiles(files) {
		list := files[p]
		sortBy(list, func(d *models.DeviceConfig) string { return naturalKey(d.Name) })
		for _, d := range list {
			// Ports listed for a cable were appended; put them in order.
			sortBy(d.Interfaces, func(i models.InterfaceConfig) string { return naturalKey(i.Name) })
			sortBy(d.FrontPorts, func(f models.FrontPortConfig) string { return naturalKey(f.Name) })
			sortBy(d.RearPorts, func(r models.RearPortConfig) string { return naturalKey(r.Name) })
		}
		e.addFile(p, kindDevice, list, len(list), "")
	}
}

func sortedFiles[T any](m map[string][]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortBy(keys, func(s string) string { return s })
	return keys
}
