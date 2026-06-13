package models

// Site represents a NetBox site
type Site struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug" validate:"required"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Region      string   `yaml:"region,omitempty" json:"region,omitempty"`
	TimeZone    string   `yaml:"time_zone,omitempty" json:"time_zone,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Comments    string   `yaml:"comments,omitempty" json:"comments,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Rack represents a NetBox rack
type Rack struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug"`
	SiteSlug    string   `yaml:"site_slug" json:"site_slug" validate:"required"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Width       int      `yaml:"width,omitempty" json:"width,omitempty"`
	UHeight     int      `yaml:"u_height,omitempty" json:"u_height,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Role represents a device role
type Role struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Slug        string `yaml:"slug" json:"slug" validate:"required"`
	Color       string `yaml:"color" json:"color" validate:"required"`
	VMRole      bool   `yaml:"vm_role,omitempty" json:"vm_role,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Tag represents a NetBox tag
type Tag struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Slug        string `yaml:"slug" json:"slug" validate:"required"`
	Color       string `yaml:"color" json:"color" validate:"required"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Manufacturer represents a hardware manufacturer
type Manufacturer struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug" validate:"required"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Platform represents a NetBox platform (e.g. an OS or firmware family that a
// device or virtual machine runs). Referenced by slug from VMs and devices.
type Platform struct {
	Name         string   `yaml:"name" json:"name" validate:"required"`
	Slug         string   `yaml:"slug" json:"slug" validate:"required"`
	Manufacturer string   `yaml:"manufacturer,omitempty" json:"manufacturer,omitempty"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags         []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// TenantGroup represents a NetBox tenant group (tenancy app).
type TenantGroup struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug" validate:"required"`
	ParentSlug  string   `yaml:"parent_slug,omitempty" json:"parent_slug,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Tenant represents a NetBox tenant (tenancy app). Referenced by slug from
// clusters, VMs and devices.
type Tenant struct {
	Name        string   `yaml:"name" json:"name" validate:"required"`
	Slug        string   `yaml:"slug" json:"slug" validate:"required"`
	GroupSlug   string   `yaml:"group_slug,omitempty" json:"group_slug,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}
