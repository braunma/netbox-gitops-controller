// SPDX-License-Identifier: Apache-2.0

package models

import "strings"

// ClusterType represents a NetBox cluster type (virtualization app), e.g.
// "VMware vSphere" or "Proxmox".
type ClusterType struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	Slug string `yaml:"slug" json:"slug" validate:"required"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// ClusterGroup represents a NetBox cluster group (virtualization app), an
// organizational grouping of clusters.
type ClusterGroup struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	Slug string `yaml:"slug" json:"slug" validate:"required"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Cluster represents a NetBox cluster of virtualization hosts. A cluster
// requires a type and may optionally belong to a group, site and tenant.
type Cluster struct {
	Name      string `yaml:"name" json:"name" validate:"required"`
	TypeSlug  string `yaml:"type_slug" json:"type_slug" validate:"required"`
	GroupSlug string `yaml:"group_slug,omitempty" json:"group_slug,omitempty"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
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
	Name string `yaml:"name" json:"name" validate:"required"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom   string    `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
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

// IsPrimaryIP reports whether the interface claims the VM's primary IP. As
// with device interfaces, the role may sit on the interface or inside its ip
// mapping.
func (i VMInterfaceConfig) IsPrimaryIP() bool {
	if strings.EqualFold(i.AddressRole, "primary") {
		return true
	}
	return i.IP != nil && strings.EqualFold(i.IP.AddressRole, "primary")
}

// VMConfig represents a virtual machine. A VM must belong to exactly one of a
// cluster or a site (NetBox allows non-clustered, site-scoped VMs).
//
// Everything here is documentation: the controller writes VMs into NetBox and
// never creates them on a hypervisor. Facts discovered by `netbox-gitops
// collect` are written back into these same YAML files and reviewed as a diff;
// see docs/INGEST.md.
type VMConfig struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom string `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	// Provision is the removed Proxmox provisioning opt-in. It is still parsed
	// so that a file which still carries the key fails validation with a
	// pointer at the changelog, instead of being silently ignored and leaving
	// the author believing something is still being provisioned. Remove the
	// key; nothing replaces it.
	Provision *bool `yaml:"provision,omitempty" json:"-"`
	// VMID is the hypervisor's numeric identifier for this VM (the Proxmox
	// VMID). NetBox's virtual-machine model has no native slot for it, so it is
	// stored in the `vmid` custom field. Collectors may write this key.
	VMID int `yaml:"vmid,omitempty" json:"vmid,omitempty"`
	// VMTemplateID is the numeric VMID of the template this VM was cloned from
	// (e.g. 800). Documentation only, stored in the `vm_template_id` custom
	// field; nothing in this controller acts on it.
	VMTemplateID int `yaml:"vm_template_id,omitempty" json:"vm_template_id,omitempty"`
	// Node is the hypervisor node the VM runs on. Documentation only.
	Node       string              `yaml:"node,omitempty" json:"node,omitempty"`
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

// IsEnabled reports whether the VM interface should be enabled in NetBox.
// Interfaces are enabled unless explicitly set to `enabled: false` in YAML.
func (i *VMInterfaceConfig) IsEnabled() bool {
	return i.Enabled == nil || *i.Enabled
}
