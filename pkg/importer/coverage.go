// SPDX-License-Identifier: Apache-2.0

package importer

// fieldRule records how the importer treats one model field, so the round-trip
// decision for every field is written down in one place and checked. The point
// is fail-closed coverage: a field added to a model next year must be given a
// rule here, or the coverage test fails — it cannot silently get the wrong
// default. The rules follow the reconciler's payload construction, not NetBox's
// OPTIONS defaults (see the u_height=1 hazard: the sync always sends u_height,
// so the importer emits its real value and never omits it against a default).
type fieldRule int

const (
	// ruleEmit: the importer maps the field and emits its value.
	ruleEmit fieldRule = iota
	// ruleIdentity: an identity key, always present, never hoisted into defaults.
	ruleIdentity
	// ruleOmitDefault: emitted only when it differs from the template/NetBox
	// default (a pointer field like an interface's `enabled`).
	ruleOmitDefault
	// ruleNever: intentionally never emitted (rename_from is a correction
	// directive; provision is a removed key kept only to reject old files).
	ruleNever
	// ruleNotModeled: NetBox has no home for this field, so it cannot round-trip
	// and is not emitted (a VM's node is documentation-only).
	ruleNotModeled
)

// fieldCoverage is the per-struct, per-field rule table. Every yaml field of
// every model the importer writes must appear here; TestFieldCoverageIsComplete
// enforces that and rejects stale entries.
var fieldCoverage = map[string]map[string]fieldRule{
	"Site": {
		"name": ruleIdentity, "slug": ruleIdentity, "rename_from": ruleNever,
		"status": ruleEmit, "region": ruleEmit, "time_zone": ruleEmit,
		"description": ruleEmit, "comments": ruleEmit, "tags": ruleEmit,
	},
	"Rack": {
		"name": ruleIdentity, "slug": ruleEmit, "site_slug": ruleEmit,
		"rename_from": ruleNever, "status": ruleEmit, "width": ruleEmit,
		"u_height": ruleEmit, "description": ruleEmit, "tags": ruleEmit,
	},
	"Role": {
		"name": ruleIdentity, "slug": ruleIdentity, "color": ruleEmit,
		"rename_from": ruleNever, "vm_role": ruleEmit, "description": ruleEmit,
	},
	"Tag": {
		"name": ruleIdentity, "slug": ruleIdentity, "color": ruleEmit,
		"rename_from": ruleNever, "description": ruleEmit,
	},
	"Platform": {
		"name": ruleIdentity, "slug": ruleIdentity, "manufacturer": ruleEmit,
		"rename_from": ruleNever, "description": ruleEmit, "tags": ruleEmit,
	},
	"TenantGroup": {
		"name": ruleIdentity, "slug": ruleIdentity, "parent_slug": ruleEmit,
		"rename_from": ruleNever, "description": ruleEmit, "tags": ruleEmit,
	},
	"Tenant": {
		"name": ruleIdentity, "slug": ruleIdentity, "group_slug": ruleEmit,
		"rename_from": ruleNever, "description": ruleEmit, "tags": ruleEmit,
	},
	"CustomField": {
		"name": ruleIdentity, "type": ruleEmit, "object_types": ruleEmit,
		"rename_from": ruleNever, "label": ruleEmit, "description": ruleEmit,
		"required": ruleEmit,
	},
	"VRF": {
		"name": ruleIdentity, "rd": ruleEmit, "rename_from": ruleNever,
		"description": ruleEmit, "enforce_unique": ruleEmit, "tags": ruleEmit,
	},
	"VLAN": {
		"name": ruleEmit, "vid": ruleIdentity, "site_slug": ruleEmit,
		"group_slug": ruleEmit, "rename_from": ruleNever, "status": ruleEmit,
		"role": ruleEmit, "description": ruleEmit, "tags": ruleEmit,
	},
	"VLANGroup": {
		"name": ruleIdentity, "slug": ruleIdentity, "site_slug": ruleEmit,
		"rename_from": ruleNever, "description": ruleEmit, "min_vid": ruleEmit,
		"max_vid": ruleEmit, "tags": ruleEmit,
	},
	"Prefix": {
		"prefix": ruleIdentity, "site_slug": ruleEmit, "vrf_name": ruleEmit,
		"vlan_name": ruleEmit, "rename_from": ruleNever, "status": ruleEmit,
		"role": ruleEmit, "is_pool": ruleEmit, "description": ruleEmit, "tags": ruleEmit,
	},
	"DeviceType": {
		"model": ruleIdentity, "slug": ruleIdentity, "manufacturer": ruleEmit,
		"rename_from": ruleNever, "part_number": ruleEmit, "u_height": ruleEmit,
		"is_full_depth": ruleEmit, "subdevice_role": ruleEmit, "airflow": ruleEmit,
		"description": ruleEmit, "comments": ruleEmit, "weight": ruleEmit,
		"weight_unit": ruleEmit, "tags": ruleEmit, "interfaces": ruleEmit,
		"front_ports": ruleEmit, "rear_ports": ruleEmit, "console_ports": ruleEmit,
		"console_server_ports": ruleEmit, "power_ports": ruleEmit,
		"power_outlets": ruleEmit, "module_bays": ruleEmit, "device_bays": ruleEmit,
	},
	"ModuleType": {
		"model": ruleIdentity, "slug": ruleEmit, "manufacturer": ruleEmit,
		"rename_from": ruleNever, "part_number": ruleEmit, "airflow": ruleEmit,
		"description": ruleEmit, "comments": ruleEmit, "weight": ruleEmit,
		"weight_unit": ruleEmit, "tags": ruleEmit, "interfaces": ruleEmit,
		"front_ports": ruleEmit, "rear_ports": ruleEmit, "console_ports": ruleEmit,
		"console_server_ports": ruleEmit, "power_ports": ruleEmit,
		"power_outlets": ruleEmit, "module_bays": ruleEmit,
	},
	"ClusterType":  {"name": ruleIdentity, "slug": ruleIdentity, "rename_from": ruleNever, "description": ruleEmit, "tags": ruleEmit},
	"ClusterGroup": {"name": ruleIdentity, "slug": ruleIdentity, "rename_from": ruleNever, "description": ruleEmit, "tags": ruleEmit},
	"Cluster": {
		"name": ruleIdentity, "type_slug": ruleEmit, "group_slug": ruleEmit,
		"rename_from": ruleNever, "site_slug": ruleEmit, "tenant": ruleEmit,
		"status": ruleEmit, "description": ruleEmit, "tags": ruleEmit,
	},
	"DeviceConfig": {
		"name": ruleIdentity, "site_slug": ruleEmit, "rename_from": ruleNever,
		"device_type_slug": ruleEmit, "role_slug": ruleEmit, "rack_slug": ruleEmit,
		"position": ruleEmit, "face": ruleEmit, "parent_device": ruleEmit,
		"device_bay": ruleEmit, "status": ruleEmit, "serial": ruleEmit,
		"asset_tag": ruleEmit, "custom_fields": ruleEmit, "tags": ruleEmit,
		"modules": ruleEmit, "interfaces": ruleEmit, "front_ports": ruleEmit,
		"rear_ports": ruleEmit,
	},
	"InterfaceConfig": {
		"name": ruleIdentity, "type": ruleOmitDefault, "rename_from": ruleNever,
		"enabled": ruleOmitDefault, "label": ruleEmit, "description": ruleEmit,
		"mtu": ruleEmit, "link": ruleEmit, "mode": ruleEmit,
		"untagged_vlan": ruleEmit, "tagged_vlans": ruleEmit, "ip": ruleEmit,
		"address_role": ruleEmit, "members": ruleEmit, "tags": ruleEmit,
	},
	"VMConfig": {
		"name": ruleIdentity, "rename_from": ruleNever, "provision": ruleNever,
		"vmid": ruleEmit, "vm_template_id": ruleEmit, "node": ruleNotModeled,
		"cluster": ruleEmit, "site_slug": ruleEmit, "role_slug": ruleEmit,
		"platform": ruleEmit, "tenant": ruleEmit, "status": ruleEmit,
		"vcpus": ruleEmit, "memory": ruleEmit, "disk": ruleEmit, "tags": ruleEmit,
		"interfaces": ruleEmit,
	},
	"VMInterfaceConfig": {
		"name": ruleIdentity, "rename_from": ruleNever, "enabled": ruleOmitDefault,
		"description": ruleEmit, "mtu": ruleEmit, "mac_address": ruleEmit,
		"mode": ruleEmit, "untagged_vlan": ruleEmit, "tagged_vlans": ruleEmit,
		"parent": ruleEmit, "ip": ruleEmit, "address_role": ruleEmit, "tags": ruleEmit,
	},
}
