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
	return wrap("site", s.Name, errs)
}

// Validate checks required fields of a Rack.
func (r *Rack) Validate() error {
	errs := requireFields(map[string]string{
		"name":      r.Name,
		"site_slug": r.SiteSlug,
	})
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
