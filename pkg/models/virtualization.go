package models

// ClusterType represents a NetBox cluster type (virtualization app), e.g.
// "VMware vSphere" or "Proxmox".
type ClusterType struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug" validate:"required"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// ClusterGroup represents a NetBox cluster group (virtualization app), an
// organizational grouping of clusters.
type ClusterGroup struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug" validate:"required"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Cluster represents a NetBox cluster of virtualization hosts. A cluster
// requires a type and may optionally belong to a group, site and tenant.
type Cluster struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	TypeSlug    string   `yaml:"type_slug" json:"type_slug" validate:"required"`
	GroupSlug   string   `yaml:"group_slug,omitempty" json:"group_slug,omitempty"`
	SiteSlug    string   `yaml:"site_slug,omitempty" json:"site_slug,omitempty"`
	Tenant      string   `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// VMInterfaceConfig represents a virtual machine interface. It mirrors the
// device InterfaceConfig (VLAN/IP semantics) but cannot be cabled, so it has
// no Link field.
type VMInterfaceConfig struct {
	Name         string    `yaml:"name" json:"name" validate:"required"`
	Enabled      bool      `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Description  string    `yaml:"description,omitempty" json:"description,omitempty"`
	MTU          int       `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	MACAddress   string    `yaml:"mac_address,omitempty" json:"mac_address,omitempty"`
	Mode         string    `yaml:"mode,omitempty" json:"mode,omitempty"`
	UntaggedVLAN string    `yaml:"untagged_vlan,omitempty" json:"untagged_vlan,omitempty"`
	TaggedVLANs  []string  `yaml:"tagged_vlans,omitempty" json:"tagged_vlans,omitempty"`
	Parent       string    `yaml:"parent,omitempty" json:"parent,omitempty"`
	IP           *IPConfig `yaml:"ip,omitempty" json:"ip,omitempty"`
	AddressRole  string    `yaml:"address_role,omitempty" json:"address_role,omitempty"`
	Tags         []string  `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// VMConfig represents a virtual machine. A VM must belong to exactly one of a
// cluster or a site (NetBox allows non-clustered, site-scoped VMs).
type VMConfig struct {
	Name       string              `yaml:"name" json:"name" validate:"required"`
	VMID       int                 `yaml:"vmid,omitempty" json:"vmid,omitempty"`
	Cluster    string              `yaml:"cluster,omitempty" json:"cluster,omitempty"`
	SiteSlug   string              `yaml:"site_slug,omitempty" json:"site_slug,omitempty"`
	RoleSlug   string              `yaml:"role_slug,omitempty" json:"role_slug,omitempty"`
	Platform   string              `yaml:"platform,omitempty" json:"platform,omitempty"`
	Tenant     string              `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	Status     string              `yaml:"status,omitempty" json:"status,omitempty"`
	VCPUs      int                 `yaml:"vcpus,omitempty" json:"vcpus,omitempty"`
	Memory     int                 `yaml:"memory,omitempty" json:"memory,omitempty"`
	Disk       int                 `yaml:"disk,omitempty" json:"disk,omitempty"`
	Tags       []string            `yaml:"tags,omitempty" json:"tags,omitempty"`
	Interfaces []VMInterfaceConfig `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
}

// Slug generates a slug from the virtual machine name.
func (v *VMConfig) Slug() string {
	return slugify(v.Name)
}
