// SPDX-License-Identifier: Apache-2.0

package models

import (
	"strings"
	"testing"
)

// checkValidation asserts that err is nil when wantErrs is empty, and that
// the error message contains every expected substring otherwise.
func checkValidation(t *testing.T, err error, wantErrs []string) {
	t.Helper()
	if len(wantErrs) == 0 {
		if err != nil {
			t.Errorf("Validate() = %v, expected nil", err)
		}
		return
	}
	if err == nil {
		t.Errorf("Validate() = nil, expected error containing %v", wantErrs)
		return
	}
	for _, want := range wantErrs {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %q, expected it to contain %q", err.Error(), want)
		}
	}
}

func TestSiteValidate(t *testing.T) {
	tests := []struct {
		name     string
		site     Site
		wantErrs []string
	}{
		{"valid", Site{Name: "Berlin DC", Slug: "berlin-dc"}, nil},
		{"missing name", Site{Slug: "berlin-dc"}, []string{"name is required"}},
		{"missing slug", Site{Name: "Berlin DC"}, []string{`site "Berlin DC"`, "slug is required"}},
		{"all missing aggregates errors", Site{}, []string{`site "<unnamed>"`, "name is required", "slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.site.Validate(), tt.wantErrs)
		})
	}
}

func TestRackValidate(t *testing.T) {
	tests := []struct {
		name     string
		rack     Rack
		wantErrs []string
	}{
		{"valid", Rack{Name: "R01", SiteSlug: "berlin-dc"}, nil},
		{"missing site_slug", Rack{Name: "R01"}, []string{`rack "R01"`, "site_slug is required"}},
		{"missing name", Rack{SiteSlug: "berlin-dc"}, []string{"name is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.rack.Validate(), tt.wantErrs)
		})
	}
}

func TestRoleValidate(t *testing.T) {
	tests := []struct {
		name     string
		role     Role
		wantErrs []string
	}{
		{"valid", Role{Name: "Switch", Slug: "switch"}, nil},
		{"missing slug", Role{Name: "Switch"}, []string{"slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.role.Validate(), tt.wantErrs)
		})
	}
}

func TestTagValidate(t *testing.T) {
	tests := []struct {
		name     string
		tag      Tag
		wantErrs []string
	}{
		{"valid", Tag{Name: "managed", Slug: "managed"}, nil},
		{"missing name", Tag{Slug: "managed"}, []string{"name is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.tag.Validate(), tt.wantErrs)
		})
	}
}

func TestManufacturerValidate(t *testing.T) {
	tests := []struct {
		name         string
		manufacturer Manufacturer
		wantErrs     []string
	}{
		{"valid", Manufacturer{Name: "Cisco", Slug: "cisco"}, nil},
		{"missing slug", Manufacturer{Name: "Cisco"}, []string{"slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.manufacturer.Validate(), tt.wantErrs)
		})
	}
}

func TestVLANValidate(t *testing.T) {
	tests := []struct {
		name     string
		vlan     VLAN
		wantErrs []string
	}{
		{"valid", VLAN{Name: "mgmt", VID: 100, SiteSlug: "berlin-dc"}, nil},
		{"vid lower bound", VLAN{Name: "mgmt", VID: 1, SiteSlug: "berlin-dc"}, nil},
		{"vid upper bound", VLAN{Name: "mgmt", VID: 4094, SiteSlug: "berlin-dc"}, nil},
		{"vid zero", VLAN{Name: "mgmt", VID: 0, SiteSlug: "berlin-dc"}, []string{"vid must be between 1 and 4094, got 0"}},
		{"vid too large", VLAN{Name: "mgmt", VID: 4095, SiteSlug: "berlin-dc"}, []string{"vid must be between 1 and 4094, got 4095"}},
		{"missing site_slug", VLAN{Name: "mgmt", VID: 100}, []string{"site_slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.vlan.Validate(), tt.wantErrs)
		})
	}
}

