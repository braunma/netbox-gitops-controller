// SPDX-License-Identifier: Apache-2.0

package models

// InterfaceTemplate represents an interface template for device types
type InterfaceTemplate struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Type        string `yaml:"type" json:"type" validate:"required"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	MgmtOnly    bool   `yaml:"mgmt_only,omitempty" json:"mgmt_only,omitempty"`
	// Enabled is a pointer because NetBox defaults it to true: an omitted
	// value must mean "take the default", not "disabled".
	Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	PoEMode string `yaml:"poe_mode,omitempty" json:"poe_mode,omitempty"`
	PoEType string `yaml:"poe_type,omitempty" json:"poe_type,omitempty"`
	RFRole  string `yaml:"rf_role,omitempty" json:"rf_role,omitempty"`
	LAGName string `yaml:"lag_name,omitempty" json:"lag_name,omitempty"`
}

// PortTemplate represents a port template for patch panels (Front/Rear).
// Positions applies to rear ports (how many front ports may terminate on it),
// RearPortPosition to front ports (which position on the rear port it takes).
// Both default to 1, which is the single-position patch panel case.
type PortTemplate struct {
	Name             string `yaml:"name" json:"name" validate:"required"`
	Type             string `yaml:"type" json:"type" validate:"required"`
	Label            string `yaml:"label,omitempty" json:"label,omitempty"`
	Description      string `yaml:"description,omitempty" json:"description,omitempty"`
	Color            string `yaml:"color,omitempty" json:"color,omitempty"`
	RearPort         string `yaml:"rear_port,omitempty" json:"rear_port,omitempty"`
	Positions        int    `yaml:"positions,omitempty" json:"positions,omitempty"`
	RearPortPosition int    `yaml:"rear_port_position,omitempty" json:"rear_port_position,omitempty"`
}

// ConsolePortTemplate represents a console (server) port template. NetBox
// allows a console port to omit its type, so Type is not required here.
type ConsolePortTemplate struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Type        string `yaml:"type,omitempty" json:"type,omitempty"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// PowerPortTemplate represents a power inlet on a device type.
type PowerPortTemplate struct {
	Name          string `yaml:"name" json:"name" validate:"required"`
	Type          string `yaml:"type,omitempty" json:"type,omitempty"`
	Label         string `yaml:"label,omitempty" json:"label,omitempty"`
	Description   string `yaml:"description,omitempty" json:"description,omitempty"`
	MaximumDraw   int    `yaml:"maximum_draw,omitempty" json:"maximum_draw,omitempty"`
	AllocatedDraw int    `yaml:"allocated_draw,omitempty" json:"allocated_draw,omitempty"`
}

// PowerOutletTemplate represents a power outlet on a device type (e.g. on a
// PDU). PowerPort names the power port template it is fed from.
type PowerOutletTemplate struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Type        string `yaml:"type,omitempty" json:"type,omitempty"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	PowerPort   string `yaml:"power_port,omitempty" json:"power_port,omitempty"`
	FeedLeg     string `yaml:"feed_leg,omitempty" json:"feed_leg,omitempty"`
}

// ModuleBayTemplate represents a module bay template (e.g., for GPUs)
type ModuleBayTemplate struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Position    string `yaml:"position,omitempty" json:"position,omitempty"`
}

// DeviceBayTemplate represents a device bay template (e.g., for Chassis Nodes)
type DeviceBayTemplate struct {
	Name        string `yaml:"name" json:"name" validate:"required"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// ModuleType represents a blueprint for a module installed into a module bay
