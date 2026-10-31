// SPDX-License-Identifier: Apache-2.0

package models

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PatchPanelRole is the role slug that makes a device a patch panel for
// cabling: the reconciler wires a cable into such a device onto a front port
// (or, between two of them, rear port to rear port), the lint mirrors that,
// and a cable path (LinkConfig.Via) may only run through devices with it.
const PatchPanelRole = "patch-panel"

// LinkConfig represents a cable connection definition.
//
// Without Via it is one cable: this port to peer_device/peer_port. With Via
// it is a whole path through patch panels, written once at the endpoint:
//
//	link:
//	  via: ["pp-a01:2", "pp-b02:2"]
//	  peer_device: "leaf-02"
//	  peer_port: "Eth1/1"
//
// The loader expands that into the cables NetBox holds — this port to the
// first panel's front port, each panel's rear port to the next panel's rear
// port (the front-to-rear mapping comes from the panel's device type), and the
// last panel's front port to the peer — so the three cables of a strand are
// no longer three declarations in three files. See loader.ExpandCablePaths.
type LinkConfig struct {
	PeerDevice string  `yaml:"peer_device" json:"peer_device"`
	PeerPort   string  `yaml:"peer_port" json:"peer_port"`
	CableType  string  `yaml:"cable_type,omitempty" json:"cable_type,omitempty"`
	Color      string  `yaml:"color,omitempty" json:"color,omitempty"`
	Length     float64 `yaml:"length,omitempty" json:"length,omitempty"`
	LengthUnit string  `yaml:"length_unit,omitempty" json:"length_unit,omitempty"`
	// Via lists the patch panels the cable runs through, in order from this
	// port to the peer, each as the panel's front port the cord lands on.
	Via []ViaHop `yaml:"via,omitempty" json:"via,omitempty"`
}

// ViaHop is one patch panel on a cable path: the panel and the front port the
// cord lands on. The cable attributes describe the cable that LEAVES this
// panel toward the peer (the backbone to the next panel, or the last cord to
// the peer). Where they are empty, a cord takes the link's attributes and
// then the panel's cable_defaults.front; a backbone takes the panel's
// cable_defaults.rear.
//
// In YAML a hop is either a mapping or the shorthand "device:port":
//
//	via: ["pp-a01:2", { device: "pp-b02", port: "2", color: "blue" }]
type ViaHop struct {
	Device     string  `yaml:"device" json:"device"`
	Port       string  `yaml:"port" json:"port"`
	CableType  string  `yaml:"cable_type,omitempty" json:"cable_type,omitempty"`
	Color      string  `yaml:"color,omitempty" json:"color,omitempty"`
	Length     float64 `yaml:"length,omitempty" json:"length,omitempty"`
	LengthUnit string  `yaml:"length_unit,omitempty" json:"length_unit,omitempty"`
}

// UnmarshalYAML accepts the "device:port" shorthand next to the mapping form.
func (h *ViaHop) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		device, port, ok := strings.Cut(s, ":")
		if !ok || device == "" || port == "" {
			return fmt.Errorf("via hop %q must be \"device:port\" or a mapping with device and port", s)
		}
		h.Device, h.Port = device, port
		return nil
	}
	type rawViaHop ViaHop
	var raw rawViaHop
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*h = ViaHop(raw)
	return nil
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

// CableAttrs are the attributes of a cable that a patch panel can supply as
// defaults for the cables on its ports (see CableDefaults).
type CableAttrs struct {
	CableType  string  `yaml:"cable_type,omitempty" json:"cable_type,omitempty"`
	Color      string  `yaml:"color,omitempty" json:"color,omitempty"`
	Length     float64 `yaml:"length,omitempty" json:"length,omitempty"`
	LengthUnit string  `yaml:"length_unit,omitempty" json:"length_unit,omitempty"`
}

// CableDefaults are what the cables of a path (`link.via`) through a patch
// panel are unless the path says otherwise: Front for the patch cords into
// its front ports, Rear for the backbones out of its rear ports. A panel
// takes one kind of connector, and the colour of a patch cord is rarely worth
// writing down per cable, so the panel declares both once and a path through
// it carries only what differs. A cable declared on its own, with its own
// `link:`, is not touched: it may be a different cable altogether. Declared
// under `cable_defaults:` on the panel (or in the file's `defaults:` for
// every panel in it).
type CableDefaults struct {
	Front CableAttrs `yaml:"front,omitempty" json:"front,omitempty"`
	Rear  CableAttrs `yaml:"rear,omitempty" json:"rear,omitempty"`
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
	// CableDefaults, on a patch panel, are the cable type and colour the
	// segments of a `link.via` path get at this panel unless the path says
	// otherwise; see CableDefaults.
	CableDefaults *CableDefaults    `yaml:"cable_defaults,omitempty" json:"cable_defaults,omitempty"`
	Tags          []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	Modules       []ModuleConfig    `yaml:"modules,omitempty" json:"modules,omitempty"`
	Interfaces    []InterfaceConfig `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	FrontPorts    []FrontPortConfig `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
	RearPorts     []RearPortConfig  `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
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