func TestVLANGroupValidate(t *testing.T) {
	tests := []struct {
		name     string
		group    VLANGroup
		wantErrs []string
	}{
		{"valid", VLANGroup{Name: "Fabric", Slug: "fabric"}, nil},
		{"valid vid range", VLANGroup{Name: "Fabric", Slug: "fabric", MinVID: 100, MaxVID: 200}, nil},
		{"unset vid range ignored", VLANGroup{Name: "Fabric", Slug: "fabric", MinVID: 0, MaxVID: 0}, nil},
		{"min exceeds max", VLANGroup{Name: "Fabric", Slug: "fabric", MinVID: 300, MaxVID: 200}, []string{"min_vid (300) must not exceed max_vid (200)"}},
		{"missing slug", VLANGroup{Name: "Fabric"}, []string{"slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.group.Validate(), tt.wantErrs)
		})
	}
}

func TestVRFValidate(t *testing.T) {
	if err := (&VRF{Name: "prod"}).Validate(); err != nil {
		t.Errorf("Validate() = %v, expected nil", err)
	}
	checkValidation(t, (&VRF{}).Validate(), []string{`vrf "<unnamed>"`, "name is required"})
}

func TestPrefixValidate(t *testing.T) {
	if err := (&Prefix{Prefix: "10.0.0.0/24"}).Validate(); err != nil {
		t.Errorf("Validate() = %v, expected nil", err)
	}
	checkValidation(t, (&Prefix{}).Validate(), []string{"prefix is required"})
}

func TestModuleTypeValidate(t *testing.T) {
	tests := []struct {
		name     string
		mt       ModuleType
		wantErrs []string
	}{
		{"valid", ModuleType{Model: "H200", Manufacturer: "NVIDIA", Slug: "h200"}, nil},
		// Slug is intentionally optional: existing data identifies module types by model name.
		{"missing slug is allowed", ModuleType{Model: "H200", Manufacturer: "NVIDIA"}, nil},
		{"missing manufacturer", ModuleType{Model: "H200"}, []string{`module type "H200"`, "manufacturer is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.mt.Validate(), tt.wantErrs)
		})
	}
}

func TestDeviceTypeValidate(t *testing.T) {
	valid := DeviceType{Model: "C9300", Slug: "c9300", Manufacturer: "Cisco"}

	tests := []struct {
		name     string
		dt       DeviceType
		wantErrs []string
	}{
		{"valid bare", valid, nil},
		{
			"valid with templates",
			DeviceType{
				Model: "PP-24", Slug: "pp-24", Manufacturer: "Generic",
				Interfaces: []InterfaceTemplate{{Name: "eth0", Type: "1000base-t"}},
				FrontPorts: []PortTemplate{{Name: "fp1", Type: "lc", RearPort: "rp1"}},
				RearPorts:  []PortTemplate{{Name: "rp1", Type: "lc"}},
			},
			nil,
		},
		{"missing manufacturer", DeviceType{Model: "C9300", Slug: "c9300"}, []string{"manufacturer is required"}},
		{
			"interface without type",
			DeviceType{Model: "C9300", Slug: "c9300", Manufacturer: "Cisco",
				Interfaces: []InterfaceTemplate{{Name: "eth0"}}},
			[]string{`interface "eth0": type is required`},
		},
		{
			"interface without name",
			DeviceType{Model: "C9300", Slug: "c9300", Manufacturer: "Cisco",
				Interfaces: []InterfaceTemplate{{Type: "1000base-t"}, {}}},
			[]string{"interface 2: name is required"},
		},
		{
			"front port without rear_port",
			DeviceType{Model: "PP-24", Slug: "pp-24", Manufacturer: "Generic",
				FrontPorts: []PortTemplate{{Name: "fp1", Type: "lc"}}},
			[]string{`front port "fp1": rear_port is required`},
		},
		{
			"front port without name or type",
			DeviceType{Model: "PP-24", Slug: "pp-24", Manufacturer: "Generic",
				FrontPorts: []PortTemplate{{RearPort: "rp1"}}},
			[]string{"front port 1: name is required", "type is required"},
		},
		{
			"rear port without type",
			DeviceType{Model: "PP-24", Slug: "pp-24", Manufacturer: "Generic",
				RearPorts: []PortTemplate{{Name: "rp1"}}},
			[]string{`rear port "rp1": type is required`},
		},
		{
			"rear port without name",
			DeviceType{Model: "PP-24", Slug: "pp-24", Manufacturer: "Generic",
				RearPorts: []PortTemplate{{Type: "lc"}}},
			[]string{"rear port 1: name is required"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.dt.Validate(), tt.wantErrs)
		})
	}
}