// (a line card, a NIC mezzanine, a transceiver, a PSU).
//
// A module type carries the same component templates as a device type, except
// device bays: NetBox instantiates them onto the device when the module is
// installed. Component names may contain the literal "{module}" placeholder,
// which NetBox substitutes with the bay position at installation time, so the
// same module type in two bays does not produce colliding interface names.
// Names are therefore passed through verbatim and never slugified.
type ModuleType struct {
	Model        string   `yaml:"model" json:"model" validate:"required"`
	Slug         string   `yaml:"slug,omitempty" json:"slug,omitempty"`
	Manufacturer string   `yaml:"manufacturer" json:"manufacturer" validate:"required"`
	PartNumber   string   `yaml:"part_number,omitempty" json:"part_number,omitempty"`
	Airflow      string   `yaml:"airflow,omitempty" json:"airflow,omitempty"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
	Comments     string   `yaml:"comments,omitempty" json:"comments,omitempty"`
	Weight       float64  `yaml:"weight,omitempty" json:"weight,omitempty"`
	WeightUnit   string   `yaml:"weight_unit,omitempty" json:"weight_unit,omitempty"`
	Tags         []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	Interfaces         []InterfaceTemplate   `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	FrontPorts         []PortTemplate        `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
	RearPorts          []PortTemplate        `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
	ConsolePorts       []ConsolePortTemplate `yaml:"console_ports,omitempty" json:"console_ports,omitempty"`
	ConsoleServerPorts []ConsolePortTemplate `yaml:"console_server_ports,omitempty" json:"console_server_ports,omitempty"`
	PowerPorts         []PowerPortTemplate   `yaml:"power_ports,omitempty" json:"power_ports,omitempty"`
	PowerOutlets       []PowerOutletTemplate `yaml:"power_outlets,omitempty" json:"power_outlets,omitempty"`
	ModuleBays         []ModuleBayTemplate   `yaml:"module_bays,omitempty" json:"module_bays,omitempty"`
}

// DeviceType represents a device type definition (blueprint for devices)
type DeviceType struct {
	Model        string `yaml:"model" json:"model" validate:"required"`
	Slug         string `yaml:"slug" json:"slug" validate:"required"`
	Manufacturer string `yaml:"manufacturer" json:"manufacturer" validate:"required"`
	PartNumber   string `yaml:"part_number,omitempty" json:"part_number,omitempty"`
	// UHeight is fractional in NetBox: half-height (0.5U) device types are
	// common in the community library, and child devices use 0.
	UHeight       float64  `yaml:"u_height,omitempty" json:"u_height,omitempty"`
	IsFullDepth   bool     `yaml:"is_full_depth,omitempty" json:"is_full_depth,omitempty"`
	SubdeviceRole string   `yaml:"subdevice_role,omitempty" json:"subdevice_role,omitempty"`
	Airflow       string   `yaml:"airflow,omitempty" json:"airflow,omitempty"`
	Description   string   `yaml:"description,omitempty" json:"description,omitempty"`
	Comments      string   `yaml:"comments,omitempty" json:"comments,omitempty"`
	Weight        float64  `yaml:"weight,omitempty" json:"weight,omitempty"`
	WeightUnit    string   `yaml:"weight_unit,omitempty" json:"weight_unit,omitempty"`
	Tags          []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	Interfaces         []InterfaceTemplate   `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	FrontPorts         []PortTemplate        `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
	RearPorts          []PortTemplate        `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
	ConsolePorts       []ConsolePortTemplate `yaml:"console_ports,omitempty" json:"console_ports,omitempty"`
	ConsoleServerPorts []ConsolePortTemplate `yaml:"console_server_ports,omitempty" json:"console_server_ports,omitempty"`
	PowerPorts         []PowerPortTemplate   `yaml:"power_ports,omitempty" json:"power_ports,omitempty"`
	PowerOutlets       []PowerOutletTemplate `yaml:"power_outlets,omitempty" json:"power_outlets,omitempty"`
	ModuleBays         []ModuleBayTemplate   `yaml:"module_bays,omitempty" json:"module_bays,omitempty"`
	DeviceBays         []DeviceBayTemplate   `yaml:"device_bays,omitempty" json:"device_bays,omitempty"`
}
