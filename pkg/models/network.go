// SPDX-License-Identifier: Apache-2.0

package models

import "strings"

// VLAN represents a NetBox VLAN
type VLAN struct {
	Name      string `yaml:"name" json:"name" validate:"required"`
	VID       int    `yaml:"vid" json:"vid" validate:"required,min=1,max=4094"`
	SiteSlug  string `yaml:"site_slug" json:"site_slug" validate:"required"`
	GroupSlug string `yaml:"group_slug,omitempty" json:"group_slug,omitempty"`
	// RenameFrom is this object's previous VID, as a string. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Role        string   `yaml:"role,omitempty" json:"role,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// VLANGroup represents a VLAN group
type VLANGroup struct {
	Name     string `yaml:"name" json:"name" validate:"required"`
	Slug     string `yaml:"slug" json:"slug" validate:"required"`
	SiteSlug string `yaml:"site_slug,omitempty" json:"site_slug,omitempty"`
	// RenameFrom is this object's previous slug. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	MinVID      int      `yaml:"min_vid,omitempty" json:"min_vid,omitempty"`
	MaxVID      int      `yaml:"max_vid,omitempty" json:"max_vid,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// VRF represents a NetBox VRF
type VRF struct {
	Name string `yaml:"name" json:"name" validate:"required"`
	RD   string `yaml:"rd,omitempty" json:"rd,omitempty"`
	// RenameFrom is this object's previous name. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom    string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Description   string   `yaml:"description,omitempty" json:"description,omitempty"`
	EnforceUnique bool     `yaml:"enforce_unique,omitempty" json:"enforce_unique,omitempty"`
	Tags          []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Slug generates a slug from the VRF name
func (v *VRF) Slug() string {
	return slugify(v.Name)
}

// Prefix represents an IP prefix
type Prefix struct {
	Prefix   string `yaml:"prefix" json:"prefix" validate:"required"`
	SiteSlug string `yaml:"site_slug,omitempty" json:"site_slug,omitempty"`
	VRFName  string `yaml:"vrf_name,omitempty" json:"vrf_name,omitempty"`
	VLANName string `yaml:"vlan_name,omitempty" json:"vlan_name,omitempty"`
	// RenameFrom is this object's previous prefix. Set it to correct a typo
	// so the existing object is renamed instead of a second one being
	// created; remove it once the sync has run.
	RenameFrom  string   `yaml:"rename_from,omitempty" json:"rename_from,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Role        string   `yaml:"role,omitempty" json:"role,omitempty"`
	IsPool      bool     `yaml:"is_pool,omitempty" json:"is_pool,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// slugify converts a string to a slug
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	return s
}