func TestDeviceConfigValidate(t *testing.T) {
	base := DeviceConfig{
		Name:           "sw-01",
		SiteSlug:       "berlin-dc",
		DeviceTypeSlug: "c9300",
		RoleSlug:       "switch",
	}

	withFields := func(mutate func(d *DeviceConfig)) DeviceConfig {
		d := base
		mutate(&d)
		return d
	}

	tests := []struct {
		name     string
		device   DeviceConfig
		wantErrs []string
	}{
		{"valid minimal", base, nil},
		{"missing role_slug", withFields(func(d *DeviceConfig) { d.RoleSlug = "" }), []string{"role_slug is required"}},
		{"missing everything", DeviceConfig{}, []string{
			`device "<unnamed>"`,
			"name is required", "site_slug is required", "device_type_slug is required", "role_slug is required",
		}},
		{
			"parent_device without device_bay",
			withFields(func(d *DeviceConfig) { d.ParentDevice = "chassis-01" }),
			[]string{"device_bay is required when parent_device is set"},
		},
		{
			"device_bay without parent_device",
			withFields(func(d *DeviceConfig) { d.DeviceBay = "bay-1" }),
			[]string{"parent_device is required when device_bay is set"},
		},
		{
			"valid bay pairing",
			withFields(func(d *DeviceConfig) { d.ParentDevice = "chassis-01"; d.DeviceBay = "bay-1" }),
			nil,
		},
		{
			"valid interface with link and ip",
			withFields(func(d *DeviceConfig) {
				d.Interfaces = []InterfaceConfig{{
					Name: "eth0",
					Link: &LinkConfig{PeerDevice: "sw-02", PeerPort: "eth0"},
					IP:   &IPConfig{Address: "10.0.0.1/24"},
				}}
			}),
			nil,
		},
		{
			"interface without name skips further checks",
			withFields(func(d *DeviceConfig) {
				d.Interfaces = []InterfaceConfig{{Link: &LinkConfig{}}}
			}),
			[]string{"interface 1: name is required"},
		},
		{
			"interface link missing peer_device",
			withFields(func(d *DeviceConfig) {
				d.Interfaces = []InterfaceConfig{{Name: "eth0", Link: &LinkConfig{PeerPort: "eth0"}}}
			}),
			[]string{`interface "eth0": link.peer_device is required`},
		},
		{
			"interface link missing peer_port",
			withFields(func(d *DeviceConfig) {
				d.Interfaces = []InterfaceConfig{{Name: "eth0", Link: &LinkConfig{PeerDevice: "sw-02"}}}
			}),
			[]string{`interface "eth0": link.peer_port is required`},
		},
		{
			"interface ip without address",
			withFields(func(d *DeviceConfig) {
				d.Interfaces = []InterfaceConfig{{Name: "eth0", IP: &IPConfig{DNSName: "sw-01.example.com"}}}
			}),
			[]string{`interface "eth0": ip.address is required`},
		},
		{
			"front port missing rear_port",
			withFields(func(d *DeviceConfig) {
				d.FrontPorts = []FrontPortConfig{{Name: "fp1", Type: "lc"}}
			}),
			[]string{`front port "fp1": rear_port is required`},
		},
		{
			"front port missing name",
			withFields(func(d *DeviceConfig) {
				d.FrontPorts = []FrontPortConfig{{Type: "lc", RearPort: "rp1"}}
			}),
			[]string{"front port 1: name is required"},
		},
		{
			"front port link validated",
			withFields(func(d *DeviceConfig) {
				d.FrontPorts = []FrontPortConfig{{Name: "fp1", Type: "lc", RearPort: "rp1",
					Link: &LinkConfig{PeerDevice: "pp-02"}}}
			}),
			[]string{`front port "fp1": link.peer_port is required`},
		},
		{
			"rear port missing name",
			withFields(func(d *DeviceConfig) {
				d.RearPorts = []RearPortConfig{{Type: "lc"}}
			}),
			[]string{"rear port 1: name is required"},
		},
		{
			"rear port link validated",
			withFields(func(d *DeviceConfig) {
				d.RearPorts = []RearPortConfig{{Name: "rp1", Link: &LinkConfig{PeerPort: "rp1"}}}
			}),
			[]string{`rear port "rp1": link.peer_device is required`},
		},
		{
			"module missing module_type_slug",
			withFields(func(d *DeviceConfig) {
				d.Modules = []ModuleConfig{{Name: "gpu-1"}}
			}),
			[]string{`module "gpu-1": module_type_slug is required`},
		},
		{
			"module missing name",
			withFields(func(d *DeviceConfig) {
				d.Modules = []ModuleConfig{{ModuleTypeSlug: "h200"}}
			}),
			[]string{"module 1: name is required"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.device.Validate(), tt.wantErrs)
		})
	}
}

