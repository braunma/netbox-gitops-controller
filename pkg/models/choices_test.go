package models

import (
	"strings"
	"testing"
)

// TestValidationCatchesWhatNetBoxRejects covers values NetBox refuses with a
// 400 partway through a sync — after earlier objects are already written.
// Catching them in yamlcheck keeps the failure in the YAML.
func TestValidationCatchesWhatNetBoxRejects(t *testing.T) {
	tests := []struct {
		name    string
		model   Validator
		wantErr string
	}{
		{
			name:    "device status not a NetBox choice",
			model:   &DeviceConfig{Name: "d", SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r", Status: "runnning"},
			wantErr: "status \"runnning\" is not valid",
		},
		{
			name:    "device face not a NetBox choice",
			model:   &DeviceConfig{Name: "d", SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r", Face: "middle"},
			wantErr: "face \"middle\" is not valid",
		},
		{
			name:    "device name over NetBox's 64-character limit",
			model:   &DeviceConfig{Name: strings.Repeat("x", 65), SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r"},
			wantErr: "exceeding NetBox's limit of 64",
		},
		{
			name:    "serial over the 50-character limit",
			model:   &DeviceConfig{Name: "d", SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r", Serial: strings.Repeat("s", 51)},
			wantErr: "serial is 51 characters",
		},
		{
			name:    "site status uses a device status",
			model:   &Site{Name: "s", Slug: "s", Status: "offline"},
			wantErr: "status \"offline\" is not valid",
		},
		{
			name:    "rack status not a NetBox choice",
			model:   &Rack{Name: "r", SiteSlug: "s", Status: "occupied"},
			wantErr: "status \"occupied\" is not valid",
		},
		{
			name:    "vlan status not a NetBox choice",
			model:   &VLAN{Name: "v", VID: 10, SiteSlug: "s", Status: "planned"},
			wantErr: "status \"planned\" is not valid",
		},
		{
			name:    "prefix status not a NetBox choice",
			model:   &Prefix{Prefix: "10.0.0.0/8", Status: "assigned"},
			wantErr: "status \"assigned\" is not valid",
		},
		{
			name:    "device type airflow not a NetBox choice",
			model:   &DeviceType{Model: "m", Slug: "m", Manufacturer: "Dell", Airflow: "front-to-back"},
			wantErr: "airflow \"front-to-back\" is not valid",
		},
		{
			name:    "weight unit not a NetBox choice",
			model:   &DeviceType{Model: "m", Slug: "m", Manufacturer: "Dell", WeightUnit: "kilograms"},
			wantErr: "weight_unit \"kilograms\" is not valid",
		},
		{
			name:    "subdevice role not a NetBox choice",
			model:   &DeviceType{Model: "m", Slug: "m", Manufacturer: "Dell", SubdeviceRole: "chassis"},
			wantErr: "subdevice_role \"chassis\" is not valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.model.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidationAcceptsValidValues guards against the choice sets being so
// strict that real configurations are rejected.
func TestValidationAcceptsValidValues(t *testing.T) {
	valid := []Validator{
		// Omitting an optional choice is always fine; the reconcilers default it.
		&DeviceConfig{Name: "d", SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r"},
		&Site{Name: "s", Slug: "s"},
		&Rack{Name: "r", SiteSlug: "s"},
		// Every documented status for its own object type.
		&DeviceConfig{Name: "d", SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r", Status: "inventory", Face: "rear"},
		&Site{Name: "s", Slug: "s", Status: "decommissioning"},
		&Rack{Name: "r", SiteSlug: "s", Status: "available"},
		&VLAN{Name: "v", VID: 10, SiteSlug: "s", Status: "deprecated"},
		&Prefix{Prefix: "10.0.0.0/8", Status: "container"},
		&DeviceType{Model: "m", Slug: "m", Manufacturer: "Dell", Airflow: "passive", WeightUnit: "lb", SubdeviceRole: "child"},
		// A name of exactly the limit, in non-ASCII characters: length is
		// counted in runes, as NetBox counts them.
		&DeviceConfig{Name: strings.Repeat("ü", 64), SiteSlug: "s", DeviceTypeSlug: "t", RoleSlug: "r"},
	}
	for _, m := range valid {
		if err := m.Validate(); err != nil {
			t.Errorf("Validate() = %v, want it accepted", err)
		}
	}
}
