package models

import (
	"errors"
	"fmt"
)

// Validator is implemented by all YAML models so the loader can reject
// invalid definitions before any API call is made. This turns NetBox 400
// errors that would otherwise appear mid-sync into pre-flight errors that
// are also caught by --dry-run and yamlcheck.
type Validator interface {
	Validate() error
}

// requireFields collects an error for every empty required field.
func requireFields(fields map[string]string) []error {
	var errs []error
	for field, value := range fields {
		if value == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	return errs
}

// Validate checks required fields of a Site.
func (s *Site) Validate() error {
	errs := requireFields(map[string]string{
		"name": s.Name,
		"slug": s.Slug,
	})
	errs = appendNonNil(errs,
		validateChoice("status", s.Status, SiteStatuses),
		validateLength("name", s.Name, MaxNameLength),
		validateLength("slug", s.Slug, MaxNameLength),
	)
	return wrap("site", s.Name, errs)
}

// Validate checks required fields of a Rack.
func (r *Rack) Validate() error {
	errs := requireFields(map[string]string{
		"name":      r.Name,
		"site_slug": r.SiteSlug,
	})
	errs = appendNonNil(errs,
		validateChoice("status", r.Status, RackStatuses),
		validateLength("name", r.Name, MaxNameLength),
	)
	return wrap("rack", r.Name, errs)
}

// Validate checks required fields of a Role.
func (r *Role) Validate() error {
	errs := requireFields(map[string]string{
		"name": r.Name,
		"slug": r.Slug,
	})
	return wrap("role", r.Name, errs)
}

// Validate checks required fields of a Tag.
func (t *Tag) Validate() error {
	errs := requireFields(map[string]string{
		"name": t.Name,
		"slug": t.Slug,
	})
	return wrap("tag", t.Name, errs)
}

// Validate checks required fields of a Manufacturer.
func (m *Manufacturer) Validate() error {
	errs := requireFields(map[string]string{
		"name": m.Name,
		"slug": m.Slug,
	})
	return wrap("manufacturer", m.Name, errs)
}

// Validate checks required fields and VID range of a VLAN.
func (v *VLAN) Validate() error {
	errs := requireFields(map[string]string{
		"name":      v.Name,
		"site_slug": v.SiteSlug,
	})
	if v.VID < 1 || v.VID > 4094 {
		errs = append(errs, fmt.Errorf("vid must be between 1 and 4094, got %d", v.VID))
	}
	errs = appendNonNil(errs,
		validateChoice("status", v.Status, VLANStatuses),
		validateLength("name", v.Name, MaxVLANNameLength),
	)
	return wrap("vlan", v.Name, errs)
}

// Validate checks required fields and VID bounds of a VLANGroup.
func (g *VLANGroup) Validate() error {
	errs := requireFields(map[string]string{
		"name": g.Name,
		"slug": g.Slug,
	})
	if g.MinVID > 0 && g.MaxVID > 0 && g.MinVID > g.MaxVID {
		errs = append(errs, fmt.Errorf("min_vid (%d) must not exceed max_vid (%d)", g.MinVID, g.MaxVID))
	}
	return wrap("vlan group", g.Name, errs)
}

// Validate checks required fields of a VRF.
func (v *VRF) Validate() error {
	errs := requireFields(map[string]string{"name": v.Name})
	return wrap("vrf", v.Name, errs)
}

// Validate checks required fields of a Prefix.
func (p *Prefix) Validate() error {
	errs := requireFields(map[string]string{"prefix": p.Prefix})
	errs = appendNonNil(errs, validateChoice("status", p.Status, PrefixStatuses))
	return wrap("prefix", p.Prefix, errs)
}

// Validate checks required fields of a ModuleType. Slug is optional:
// existing data identifies module types by model name.
func (m *ModuleType) Validate() error {
	errs := requireFields(map[string]string{
		"model":        m.Model,
		"manufacturer": m.Manufacturer,
	})
	return wrap("module type", m.Model, errs)
}

// Validate checks required fields of a DeviceType and all its templates.
// Interface templates without a type are the most common cause of
// "400 Bad Request: type may not be blank" errors (see README).
func (d *DeviceType) Validate() error {
	errs := requireFields(map[string]string{
		"model":        d.Model,
		"slug":         d.Slug,
		"manufacturer": d.Manufacturer,
	})
	errs = appendNonNil(errs,
		validateChoice("subdevice_role", d.SubdeviceRole, SubdeviceRoles),
		validateChoice("airflow", d.Airflow, DeviceTypeAirflows),
		validateChoice("weight_unit", d.WeightUnit, WeightUnits),
		validateLength("model", d.Model, MaxNameLength),
		validateLength("slug", d.Slug, MaxNameLength),
		validateLength("part_number", d.PartNumber, MaxPartNumberLength),
	)

	for i, iface := range d.Interfaces {
		if iface.Name == "" {
			errs = append(errs, fmt.Errorf("interface %d: name is required", i+1))
		}
		if iface.Type == "" {
			errs = append(errs, fmt.Errorf("interface %q: type is required (e.g. 1000base-t, virtual, lag)", iface.Name))
		}
	}
	for i, fp := range d.FrontPorts {
		if fp.Name == "" {
			errs = append(errs, fmt.Errorf("front port %d: name is required", i+1))
		}
		if fp.Type == "" {
			errs = append(errs, fmt.Errorf("front port %q: type is required", fp.Name))
		}
		if fp.RearPort == "" {
			errs = append(errs, fmt.Errorf("front port %q: rear_port is required", fp.Name))
		}
	}
	for i, rp := range d.RearPorts {
		if rp.Name == "" {
			errs = append(errs, fmt.Errorf("rear port %d: name is required", i+1))
		}
		if rp.Type == "" {
			errs = append(errs, fmt.Errorf("rear port %q: type is required", rp.Name))
		}
	}
	for i, cp := range d.ConsolePorts {
		if cp.Name == "" {
			errs = append(errs, fmt.Errorf("console port %d: name is required", i+1))
		}
	}
	for i, cp := range d.ConsoleServerPorts {
		if cp.Name == "" {
			errs = append(errs, fmt.Errorf("console server port %d: name is required", i+1))
		}
	}
	for i, pp := range d.PowerPorts {
		if pp.Name == "" {
			errs = append(errs, fmt.Errorf("power port %d: name is required", i+1))
		}
	}
	for i, po := range d.PowerOutlets {
		if po.Name == "" {
			errs = append(errs, fmt.Errorf("power outlet %d: name is required", i+1))
		}
	}

	// NetBox refuses device bay templates unless the device type is declared a
	// parent, rejecting the bay with a message that names the constraint but
	// not the fix. Catching it here keeps the failure in the YAML, before any
	// part of the device type has been written.
	if len(d.DeviceBays) > 0 && d.SubdeviceRole != "parent" {
		errs = append(errs, fmt.Errorf(
			"subdevice_role must be \"parent\" to declare device_bays (found %q)", d.SubdeviceRole))
	}

	return wrap("device type", d.Model, errs)
}

// Validate checks required fields and cross-field constraints of a
// DeviceConfig, including its nested interface, port, and module configs.
func (d *DeviceConfig) Validate() error {
	errs := requireFields(map[string]string{
		"name":             d.Name,
		"site_slug":        d.SiteSlug,
		"device_type_slug": d.DeviceTypeSlug,
		"role_slug":        d.RoleSlug,
	})

	if d.ParentDevice != "" && d.DeviceBay == "" {
		errs = append(errs, errors.New("device_bay is required when parent_device is set"))
	}
	if d.DeviceBay != "" && d.ParentDevice == "" {
		errs = append(errs, errors.New("parent_device is required when device_bay is set"))
	}
	errs = appendNonNil(errs,
		validateChoice("status", d.Status, DeviceStatuses),
		validateChoice("face", d.Face, DeviceFaces),
		validateLength("name", d.Name, MaxDeviceNameLength),
		validateLength("serial", d.Serial, MaxSerialLength),
		validateLength("asset_tag", d.AssetTag, MaxAssetTagLength),
	)

	for i, iface := range d.Interfaces {
		if iface.Name == "" {
			errs = append(errs, fmt.Errorf("interface %d: name is required", i+1))
			continue
		}
		if err := validateLink(iface.Link); err != nil {
			errs = append(errs, fmt.Errorf("interface %q: %w", iface.Name, err))
		}
		if iface.IP != nil && iface.IP.Address == "" {
			errs = append(errs, fmt.Errorf("interface %q: ip.address is required", iface.Name))
		}
	}
	for i, fp := range d.FrontPorts {
		if fp.Name == "" {
			errs = append(errs, fmt.Errorf("front port %d: name is required", i+1))
			continue
		}
		if fp.RearPort == "" {
			errs = append(errs, fmt.Errorf("front port %q: rear_port is required", fp.Name))
		}
		if err := validateLink(fp.Link); err != nil {
			errs = append(errs, fmt.Errorf("front port %q: %w", fp.Name, err))
		}
	}
	for i, rp := range d.RearPorts {
		if rp.Name == "" {
			errs = append(errs, fmt.Errorf("rear port %d: name is required", i+1))
			continue
		}
		if err := validateLink(rp.Link); err != nil {
			errs = append(errs, fmt.Errorf("rear port %q: %w", rp.Name, err))
		}
	}
	for i, mod := range d.Modules {
		if mod.Name == "" {
			errs = append(errs, fmt.Errorf("module %d: name is required", i+1))
			continue
		}
		if mod.ModuleTypeSlug == "" {
			errs = append(errs, fmt.Errorf("module %q: module_type_slug is required", mod.Name))
		}
	}

	return wrap("device", d.Name, errs)
}

// Validate checks required fields of a Platform.
func (p *Platform) Validate() error {
	errs := requireFields(map[string]string{
		"name": p.Name,
		"slug": p.Slug,
	})
	return wrap("platform", p.Name, errs)
}

// Validate checks required fields of a TenantGroup.
func (g *TenantGroup) Validate() error {
	errs := requireFields(map[string]string{
		"name": g.Name,
		"slug": g.Slug,
	})
	return wrap("tenant group", g.Name, errs)
}

// Validate checks required fields of a CustomField.
func (c *CustomField) Validate() error {
	errs := requireFields(map[string]string{
		"name": c.Name,
		"type": c.Type,
	})
	if len(c.ObjectTypes) == 0 {
		errs = append(errs, errors.New("object_types must list at least one content type (e.g. virtualization.virtualmachine)"))
	}
	return wrap("custom field", c.Name, errs)
}

// Validate checks required fields of a Tenant.
func (t *Tenant) Validate() error {
	errs := requireFields(map[string]string{
		"name": t.Name,
		"slug": t.Slug,
	})
	return wrap("tenant", t.Name, errs)
}

// Validate checks required fields of a ClusterType.
func (c *ClusterType) Validate() error {
	errs := requireFields(map[string]string{
		"name": c.Name,
		"slug": c.Slug,
	})
	return wrap("cluster type", c.Name, errs)
}

// Validate checks required fields of a ClusterGroup.
func (c *ClusterGroup) Validate() error {
	errs := requireFields(map[string]string{
		"name": c.Name,
		"slug": c.Slug,
	})
	return wrap("cluster group", c.Name, errs)
}

// Validate checks required fields of a Cluster.
func (c *Cluster) Validate() error {
	errs := requireFields(map[string]string{
		"name":      c.Name,
		"type_slug": c.TypeSlug,
	})
	return wrap("cluster", c.Name, errs)
}

// Validate checks required fields and cross-field constraints of a VMConfig,
// including its nested interface configs. A VM must belong to a cluster or a
// site (NetBox allows non-clustered, site-scoped VMs); both may be set.
func (v *VMConfig) Validate() error {
	errs := requireFields(map[string]string{"name": v.Name})

	if v.Cluster == "" && v.SiteSlug == "" {
		errs = append(errs, errors.New("one of cluster or site_slug is required"))
	}
	errs = appendNonNil(errs,
		validateChoice("status", v.Status, VMStatuses),
		validateLength("name", v.Name, MaxDeviceNameLength),
	)

	// When a VM opts into Proxmox provisioning, the facts Terraform needs must
	// be present up front so the error surfaces in --dry-run/yamlcheck rather
	// than mid-apply. Documentation-only VMs are free to omit all of these.
	if v.Provision {
		if v.VMID <= 0 {
			errs = append(errs, errors.New("vmid is required when provision is true (the Proxmox VMID to create)"))
		}
		if v.VMTemplateID <= 0 {
			errs = append(errs, errors.New("vm_template_id is required when provision is true (the template VMID to clone, e.g. 800)"))
		}
		if v.Node == "" {
			errs = append(errs, errors.New("node is required when provision is true (the target Proxmox node)"))
		}
	}

	for i, iface := range v.Interfaces {
		if iface.Name == "" {
			errs = append(errs, fmt.Errorf("interface %d: name is required", i+1))
			continue
		}
		if iface.IP != nil && iface.IP.Address == "" {
			errs = append(errs, fmt.Errorf("interface %q: ip.address is required", iface.Name))
		}
	}

	return wrap("virtual machine", v.Name, errs)
}

// validateLink checks that a cable link definition names both ends.
func validateLink(link *LinkConfig) error {
	if link == nil {
		return nil
	}
	if link.PeerDevice == "" {
		return errors.New("link.peer_device is required")
	}
	if link.PeerPort == "" {
		return errors.New("link.peer_port is required")
	}
	return nil
}

// wrap aggregates validation errors with the object kind and identifier.
func wrap(kind, name string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if name == "" {
		name = "<unnamed>"
	}
	return fmt.Errorf("%s %q: %w", kind, name, errors.Join(errs...))
}
