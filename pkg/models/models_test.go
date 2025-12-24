package models

import (
	"testing"
)

func TestVRFSlug(t *testing.T) {
	tests := []struct {
		name     string
		vrfName  string
		expected string
	}{
		{
			name:     "simple name",
			vrfName:  "production",
			expected: "production",
		},
		{
			name:     "name with spaces",
			vrfName:  "Production VRF",
			expected: "production-vrf",
		},
		{
			name:     "uppercase",
			vrfName:  "MANAGEMENT",
			expected: "management",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vrf := &VRF{Name: tt.vrfName}
			result := vrf.Slug()
			if result != tt.expected {
				t.Errorf("VRF.Slug() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestDeviceConfigSlug(t *testing.T) {
	tests := []struct {
		name       string
		deviceName string
		expected   string
	}{
		{
			name:       "simple name",
			deviceName: "server01",
			expected:   "server01",
		},
		{
			name:       "name with hyphens",
			deviceName: "web-server-01",
			expected:   "web-server-01",
		},
		{
			name:       "name with spaces",
			deviceName: "Web Server 01",
			expected:   "web-server-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := &DeviceConfig{Name: tt.deviceName}
			result := device.Slug()
			if result != tt.expected {
				t.Errorf("DeviceConfig.Slug() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestSiteModel(t *testing.T) {
	site := Site{
		Name:     "Berlin DC",
		Slug:     "berlin-dc",
		Status:   "active",
		TimeZone: "Europe/Berlin",
	}

	if site.Name != "Berlin DC" {
		t.Errorf("Site.Name = %q, expected %q", site.Name, "Berlin DC")
	}

	if site.Slug != "berlin-dc" {
		t.Errorf("Site.Slug = %q, expected %q", site.Slug, "berlin-dc")
	}
}

func TestVLANModel(t *testing.T) {
	vlan := VLAN{
		Name:     "Management",
		VID:      100,
		SiteSlug: "berlin-dc",
		Status:   "active",
	}

	if vlan.VID != 100 {
		t.Errorf("VLAN.VID = %d, expected %d", vlan.VID, 100)
	}
}

func TestDeviceTypeModel(t *testing.T) {
	dt := DeviceType{
		Model:        "PowerEdge R740",
		Slug:         "poweredge-r740",
		Manufacturer: "Dell",
		UHeight:      2,
		IsFullDepth:  true,
	}

	if dt.UHeight != 2 {
		t.Errorf("DeviceType.UHeight = %d, expected %d", dt.UHeight, 2)
	}

	if !dt.IsFullDepth {
		t.Errorf("DeviceType.IsFullDepth = %v, expected %v", dt.IsFullDepth, true)
	}
}

func TestLinkConfig(t *testing.T) {
	link := LinkConfig{
		PeerDevice: "switch-01",
		PeerPort:   "Eth1/1",
		CableType:  "cat6a",
		Length:     2.5,
		LengthUnit: "m",
	}

	if link.PeerDevice != "switch-01" {
		t.Errorf("LinkConfig.PeerDevice = %q, expected %q", link.PeerDevice, "switch-01")
	}

	if link.Length != 2.5 {
		t.Errorf("LinkConfig.Length = %f, expected %f", link.Length, 2.5)
	}
}
