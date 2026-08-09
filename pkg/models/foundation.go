// SPDX-License-Identifier: Apache-2.0

package models

// Site represents a NetBox site
type Site struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	Slug string `yaml:"slug" json:"slug" validate:"required"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Region      string   `yaml:"region,omitempty" json:"region,omitempty"`
	TimeZone    string   `yaml:"time_zone,omitempty" json:"time_zone,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Comments    string   `yaml:"comments,omitempty" json:"comments,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Rack represents a NetBox rack
type Rack struct {
	Name     string `yaml:"name" json:"name" validate:"required"`
	Slug     string `yaml:"slug" json:"slug"`
	SiteSlug string `yaml:"site_slug" json:"site_slug" validate:"required"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Width       int      `yaml:"width,omitempty" json:"width,omitempty"`
	UHeight     int      `yaml:"u_height,omitempty" json:"u_height,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Role represents a device role
type Role struct {
	Name  string `yaml:"name" json:"name" validate:"required"`
	Slug  string `yaml:"slug" json:"slug" validate:"required"`
	Color string `yaml:"color" json:"color" validate:"required"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	VMRole      bool   `yaml:"vm_role,omitempty" json:"vm_role,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Tag represents a NetBox tag
type Tag struct {
	Name  string `yaml:"name" json:"name" validate:"required"`
	Slug  string `yaml:"slug" json:"slug" validate:"required"`
	Color string `yaml:"color" json:"color" validate:"required"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Manufacturer represents a hardware manufacturer
type Manufacturer struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	Slug string `yaml:"slug" json:"slug" validate:"required"`
	// RenameFrom is this object's previous name or slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Platform represents a NetBox platform (e.g. an OS or firmware family that a
// device or virtual machine runs). Referenced by slug from VMs and devices.
type Platform struct {
	Name         string `yaml:"name" json:"name" validate:"required"`
	Slug         string `yaml:"slug" json:"slug" validate:"required"`
	Manufacturer string `yaml:"manufacturer,omitempty" json:"manufacturer,omitempty"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// TenantGroup represents a NetBox tenant group (tenancy app).
type TenantGroup struct {
	Name       string `yaml:"name" json:"name" validate:"required"`
	Slug       string `yaml:"slug" json:"slug" validate:"required"`
	ParentSlug string `yaml:"parent_slug,omitempty" json:"parent_slug,omitempty"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// CustomField represents a NetBox custom field definition (extras app). It is
// declared once and attached to one or more object types (e.g. the
// `vmid` field on `virtualization.virtualmachine`); the per-object value is
// then set from the owning object's YAML (e.g. VMConfig.VMID).
//
// NetBox 4.x names the applicable content types `object_types` (older
// releases used `content_types`). This model targets the current name.
type CustomField struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Type        string   `yaml:"type" json:"type" validate:"required"`
	ObjectTypes []string `yaml:"object_types" json:"object_types" validate:"required"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

// Tenant represents a NetBox tenant (tenancy app). Referenced by slug from
// clusters, VMs and devices.
type Tenant struct {
	Name      string `yaml:"name" json:"name" validate:"required"`
	Slug      string `yaml:"slug" json:"slug" validate:"required"`
	GroupSlug string `yaml:"group_slug,omitempty" json:"group_slug,omitempty"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}
