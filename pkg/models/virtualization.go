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
	Enabled      *bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
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
//
// Provisioning is opt-in and independent of NetBox documentation: a VM is only
// created in Proxmox when Provision is true. Documentation-only VMs (Provision
// false or unset) are reconciled into NetBox but skipped by the Terraform
// generator, even if they carry a VMID/VMTemplateID for reference.
type VMConfig struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	// Provision opts the VM into Proxmox provisioning via Terraform. When false
	// (the default) the VM is documented in NetBox only.
	Provision bool `yaml:"provision,omitempty" json:"provision,omitempty"`
	VMID      int  `yaml:"vmid,omitempty" json:"vmid,omitempty"`
	// VMTemplateID is the numeric Proxmox VMID of the template VM to clone from
	// (e.g. 800). It is the only thing used to provision the VM; Platform is kept
	// purely for NetBox documentation. Stored in NetBox as a custom field too.
	VMTemplateID int                 `yaml:"vm_template_id,omitempty" json:"vm_template_id,omitempty"`
	Node         string              `yaml:"node,omitempty" json:"node,omitempty"`
	Cluster      string              `yaml:"cluster,omitempty" json:"cluster,omitempty"`
	SiteSlug     string              `yaml:"site_slug,omitempty" json:"site_slug,omitempty"`
	RoleSlug     string              `yaml:"role_slug,omitempty" json:"role_slug,omitempty"`
	Platform     string              `yaml:"platform,omitempty" json:"platform,omitempty"`
	Tenant       string              `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	Status       string              `yaml:"status,omitempty" json:"status,omitempty"`
	VCPUs        int                 `yaml:"vcpus,omitempty" json:"vcpus,omitempty"`
	Memory       int                 `yaml:"memory,omitempty" json:"memory,omitempty"`
	Disk         int                 `yaml:"disk,omitempty" json:"disk,omitempty"`
	Tags         []string            `yaml:"tags,omitempty" json:"tags,omitempty"`
	Interfaces   []VMInterfaceConfig `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
}

// Slug generates a slug from the virtual machine name.
func (v *VMConfig) Slug() string {
	return slugify(v.Name)
}

// IsEnabled reports whether the VM interface should be enabled in NetBox.
// Interfaces are enabled unless explicitly set to `enabled: false` in YAML.
func (i *VMInterfaceConfig) IsEnabled() bool {
	return i.Enabled == nil || *i.Enabled
}
