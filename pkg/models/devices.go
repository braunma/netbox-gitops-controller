package models

// LinkConfig represents a cable connection definition
type LinkConfig struct {
	PeerDevice string  `yaml:"peer_device" json:"peer_device" validate:"required"`
	PeerPort   string  `yaml:"peer_port" json:"peer_port" validate:"required"`
	CableType  string  `yaml:"cable_type,omitempty" json:"cable_type,omitempty"`
	Color      string  `yaml:"color,omitempty" json:"color,omitempty"`
	Length     float64 `yaml:"length,omitempty" json:"length,omitempty"`
	LengthUnit string  `yaml:"length_unit,omitempty" json:"length_unit,omitempty"`
}

// IPConfig represents IP address configuration
type IPConfig struct {
	Address     string   `yaml:"address" json:"address" validate:"required"`
	DNSName     string   `yaml:"dns_name,omitempty" json:"dns_name,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	VRF         string   `yaml:"vrf,omitempty" json:"vrf,omitempty"`
	AddressRole string   `yaml:"address_role,omitempty" json:"address_role,omitempty"`
}

// InterfaceConfig represents an interface configuration (for concrete devices)
type InterfaceConfig struct {
	Name         string       `yaml:"name" json:"name" validate:"required"`
	Type         string       `yaml:"type,omitempty" json:"type,omitempty"`
	Enabled      bool         `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Label        string       `yaml:"label,omitempty" json:"label,omitempty"`
	Description  string       `yaml:"description,omitempty" json:"description,omitempty"`
	MTU          int          `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	Link         *LinkConfig  `yaml:"link,omitempty" json:"link,omitempty"`
	Mode         string       `yaml:"mode,omitempty" json:"mode,omitempty"`
	UntaggedVLAN string       `yaml:"untagged_vlan,omitempty" json:"untagged_vlan,omitempty"`
	TaggedVLANs  []string     `yaml:"tagged_vlans,omitempty" json:"tagged_vlans,omitempty"`
	IP           *IPConfig    `yaml:"ip,omitempty" json:"ip,omitempty"`
	AddressRole  string       `yaml:"address_role,omitempty" json:"address_role,omitempty"`
	Members      []string     `yaml:"members,omitempty" json:"members,omitempty"`
	Tags         []string     `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// RearPortConfig represents a rear port configuration (Backbone)
type RearPortConfig struct {
	Name        string      `yaml:"name" json:"name" validate:"required"`
	Type        string      `yaml:"type,omitempty" json:"type,omitempty"`
	Label       string      `yaml:"label,omitempty" json:"label,omitempty"`
	Positions   int         `yaml:"positions,omitempty" json:"positions,omitempty"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
	Link        *LinkConfig `yaml:"link,omitempty" json:"link,omitempty"`
}

// FrontPortConfig represents a front port configuration (Patch)
type FrontPortConfig struct {
	Name             string      `yaml:"name" json:"name" validate:"required"`
	Type             string      `yaml:"type" json:"type" validate:"required"`
	Label            string      `yaml:"label,omitempty" json:"label,omitempty"`
	Description      string      `yaml:"description,omitempty" json:"description,omitempty"`
	Tags             []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
	RearPort         string      `yaml:"rear_port" json:"rear_port" validate:"required"`
	RearPortPosition int         `yaml:"rear_port_position,omitempty" json:"rear_port_position,omitempty"`
	Link             *LinkConfig `yaml:"link,omitempty" json:"link,omitempty"`
}

// ModuleConfig represents a module configuration (e.g., installed GPU)
type ModuleConfig struct {
	Name           string   `yaml:"name" json:"name" validate:"required"`
	ModuleTypeSlug string   `yaml:"module_type_slug" json:"module_type_slug" validate:"required"`
	Status         string   `yaml:"status,omitempty" json:"status,omitempty"`
	Serial         string   `yaml:"serial,omitempty" json:"serial,omitempty"`
	AssetTag       string   `yaml:"asset_tag,omitempty" json:"asset_tag,omitempty"`
	Description    string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags           []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// DeviceConfig represents a device configuration (concrete device)
type DeviceConfig struct {
	Name           string              `yaml:"name" json:"name" validate:"required"`
	SiteSlug       string              `yaml:"site_slug" json:"site_slug" validate:"required"`
	DeviceTypeSlug string              `yaml:"device_type_slug" json:"device_type_slug" validate:"required"`
	RoleSlug       string              `yaml:"role_slug" json:"role_slug" validate:"required"`
	RackSlug       string              `yaml:"rack_slug,omitempty" json:"rack_slug,omitempty"`
	Position       int                 `yaml:"position,omitempty" json:"position,omitempty"`
	Face           string              `yaml:"face,omitempty" json:"face,omitempty"`
	ParentDevice   string              `yaml:"parent_device,omitempty" json:"parent_device,omitempty"`
	DeviceBay      string              `yaml:"device_bay,omitempty" json:"device_bay,omitempty"`
	Status         string              `yaml:"status,omitempty" json:"status,omitempty"`
	Serial         string              `yaml:"serial,omitempty" json:"serial,omitempty"`
	AssetTag       string              `yaml:"asset_tag,omitempty" json:"asset_tag,omitempty"`
	Tags           []string            `yaml:"tags,omitempty" json:"tags,omitempty"`
	Modules        []ModuleConfig      `yaml:"modules,omitempty" json:"modules,omitempty"`
	Interfaces     []InterfaceConfig   `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	FrontPorts     []FrontPortConfig   `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
	RearPorts      []RearPortConfig    `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
}

// Slug generates a slug from the device name
func (d *DeviceConfig) Slug() string {
	return slugify(d.Name)
}
