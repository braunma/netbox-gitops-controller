// SPDX-License-Identifier: Apache-2.0

package export

import (
	"bytes"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// declaredIndex holds the identities the repository already declares, per
// kind, keyed the way the reconciler looks each kind up in NetBox.
type declaredIndex struct {
	keys map[string]map[string]bool
	// all makes has answer false for everything, so that declared objects
	// are exported too (Options.All). The slug maps below still apply.
	all bool
	// rackSlug maps "site/name" of a declared rack to the slug the YAML gave
	// it, which is what a device's `rack` has to say.
	rackSlug map[string]string
	// moduleTypeSlug maps "manufacturer/model" of a declared module type to
	// its slug.
	moduleTypeSlug map[string]string
}

func (d declaredIndex) has(kind, key string) bool { return !d.all && d.keys[kind][key] }

func indexDeclared(ds *loader.Dataset, all bool) declaredIndex {
	d := declaredIndex{
		all:            all,
		keys:           map[string]map[string]bool{},
		rackSlug:       map[string]string{},
		moduleTypeSlug: map[string]string{},
	}
	add := func(kind, key string) {
		if d.keys[kind] == nil {
			d.keys[kind] = map[string]bool{}
		}
		d.keys[kind][key] = true
	}
	if ds == nil {
		return d
	}
	for _, o := range ds.Tags {
		add(kindTag, o.Slug)
	}
	for _, o := range ds.Roles {
		add(kindRole, o.Slug)
	}
	for _, o := range ds.CustomFields {
		add(kindCustomField, o.Name)
	}
	for _, o := range ds.Platforms {
		add(kindPlatform, o.Slug)
	}
	for _, o := range ds.TenantGroups {
		add(kindTenantGroup, o.Slug)
	}
	for _, o := range ds.Tenants {
		add(kindTenant, o.Slug)
	}
	for _, o := range ds.Sites {
		add(kindSite, o.Slug)
	}
	for _, o := range ds.Racks {
		add(kindRack, o.SiteSlug+"/"+o.Name)
		d.rackSlug[o.SiteSlug+"/"+o.Name] = o.Slug
	}
	for _, o := range ds.VRFs {
		add(kindVRF, o.Name)
	}
	for _, o := range ds.VLANGroups {
		add(kindVLANGroup, o.Slug)
	}
	for _, o := range ds.VLANs {
		add(kindVLAN, o.SiteSlug+"/"+strconv.Itoa(o.VID))
	}
	for _, o := range ds.Prefixes {
		add(kindPrefix, o.Prefix)
	}
	for _, o := range ds.DeviceTypes {
		add(kindDeviceType, o.Slug)
	}
	for _, o := range ds.ModuleTypes {
		key := utils.Slugify(o.Manufacturer) + "/" + o.Model
		add(kindModuleType, key)
		d.moduleTypeSlug[key] = o.Slug
	}
	for _, o := range ds.ClusterTypes {
		add(kindClusterType, o.Slug)
	}
	for _, o := range ds.ClusterGroups {
		add(kindClusterGroup, o.Slug)
	}
	for _, o := range ds.Clusters {
		add(kindCluster, o.Name)
	}
	for _, o := range ds.Devices {
		add(kindDevice, o.SiteSlug+"/"+o.Name)
	}
	for _, o := range ds.VMs {
		add(kindVM, o.Name)
	}
	return d
}

// Kinds, as reported in Result counts.
const (
	kindTag          = "tag"
	kindRole         = "device role"
	kindCustomField  = "custom field"
	kindPlatform     = "platform"
	kindTenantGroup  = "tenant group"
	kindTenant       = "tenant"
	kindSite         = "site"
	kindRack         = "rack"
	kindVRF          = "VRF"
	kindVLANGroup    = "VLAN group"
	kindVLAN         = "VLAN"
	kindPrefix       = "prefix"
	kindDeviceType   = "device type"
	kindModuleType   = "module type"
	kindClusterType  = "cluster type"
	kindClusterGroup = "cluster group"
	kindCluster      = "cluster"
	kindDevice       = "device"
	kindVM           = "virtual machine"
)

// Kinds lists every kind in the order a summary should print them.
var Kinds = []string{
	kindTag, kindCustomField, kindRole, kindPlatform, kindTenantGroup, kindTenant,
	kindSite, kindRack, kindVRF, kindVLANGroup, kindVLAN, kindPrefix,
	kindModuleType, kindDeviceType, kindClusterType, kindClusterGroup, kindCluster,
	kindDevice, kindVM,
}

const fileHeader = `# Exported from NetBox by "netbox-gitops export".
#
# Every object here already exists in NetBox. Merging this file makes the
# controller adopt it: the next sync finds it under the same name or slug,
# stamps the gitops tag on it, and from then on manages it like any other
# object - including deleting it with --prune once it is removed from here.
#
# Review it before merging, and run "netbox-gitops --dry-run": for objects
# exported unchanged the plan should only add the gitops tag.
`

const referenceFileHeader = `# Reference export from NetBox by "netbox-gitops export --all".
#
# This file lists what NetBox holds, including objects the repository already
# declares. It is for comparing: copy what your own files are missing (a
# tenant, a description) into them. Do not merge it - every object here that
# the repository declares would be declared twice.
`

// render marshals a value as YAML with the export header and a two-space
// indent, the style of the hand-written files. reference selects the header
// of an Options.All export.
func render(v interface{}, extraHeader string, reference bool) ([]byte, error) {
	var buf bytes.Buffer
	if reference {
		buf.WriteString(referenceFileHeader)
	} else {
		buf.WriteString(fileHeader)
	}
	if extraHeader != "" {
		buf.WriteString("#\n")
		buf.WriteString(extraHeader)
	}
	buf.WriteString("\n")
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return nil, err
	}
	shortKeys(&node)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shortNames are the canonical keys that have a short form (see
// models.FieldAliases). Every model that has one of these keys accepts its
// short form, so they can be renamed wherever they occur; the hand-written
// files use the short forms.
var shortNames = map[string]string{
	"site_slug":        "site",
	"role_slug":        "role",
	"device_type_slug": "device_type",
	"rack_slug":        "rack",
	"module_type_slug": "module_type",
	"group_slug":       "group",
	"parent_slug":      "parent",
	"type_slug":        "type",
	"vrf_name":         "vrf",
	"vlan_name":        "vlan",
}

func shortKeys(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if short, ok := shortNames[n.Content[i].Value]; ok {
				n.Content[i].Value = short
			}
		}
	}
	for _, c := range n.Content {
		shortKeys(c)
	}
}