func TestValidateLink(t *testing.T) {
	if err := validateLink(nil); err != nil {
		t.Errorf("validateLink(nil) = %v, expected nil (links are optional)", err)
	}
	if err := validateLink(&LinkConfig{PeerDevice: "sw-02", PeerPort: "eth0"}); err != nil {
		t.Errorf("validateLink(valid) = %v, expected nil", err)
	}
}

func TestWrap(t *testing.T) {
	if err := wrap("site", "x", nil); err != nil {
		t.Errorf("wrap() with no errors = %v, expected nil", err)
	}
}

func TestPlatformValidate(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		wantErrs []string
	}{
		{"valid", Platform{Name: "Ubuntu 22.04", Slug: "ubuntu-22-04"}, nil},
		{"valid with manufacturer", Platform{Name: "IOS-XE", Slug: "ios-xe", Manufacturer: "cisco"}, nil},
		{"missing slug", Platform{Name: "Ubuntu 22.04"}, []string{`platform "Ubuntu 22.04"`, "slug is required"}},
		{"missing name", Platform{Slug: "ubuntu-22-04"}, []string{"name is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.platform.Validate(), tt.wantErrs)
		})
	}
}

func TestTenantGroupValidate(t *testing.T) {
	tests := []struct {
		name     string
		group    TenantGroup
		wantErrs []string
	}{
		{"valid", TenantGroup{Name: "Customers", Slug: "customers"}, nil},
		{"missing slug", TenantGroup{Name: "Customers"}, []string{`tenant group "Customers"`, "slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.group.Validate(), tt.wantErrs)
		})
	}
}

func TestTenantValidate(t *testing.T) {
	tests := []struct {
		name     string
		tenant   Tenant
		wantErrs []string
	}{
		{"valid", Tenant{Name: "Acme Corp", Slug: "acme-corp"}, nil},
		{"missing name", Tenant{Slug: "acme-corp"}, []string{"name is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.tenant.Validate(), tt.wantErrs)
		})
	}
}

func TestClusterTypeValidate(t *testing.T) {
	tests := []struct {
		name     string
		ct       ClusterType
		wantErrs []string
	}{
		{"valid", ClusterType{Name: "VMware vSphere", Slug: "vmware-vsphere"}, nil},
		{"missing slug", ClusterType{Name: "VMware vSphere"}, []string{`cluster type "VMware vSphere"`, "slug is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.ct.Validate(), tt.wantErrs)
		})
	}
}

