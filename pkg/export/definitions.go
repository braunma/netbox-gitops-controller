// SPDX-License-Identifier: Apache-2.0

package export

import (
	"path"
	"strconv"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// exportDefinitions exports everything under definitions/.
func (e *exporter) exportDefinitions() {
	e.exportTags()
	e.exportCustomFields()
	e.exportRoles()
	e.exportPlatforms()
	e.exportTenancy()
	e.exportSites()
	e.exportRacks()
	e.exportVRFs()
	e.exportVLANGroups()
	e.exportVLANs()
	e.exportPrefixes()
	e.exportModuleTypes()
	e.exportDeviceTypes()
	e.exportClusterDefinitions()
}

func (e *exporter) exportTags() {
	var out []*models.Tag
	for _, o := range e.data["extras/tags"] {
		slug := str(o, "slug")
		if slug == constants.ManagedTagSlug || slug == constants.StateTagSlug {
			// The controller's own tags: declaring the state tag would have
			// the next sync overwrite the note it keeps there.
			continue
		}
		if e.declared.has(kindTag, slug) {
			e.skipDeclared(kindTag)
			continue
		}
		out = append(out, &models.Tag{
			Name: str(o, "name"), Slug: slug,
			Color: str(o, "color"), Description: str(o, "description"),
		})
	}
	sortBy(out, func(t *models.Tag) string { return t.Slug })
	e.addFile(defsPath("extras"), kindTag, out, len(out), "")
}

func (e *exporter) exportCustomFields() {
	var out []*models.CustomField
	for _, o := range e.data["extras/custom-fields"] {
		name := str(o, "name")
		if e.declared.has(kindCustomField, name) {
			e.skipDeclared(kindCustomField)
			continue
		}
		// NetBox 4.0 renamed content_types to object_types.
		types := stringList(o["object_types"])
		if len(types) == 0 {
			types = stringList(o["content_types"])
		}
		cf := &models.CustomField{
			Name: name, Type: choice(o, "type"), ObjectTypes: types,
			Label: str(o, "label"), Description: str(o, "description"),
			Required: boolean(o, "required"),
		}
		if cf.Type == "select" || cf.Type == "multiselect" {
			e.warn("custom field %s is a %s field; its choice set is not managed here and is left as it is", name, cf.Type)
		}
		out = append(out, cf)
	}
	sortBy(out, func(c *models.CustomField) string { return c.Name })
	e.addFile(defsPath("custom_fields"), kindCustomField, out, len(out), "")
}

func stringList(v interface{}) []string {
	list, _ := v.([]interface{})
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (e *exporter) exportRoles() {
	var out []*models.Role
	for _, o := range e.data["dcim/device-roles"] {
		slug := str(o, "slug")
		if e.declared.has(kindRole, slug) {
			e.skipDeclared(kindRole)
			continue
		}
		e.untaggable("device role", slug, o)
		out = append(out, &models.Role{
			Name: str(o, "name"), Slug: slug, Color: str(o, "color"),
			VMRole: boolean(o, "vm_role"), Description: str(o, "description"),
		})
	}
	sortBy(out, func(r *models.Role) string { return r.Slug })
	e.addFile(defsPath("roles"), kindRole, out, len(out), "")
}

func (e *exporter) exportPlatforms() {
	var out []*models.Platform
	for _, o := range e.data["dcim/platforms"] {
		slug := str(o, "slug")
		if e.declared.has(kindPlatform, slug) {
			e.skipDeclared(kindPlatform)
			continue
		}
		out = append(out, &models.Platform{
			Name: str(o, "name"), Slug: slug,
			Manufacturer: nestedStr(o, "manufacturer", "name"),
			Description:  str(o, "description"), Tags: e.tags(o),
		})
	}
	sortBy(out, func(p *models.Platform) string { return p.Slug })
	e.addFile(defsPath("platforms"), kindPlatform, out, len(out), "")
}

func (e *exporter) exportTenancy() {
	var groups []*models.TenantGroup
	for _, o := range e.data["tenancy/tenant-groups"] {
		slug := str(o, "slug")
		if e.declared.has(kindTenantGroup, slug) {
			e.skipDeclared(kindTenantGroup)
			continue
		}
		groups = append(groups, &models.TenantGroup{
			Name: str(o, "name"), Slug: slug,
			ParentSlug:  nestedStr(o, "parent", "slug"),
			Description: str(o, "description"), Tags: e.tags(o),
		})
	}
	sortBy(groups, func(g *models.TenantGroup) string { return g.Slug })
	e.addFile(defsPath("tenant_groups"), kindTenantGroup, groups, len(groups), "")

	var tenants []*models.Tenant
	for _, o := range e.data["tenancy/tenants"] {
		slug := str(o, "slug")
		if e.declared.has(kindTenant, slug) {
			e.skipDeclared(kindTenant)
			continue
		}
		tenants = append(tenants, &models.Tenant{
			Name: str(o, "name"), Slug: slug,
			GroupSlug:   nestedStr(o, "group", "slug"),
			Description: str(o, "description"), Tags: e.tags(o),
		})
	}
	sortBy(tenants, func(t *models.Tenant) string { return t.Slug })
	e.addFile(defsPath("tenants"), kindTenant, tenants, len(tenants), "")
}

func (e *exporter) exportSites() {
	var out []*models.Site
	for _, o := range e.data["dcim/sites"] {
		slug := str(o, "slug")
		if !e.inScope(slug) {
			continue
		}
		if e.declared.has(kindSite, slug) {
			e.skipDeclared(kindSite)
			continue
		}
		// The region is deliberately not exported: the site reconciler sends
		// it verbatim, and NetBox wants a region ID there. Leaving it out
		// leaves the site's region as it is.
		out = append(out, &models.Site{
			Name: str(o, "name"), Slug: slug, Status: status(o),
			TimeZone: str(o, "time_zone"), Tenant: nestedStr(o, "tenant", "slug"),
			Description: str(o, "description"),
			Comments:    str(o, "comments"), Tags: e.tags(o),
		})
	}
	sortBy(out, func(s *models.Site) string { return s.Slug })
	e.addFile(defsPath("sites"), kindSite, out, len(out), "")
}

// rackRef is what a device's `rack` says for a rack: the slug the repository
// declared it with, or the slug the export gives it.
func (e *exporter) rackRef(site, name string) string {
	if slug, ok := e.declared.rackSlug[site+"/"+name]; ok && slug != "" {
		return slug
	}
	return utils.Slugify(name)
}

func (e *exporter) exportRacks() {
	var out []*models.Rack
	for _, o := range e.data["dcim/racks"] {
		site := nestedStr(o, "site", "slug")
		name := str(o, "name")
		if !e.inScope(site) {
			continue
		}
		if e.declared.has(kindRack, site+"/"+name) {
			e.skipDeclared(kindRack)
			continue
		}
		r := &models.Rack{
			Name: name, Slug: e.rackRef(site, name), SiteSlug: site,
			Status: status(o), Tenant: nestedStr(o, "tenant", "slug"),
			Description: str(o, "description"),
			Tags:        e.tags(o),
		}
		if w, _, ok := whole(o, "width"); ok {
			r.Width = w
		}
		if h, _, ok := whole(o, "u_height"); ok {
			r.UHeight = h
		}
		out = append(out, r)
	}
	sortBy(out, func(r *models.Rack) string { return r.SiteSlug + "/" + r.Name })
	e.addFile(defsPath("racks"), kindRack, out, len(out), "")
}

func (e *exporter) exportVRFs() {
	var out []*models.VRF
	for _, o := range e.data["ipam/vrfs"] {
		name := str(o, "name")
		if e.declared.has(kindVRF, name) {
			e.skipDeclared(kindVRF)
			continue
		}
		out = append(out, &models.VRF{
			Name: name, RD: str(o, "rd"), Description: str(o, "description"),
			EnforceUnique: boolean(o, "enforce_unique"),
			Tenant:        nestedStr(o, "tenant", "slug"), Tags: e.tags(o),
		})
	}
	sortBy(out, func(v *models.VRF) string { return v.Name })
	e.addFile(defsPath("vrfs"), kindVRF, out, len(out), "")
}

func (e *exporter) exportVLANGroups() {
	var out []*models.VLANGroup
	for _, o := range e.data["ipam/vlan-groups"] {
		slug := str(o, "slug")
		site, ok := siteFromScope(o, e.siteSlugByID)
		if site != "" && !e.inScope(site) {
			continue
		}
		if e.declared.has(kindVLANGroup, slug) {
			e.skipDeclared(kindVLANGroup)
			continue
		}
		if !ok {
			e.warn("VLAN group %s is scoped to a %s, which the YAML cannot express; it is exported without a scope, and its scope is left as it is", slug, str(o, "scope_type"))
		}
		g := &models.VLANGroup{
			Name: str(o, "name"), Slug: slug, SiteSlug: site,
			Description: str(o, "description"), Tags: e.tags(o),
		}
		// NetBox 4.2 holds a list of VID ranges; the YAML holds one. A group
		// with several ranges keeps them: leaving min/max out sends nothing.
		if ranges, ok := o["vid_ranges"].([]interface{}); ok {
			if len(ranges) == 1 {
				if pair, ok := ranges[0].([]interface{}); ok && len(pair) == 2 {
					lo, _ := pair[0].(float64)
					hi, _ := pair[1].(float64)
					g.MinVID, g.MaxVID = int(lo), int(hi)
				}
			} else if len(ranges) > 1 {
				e.warn("VLAN group %s has %d VID ranges; the YAML holds one, so they are left out and kept as they are", slug, len(ranges))
			}
		}
		out = append(out, g)
	}
	sortBy(out, func(g *models.VLANGroup) string { return g.Slug })
	e.addFile(defsPath("vlan_groups"), kindVLANGroup, out, len(out), "")
}

func (e *exporter) exportVLANs() {
	var out []*models.VLAN
	noSite := 0
	for _, o := range e.data["ipam/vlans"] {
		site := nestedStr(o, "site", "slug")
		vid, _, _ := whole(o, "vid")
		if site == "" {
			noSite++
			continue
		}
		if !e.inScope(site) {
			continue
		}
		if e.declared.has(kindVLAN, site+"/"+strconv.Itoa(vid)) {
			e.skipDeclared(kindVLAN)
			continue
		}
		if nested(o, "group") == nil {
			e.vlanGroupDerived(str(o, "name"), vid, site)
		}
		// A VLAN's role is not exported, for the same reason as a site's
		// region: it is sent verbatim where NetBox wants an ID.
		out = append(out, &models.VLAN{
			Name: str(o, "name"), VID: vid, SiteSlug: site,
			GroupSlug: nestedStr(o, "group", "slug"), Status: status(o),
			Tenant:      nestedStr(o, "tenant", "slug"),
			Description: str(o, "description"), Tags: e.tags(o),
		})
	}
	if noSite > 0 {
		e.warn("%s without a site were not exported: this repository declares VLANs per site", plural(noSite, "VLAN"))
	}
	sortBy(out, func(v *models.VLAN) string { return v.SiteSlug + "/" + strconv.Itoa(10000+v.VID) })
	e.addFile(defsPath("vlans"), kindVLAN, out, len(out), "")
}

func (e *exporter) exportPrefixes() {
	var out []*models.Prefix
	seen := map[string]bool{}
	for _, o := range e.data["ipam/prefixes"] {
		p := str(o, "prefix")
		site, ok := siteFromScope(o, e.siteSlugByID)
		if site != "" && !e.inScope(site) {
			continue
		}
		if e.sites != nil && site == "" {
			continue
		}
		if e.declared.has(kindPrefix, p) {
			e.skipDeclared(kindPrefix)
			continue
		}
		// The reconciler finds a prefix by its CIDR alone, so two prefixes
		// with one CIDR (in different VRFs) would both resolve to whichever
		// NetBox lists first.
		if seen[p] {
			e.warn("prefix %s exists more than once (in different VRFs); only the first was exported, because a prefix is matched by its CIDR alone", p)
			continue
		}
		seen[p] = true
		if !ok {
			e.warn("prefix %s is scoped to a %s, which the YAML cannot express; its scope is left as it is", p, str(o, "scope_type"))
		}
		pr := &models.Prefix{
			Prefix: p, SiteSlug: site, VRFName: nestedStr(o, "vrf", "name"),
			Status: status(o), IsPool: boolean(o, "is_pool"),
			Tenant:      nestedStr(o, "tenant", "slug"),
			Description: str(o, "description"), Tags: e.tags(o),
		}
		if v := e.vlanByID[nestedID(o, "vlan")]; v != nil {
			pr.VLANName = str(v, "name")
			if site == "" {
				e.prefixSiteDerived(p, pr.VLANName)
			}
		}
		out = append(out, pr)
	}
	sortBy(out, func(p *models.Prefix) string { return p.VRFName + "/" + p.Prefix })
	e.addFile(defsPath("prefixes"), kindPrefix, out, len(out), "")
}

// indexModuleTypeRefs decides how a module refers to each module type: by
// the slug the repository declared it with, by the export's slug, or — when
// two manufacturers ship the same model name — by the manufacturer-qualified
// form the reconciler registers for exactly that case.
func (e *exporter) indexModuleTypeRefs() {
	e.moduleTypeRef = map[int]string{}
	owners := map[string]map[string]bool{}
	for _, o := range e.data["dcim/module-types"] {
		slug := utils.Slugify(str(o, "model"))
		if owners[slug] == nil {
			owners[slug] = map[string]bool{}
		}
		owners[slug][nestedStr(o, "manufacturer", "name")] = true
	}
	for _, o := range e.data["dcim/module-types"] {
		mfg := nestedStr(o, "manufacturer", "name")
		model := str(o, "model")
		slug := utils.Slugify(model)
		if s, ok := e.declared.moduleTypeSlug[utils.Slugify(mfg)+"/"+model]; ok && s != "" {
			slug = s
		}
		ref := slug
		if len(owners[slug]) > 1 {
			ref = utils.Slugify(mfg) + "/" + slug
		}
		e.moduleTypeRef[idOf(o)] = ref
	}
}

func (e *exporter) exportModuleTypes() {
	tmpl := e.templatesBy("module_type")
	for _, o := range e.data["dcim/module-types"] {
		mfg := nestedStr(o, "manufacturer", "name")
		model := str(o, "model")
		if e.declared.has(kindModuleType, utils.Slugify(mfg)+"/"+model) {
			e.skipDeclared(kindModuleType)
			continue
		}
		slug := utils.Slugify(model)
		mt := &models.ModuleType{
			Model: model, Slug: slug, Manufacturer: mfg,
			PartNumber: str(o, "part_number"), Airflow: choice(o, "airflow"),
			Description: str(o, "description"), Comments: str(o, "comments"),
			Tags: e.tags(o),
		}
		mt.Weight, mt.WeightUnit = weight(o)
		c := e.componentTemplates(tmpl, idOf(o))
		mt.Interfaces, mt.FrontPorts, mt.RearPorts = c.Interfaces, c.FrontPorts, c.RearPorts
		mt.ConsolePorts, mt.ConsoleServerPorts = c.ConsolePorts, c.ConsoleServerPorts
		mt.PowerPorts, mt.PowerOutlets, mt.ModuleBays = c.PowerPorts, c.PowerOutlets, c.ModuleBays
		e.addFile(path.Join("definitions/module_types", FileStem, fileName(mfg)+"-"+fileName(slug)+".yaml"),
			kindModuleType, mt, 1, "")
	}
}

func (e *exporter) exportDeviceTypes() {
	tmpl := e.templatesBy("device_type")
	for _, o := range e.data["dcim/device-types"] {
		slug := str(o, "slug")
		if e.declared.has(kindDeviceType, slug) {
			e.skipDeclared(kindDeviceType)
			continue
		}
		dt := &models.DeviceType{
			Model: str(o, "model"), Slug: slug,
			Manufacturer: nestedStr(o, "manufacturer", "name"),
			PartNumber:   str(o, "part_number"), IsFullDepth: boolean(o, "is_full_depth"),
			SubdeviceRole: choice(o, "subdevice_role"), Airflow: choice(o, "airflow"),
			Description: str(o, "description"), Comments: str(o, "comments"),
			Tags: e.tags(o),
		}
		if h, ok := num(o, "u_height"); ok {
			dt.UHeight = h
		}
		dt.Weight, dt.WeightUnit = weight(o)
		c := e.componentTemplates(tmpl, idOf(o))
		dt.Interfaces, dt.FrontPorts, dt.RearPorts = c.Interfaces, c.FrontPorts, c.RearPorts
		dt.ConsolePorts, dt.ConsoleServerPorts = c.ConsolePorts, c.ConsoleServerPorts
		dt.PowerPorts, dt.PowerOutlets, dt.ModuleBays = c.PowerPorts, c.PowerOutlets, c.ModuleBays
		dt.DeviceBays = c.DeviceBays
		e.addFile(path.Join("definitions/device_types", FileStem, fileName(slug)+".yaml"),
			kindDeviceType, dt, 1, "")
	}
}

func weight(o map[string]interface{}) (float64, string) {
	w, ok := num(o, "weight")
	if !ok || w == 0 {
		return 0, ""
	}
	return w, choice(o, "weight_unit")
}

// templatesBy groups every component template endpoint by its owner (the
// device_type or module_type key).
func (e *exporter) templatesBy(owner string) map[string]map[int][]client.Object {
	out := map[string]map[int][]client.Object{}
	for _, ep := range []string{
		"dcim/interface-templates", "dcim/front-port-templates", "dcim/rear-port-templates",
		"dcim/console-port-templates", "dcim/console-server-port-templates",
		"dcim/power-port-templates", "dcim/power-outlet-templates",
		"dcim/module-bay-templates", "dcim/device-bay-templates",
	} {
		out[ep] = byParent(e.data[ep], owner)
	}
	return out
}

type templates struct {
	Interfaces         []models.InterfaceTemplate
	FrontPorts         []models.PortTemplate
	RearPorts          []models.PortTemplate
	ConsolePorts       []models.ConsolePortTemplate
	ConsoleServerPorts []models.ConsolePortTemplate
	PowerPorts         []models.PowerPortTemplate
	PowerOutlets       []models.PowerOutletTemplate
	ModuleBays         []models.ModuleBayTemplate
	DeviceBays         []models.DeviceBayTemplate
}

func (e *exporter) componentTemplates(t map[string]map[int][]client.Object, owner int) templates {
	var c templates
	for _, o := range t["dcim/interface-templates"][owner] {
		it := models.InterfaceTemplate{
			Name: str(o, "name"), Type: choice(o, "type"), Label: str(o, "label"),
			Description: str(o, "description"), MgmtOnly: boolean(o, "mgmt_only"),
			PoEMode: choice(o, "poe_mode"), PoEType: choice(o, "poe_type"), RFRole: choice(o, "rf_role"),
		}
		if en, ok := o["enabled"].(bool); ok && !en {
			it.Enabled = &en
		}
		c.Interfaces = append(c.Interfaces, it)
	}
	rearName := map[int]string{}
	for _, o := range t["dcim/rear-port-templates"][owner] {
		rearName[idOf(o)] = str(o, "name")
		pt := models.PortTemplate{
			Name: str(o, "name"), Type: choice(o, "type"), Label: str(o, "label"),
			Description: str(o, "description"), Color: str(o, "color"),
		}
		if p, _, ok := whole(o, "positions"); ok {
			pt.Positions = p
		}
		c.RearPorts = append(c.RearPorts, pt)
	}
	for _, o := range t["dcim/front-port-templates"][owner] {
		rear, pos := frontPortRear(o, rearName)
		c.FrontPorts = append(c.FrontPorts, models.PortTemplate{
			Name: str(o, "name"), Type: choice(o, "type"), Label: str(o, "label"),
			Description: str(o, "description"), Color: str(o, "color"),
			RearPort: rear, RearPortPosition: pos,
		})
	}
	console := func(ep string) []models.ConsolePortTemplate {
		var out []models.ConsolePortTemplate
		for _, o := range t[ep][owner] {
			out = append(out, models.ConsolePortTemplate{
				Name: str(o, "name"), Type: choice(o, "type"),
				Label: str(o, "label"), Description: str(o, "description"),
			})
		}
		return out
	}
	c.ConsolePorts = console("dcim/console-port-templates")
	c.ConsoleServerPorts = console("dcim/console-server-port-templates")
	for _, o := range t["dcim/power-port-templates"][owner] {
		pp := models.PowerPortTemplate{
			Name: str(o, "name"), Type: choice(o, "type"),
			Label: str(o, "label"), Description: str(o, "description"),
		}
		pp.MaximumDraw, _, _ = whole(o, "maximum_draw")
		pp.AllocatedDraw, _, _ = whole(o, "allocated_draw")
		c.PowerPorts = append(c.PowerPorts, pp)
	}
	for _, o := range t["dcim/power-outlet-templates"][owner] {
		c.PowerOutlets = append(c.PowerOutlets, models.PowerOutletTemplate{
			Name: str(o, "name"), Type: choice(o, "type"),
			Label: str(o, "label"), Description: str(o, "description"),
			PowerPort: nestedStr(o, "power_port", "name"), FeedLeg: choice(o, "feed_leg"),
		})
	}
	for _, o := range t["dcim/module-bay-templates"][owner] {
		c.ModuleBays = append(c.ModuleBays, models.ModuleBayTemplate{
			Name: str(o, "name"), Label: str(o, "label"),
			Description: str(o, "description"), Position: str(o, "position"),
		})
	}
	for _, o := range t["dcim/device-bay-templates"][owner] {
		c.DeviceBays = append(c.DeviceBays, models.DeviceBayTemplate{
			Name: str(o, "name"), Label: str(o, "label"), Description: str(o, "description"),
		})
	}
	sortBy(c.Interfaces, func(t models.InterfaceTemplate) string { return naturalKey(t.Name) })
	sortBy(c.FrontPorts, func(t models.PortTemplate) string { return naturalKey(t.Name) })
	sortBy(c.RearPorts, func(t models.PortTemplate) string { return naturalKey(t.Name) })
	return c
}

// frontPortRear reads which rear port (and position) a front port or front
// port template maps to: `rear_port`/`rear_port_position` before NetBox 4.5,
// a `rear_ports` list of mappings since.
func frontPortRear(o map[string]interface{}, rearName map[int]string) (string, int) {
	pos := 0
	if list, ok := o["rear_ports"].([]interface{}); ok && len(list) > 0 {
		m := asMap(list[0])
		if m == nil {
			return "", 0
		}
		name := nestedStr(m, "rear_port", "name")
		if name == "" {
			name = rearName[utils.GetIDFromObject(m["rear_port"])]
		}
		pos, _, _ = whole(m, "rear_port_position")
		return name, keepNonDefault(pos)
	}
	name := nestedStr(o, "rear_port", "name")
	if name == "" {
		name = rearName[nestedID(o, "rear_port")]
	}
	pos, _, _ = whole(o, "rear_port_position")
	return name, keepNonDefault(pos)
}

// keepNonDefault drops a rear port position of 1, which is what an omitted
// one means.
func keepNonDefault(p int) int {
	if p == 1 {
		return 0
	}
	return p
}

func (e *exporter) exportClusterDefinitions() {
	var types []*models.ClusterType
	for _, o := range e.data["virtualization/cluster-types"] {
		slug := str(o, "slug")
		if e.declared.has(kindClusterType, slug) {
			e.skipDeclared(kindClusterType)
			continue
		}
		types = append(types, &models.ClusterType{
			Name: str(o, "name"), Slug: slug, Description: str(o, "description"), Tags: e.tags(o),
		})
	}
	sortBy(types, func(t *models.ClusterType) string { return t.Slug })
	e.addFile(defsPath("virtualization/cluster_types"), kindClusterType, types, len(types), "")

	var groups []*models.ClusterGroup
	for _, o := range e.data["virtualization/cluster-groups"] {
		slug := str(o, "slug")
		if e.declared.has(kindClusterGroup, slug) {
			e.skipDeclared(kindClusterGroup)
			continue
		}
		groups = append(groups, &models.ClusterGroup{
			Name: str(o, "name"), Slug: slug, Description: str(o, "description"), Tags: e.tags(o),
		})
	}
	sortBy(groups, func(g *models.ClusterGroup) string { return g.Slug })
	e.addFile(defsPath("virtualization/cluster_groups"), kindClusterGroup, groups, len(groups), "")

	var clusters []*models.Cluster
	for _, o := range e.data["virtualization/clusters"] {
		name := str(o, "name")
		site, ok := siteFromScope(o, e.siteSlugByID)
		if e.sites != nil && (site == "" || !e.inScope(site)) {
			continue
		}
		if e.declared.has(kindCluster, name) {
			e.skipDeclared(kindCluster)
			continue
		}
		if !ok {
			e.warn("cluster %s is scoped to a %s, which the YAML cannot express; its scope is left as it is", name, str(o, "scope_type"))
		}
		clusters = append(clusters, &models.Cluster{
			Name: name, TypeSlug: nestedStr(o, "type", "slug"),
			GroupSlug: nestedStr(o, "group", "slug"), SiteSlug: site,
			Tenant: nestedStr(o, "tenant", "slug"), Status: status(o),
			Description: str(o, "description"), Tags: e.tags(o),
		})
	}
	sortBy(clusters, func(c *models.Cluster) string { return c.Name })
	e.addFile(defsPath("virtualization/clusters"), kindCluster, clusters, len(clusters), "")
}
