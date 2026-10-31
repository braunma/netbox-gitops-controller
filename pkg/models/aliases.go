// SPDX-License-Identifier: Apache-2.0

package models

// FieldAliases is implemented by models that accept a short name for some of
// their YAML keys. YAMLAliases maps each short key to the canonical key it
// stands for; the loader rewrites the short form before decoding, so the rest
// of the code only ever sees the canonical field.
//
// A short name is only listed where it is free: a model whose own fields
// already use the short name (a prefix's `role`, an interface's `type`) keeps
// its meaning, and a mapping that spells both forms of one field is rejected
// rather than one silently winning.
type FieldAliases interface {
	YAMLAliases() map[string]string
}

// YAMLAliases implements FieldAliases.
func (DeviceConfig) YAMLAliases() map[string]string {
	return map[string]string{
		"site":        "site_slug",
		"role":        "role_slug",
		"device_type": "device_type_slug",
		"rack":        "rack_slug",
	}
}

// YAMLAliases implements FieldAliases.
func (ModuleConfig) YAMLAliases() map[string]string {
	return map[string]string{"module_type": "module_type_slug"}
}

// YAMLAliases implements FieldAliases.
func (Rack) YAMLAliases() map[string]string {
	return map[string]string{"site": "site_slug"}
}

// YAMLAliases implements FieldAliases.
func (TenantGroup) YAMLAliases() map[string]string {
	return map[string]string{"parent": "parent_slug"}
}

// YAMLAliases implements FieldAliases.
func (Tenant) YAMLAliases() map[string]string {
	return map[string]string{"group": "group_slug"}
}

// YAMLAliases implements FieldAliases.
func (VLAN) YAMLAliases() map[string]string {
	return map[string]string{
		"site":  "site_slug",
		"group": "group_slug",
	}
}

// YAMLAliases implements FieldAliases.
func (VLANGroup) YAMLAliases() map[string]string {
	return map[string]string{"site": "site_slug"}
}

// YAMLAliases implements FieldAliases.
func (Prefix) YAMLAliases() map[string]string {
	return map[string]string{
		"site": "site_slug",
		"vrf":  "vrf_name",
		"vlan": "vlan_name",
	}
}

// YAMLAliases implements FieldAliases.
func (Cluster) YAMLAliases() map[string]string {
	return map[string]string{
		"type":  "type_slug",
		"group": "group_slug",
		"site":  "site_slug",
	}
}

// YAMLAliases implements FieldAliases.
func (VMConfig) YAMLAliases() map[string]string {
	return map[string]string{
		"site": "site_slug",
		"role": "role_slug",
	}
}