func TestClusterGroupValidate(t *testing.T) {
	tests := []struct {
		name     string
		cg       ClusterGroup
		wantErrs []string
	}{
		{"valid", ClusterGroup{Name: "Production", Slug: "production"}, nil},
		{"missing name", ClusterGroup{Slug: "production"}, []string{"name is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.cg.Validate(), tt.wantErrs)
		})
	}
}

func TestClusterValidate(t *testing.T) {
	tests := []struct {
		name     string
		cluster  Cluster
		wantErrs []string
	}{
		{"valid", Cluster{Name: "prod-cluster", TypeSlug: "vmware-vsphere"}, nil},
		{"missing type_slug", Cluster{Name: "prod-cluster"}, []string{`cluster "prod-cluster"`, "type_slug is required"}},
		{"missing name", Cluster{TypeSlug: "vmware-vsphere"}, []string{"name is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.cluster.Validate(), tt.wantErrs)
		})
	}
}

func TestVMConfigValidate(t *testing.T) {
	tests := []struct {
		name     string
		vm       VMConfig
		wantErrs []string
	}{
		{"valid clustered", VMConfig{Name: "web-01", Cluster: "prod-cluster"}, nil},
		{"valid site-only", VMConfig{Name: "web-01", SiteSlug: "berlin-dc"}, nil},
		{"valid with both cluster and site", VMConfig{Name: "web-01", Cluster: "prod-cluster", SiteSlug: "berlin-dc"}, nil},
		{"missing name", VMConfig{Cluster: "prod-cluster"}, []string{"name is required"}},
		{
			"neither cluster nor site",
			VMConfig{Name: "web-01"},
			[]string{`virtual machine "web-01"`, "one of cluster or site_slug is required"},
		},
		{
			"interface without name",
			VMConfig{Name: "web-01", Cluster: "prod-cluster", Interfaces: []VMInterfaceConfig{{}}},
			[]string{"interface 1: name is required"},
		},
		{
			"interface ip without address",
			VMConfig{Name: "web-01", Cluster: "prod-cluster",
				Interfaces: []VMInterfaceConfig{{Name: "eth0", IP: &IPConfig{DNSName: "web-01.example.com"}}}},
			[]string{`interface "eth0": ip.address is required`},
		},
		{
			"valid interface with ip",
			VMConfig{Name: "web-01", Cluster: "prod-cluster",
				Interfaces: []VMInterfaceConfig{{Name: "eth0", IP: &IPConfig{Address: "10.0.0.10/24"}}}},
			nil,
		},
		{
			"provision without vmid/template/node",
			VMConfig{Name: "web-01", Cluster: "prod-cluster", Provision: true},
			[]string{
				"vmid is required when provision is true",
				"vm_template_id is required when provision is true",
				"node is required when provision is true",
			},
		},
		{
			"valid provisioned vm",
			VMConfig{Name: "web-01", Cluster: "prod-cluster", Provision: true,
				VMID: 101, VMTemplateID: 800, Node: "pve-01"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.vm.Validate(), tt.wantErrs)
		})
	}
}

func TestCustomFieldValidate(t *testing.T) {
	tests := []struct {
		name     string
		cf       CustomField
		wantErrs []string
	}{
		{
			"valid",
			CustomField{Name: "vmid", Type: "integer", ObjectTypes: []string{"virtualization.virtualmachine"}},
			nil,
		},
		{"missing name", CustomField{Type: "integer", ObjectTypes: []string{"virtualization.virtualmachine"}}, []string{"name is required"}},
		{"missing type", CustomField{Name: "vmid", ObjectTypes: []string{"virtualization.virtualmachine"}}, []string{"type is required"}},
		{
			"missing object_types",
			CustomField{Name: "vmid", Type: "integer"},
			[]string{`custom field "vmid"`, "object_types must list at least one content type"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValidation(t, tt.cf.Validate(), tt.wantErrs)
		})
	}
}
