// SPDX-License-Identifier: Apache-2.0

package models

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// LinkConfig represents a cable connection definition
type LinkConfig struct {
	PeerDevice string  `yaml:"peer_device" json:"peer_device"`
	PeerPort   string  `yaml:"peer_port" json:"peer_port"`
	CableType  string  `yaml:"cable_type,omitempty" json:"cable_type,omitempty"`
	Color      string  `yaml:"color,omitempty" json:"color,omitempty"`
	Length     float64 `yaml:"length,omitempty" json:"length,omitempty"`
	LengthUnit string  `yaml:"length_unit,omitempty" json:"length_unit,omitempty"`
}

// IPConfig represents IP address configuration
type IPConfig struct {
	Address     string   `yaml:"address" json:"address"`
	DNSName     string   `yaml:"dns_name,omitempty" json:"dns_name,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	VRF         string   `yaml:"vrf,omitempty" json:"vrf,omitempty"`
	AddressRole string   `yaml:"address_role,omitempty" json:"address_role,omitempty"`
}

// UnmarshalYAML accepts either the full mapping form or a plain CIDR string
// as a shorthand:
//
//	ip: "10.0.0.1/24"
//
// is equivalent to
//
//	ip:
//	  address: "10.0.0.1/24"
func (ip *IPConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&ip.Address)
	}
	type rawIPConfig IPConfig // alias without UnmarshalYAML to avoid recursion
	var raw rawIPConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*ip = IPConfig(raw)
	return nil
}

// InterfaceConfig represents an interface configuration (for concrete devices)
type InterfaceConfig struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom   string      `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Enabled      *bool       `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Label        string      `yaml:"label,omitempty" json:"label,omitempty"`
	Description  string      `yaml:"description,omitempty" json:"description,omitempty"`
	MTU          int         `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	Link         *LinkConfig `yaml:"link,omitempty" json:"link,omitempty"`
	Mode         string      `yaml:"mode,omitempty" json:"mode,omitempty"`
	UntaggedVLAN string      `yaml:"untagged_vlan,omitempty" json:"untagged_vlan,omitempty"`
	TaggedVLANs  []string    `yaml:"tagged_vlans,omitempty" json:"tagged_vlans,omitempty"`
	IP           *IPConfig   `yaml:"ip,omitempty" json:"ip,omitempty"`
	AddressRole  string      `yaml:"address_role,omitempty" json:"address_role,omitempty"`
	Members      []string    `yaml:"members,omitempty" json:"members,omitempty"`
	Tags         []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// IsPrimaryIP reports whether the interface claims the device's primary IP.
// The role may be declared on the interface itself or inside its ip mapping;
// both spellings appear in the wild, so every consumer honours both.
func (i InterfaceConfig) IsPrimaryIP() bool {
	if strings.EqualFold(i.AddressRole, "primary") {
		return true
	}
	return i.IP != nil && strings.EqualFold(i.IP.AddressRole, "primary")
}

// RearPortConfig represents a rear port configuration (Backbone)
type RearPortConfig struct {
	Name        string      `yaml:"name" json:"name"`
	Type        string      `yaml:"type,omitempty" json:"type,omitempty"`
	Label       string      `yaml:"label,omitempty" json:"label,omitempty"`
	Positions   int         `yaml:"positions,omitempty" json:"positions,omitempty"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
	Link        *LinkConfig `yaml:"link,omitempty" json:"link,omitempty"`
}

// FrontPortConfig represents a front port configuration (Patch)
type FrontPortConfig struct {
	Name             string      `yaml:"name" json:"name"`
	Type             string      `yaml:"type" json:"type"`
	Label            string      `yaml:"label,omitempty" json:"label,omitempty"`
	Description      string      `yaml:"description,omitempty" json:"description,omitempty"`
	Tags             []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
	RearPort         string      `yaml:"rear_port" json:"rear_port"`
	RearPortPosition int         `yaml:"rear_port_position,omitempty" json:"rear_port_position,omitempty"`
	Link             *LinkConfig `yaml:"link,omitempty" json:"link,omitempty"`
}

// ModuleConfig represents a module configuration (e.g., installed GPU)
//
// In YAML the references are usually written by their short names (module_type
// for module_type_slug); see YAMLAliases.
type ModuleConfig struct {
	Name           string   `yaml:"name" json:"name"`
	ModuleTypeSlug string   `yaml:"module_type_slug" json:"module_type_slug"`
	Status         string   `yaml:"status,omitempty" json:"status,omitempty"`
	Serial         string   `yaml:"serial,omitempty" json:"serial,omitempty"`
	AssetTag       string   `yaml:"asset_tag,omitempty" json:"asset_tag,omitempty"`
	Description    string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags           []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// DeviceConfig represents a device configuration (concrete device)
//
// In YAML the references are usually written by their short names (site, role,
// device_type and rack for the *_slug keys); see YAMLAliases.
type DeviceConfig struct {
	Name     string `yaml:"name" json:"name"`
	SiteSlug string `yaml:"site_slug" json:"site_slug"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom     string `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	DeviceTypeSlug string `yaml:"device_type_slug" json:"device_type_slug"`
	RoleSlug       string `yaml:"role_slug" json:"role_slug"`
	RackSlug       string `yaml:"rack_slug,omitempty" json:"rack_slug,omitempty"`
	Position       int    `yaml:"position,omitempty" json:"position,omitempty"`
	Face           string `yaml:"face,omitempty" json:"face,omitempty"`
	ParentDevice   string `yaml:"parent_device,omitempty" json:"parent_device,omitempty"`
	DeviceBay      string `yaml:"device_bay,omitempty" json:"device_bay,omitempty"`
	Status         string `yaml:"status,omitempty" json:"status,omitempty"`
	Serial         string `yaml:"serial,omitempty" json:"serial,omitempty"`
	AssetTag       string `yaml:"asset_tag,omitempty" json:"asset_tag,omitempty"`
	// CustomFields holds values for NetBox custom fields defined on the device
	// content type, keyed by field name. Only the fields declared here are
	// written; a field NetBox holds but the YAML does not mention is left
	// alone, so a value somebody set in the UI survives a sync.
	//
	// This is where the `hw_*` hardware inventory lands when facts are
	// ingested from an out-of-band scan; see docs/INGEST.md.
	CustomFields map[string]interface{} `yaml:"custom_fields,omitempty" json:"custom_fields,omitempty"`
	Tags         []string               `yaml:"tags,omitempty" json:"tags,omitempty"`
	Modules      []ModuleConfig         `yaml:"modules,omitempty" json:"modules,omitempty"`
	Interfaces   []InterfaceConfig      `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	FrontPorts   []FrontPortConfig      `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
	RearPorts    []RearPortConfig       `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
}

// EffectiveFace is the rack face sent to NetBox: the declared one, or "front"
// when the device has a rack position but names no face. NetBox requires a
// face with every position and answers 400 otherwise; nearly every device is
// front-mounted, so the YAML only has to say so when it is not. The default
// is documented in the README under "Conventions that keep the YAML small".
func (d *DeviceConfig) EffectiveFace() string {
	if d.Face == "" && d.Position > 0 {
		return "front"
	}
	return d.Face
}

// Slug generates a slug from the device name
func (d *DeviceConfig) Slug() string {
	return slugify(d.Name)
}

// IsEnabled reports whether the interface should be enabled in NetBox.
// Interfaces are enabled unless explicitly set to `enabled: false` in YAML.
func (i *InterfaceConfig) IsEnabled() bool {
	return i.Enabled == nil || *i.Enabled
}
