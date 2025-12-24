package models

// InterfaceTemplate represents an interface template for device types
type InterfaceTemplate struct {
	Name     string `yaml:"name" json:"name" validate:"required"`
	Type     string `yaml:"type" json:"type" validate:"required"`
	MgmtOnly bool   `yaml:"mgmt_only,omitempty" json:"mgmt_only,omitempty"`
	LAGName  string `yaml:"lag_name,omitempty" json:"lag_name,omitempty"`
}

// PortTemplate represents a port template for patch panels (Front/Rear)
type PortTemplate struct {
	Name     string `yaml:"name" json:"name" validate:"required"`
	Type     string `yaml:"type" json:"type" validate:"required"`
	RearPort string `yaml:"rear_port,omitempty" json:"rear_port,omitempty"`
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

// ModuleType represents a blueprint for a module (e.g., NVIDIA H200)
type ModuleType struct {
	Model        string   `yaml:"model" json:"model" validate:"required"`
	Slug         string   `yaml:"slug" json:"slug" validate:"required"`
	Manufacturer string   `yaml:"manufacturer" json:"manufacturer" validate:"required"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags         []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// DeviceType represents a device type definition (blueprint for devices)
type DeviceType struct {
	Model         string                  `yaml:"model" json:"model" validate:"required"`
	Slug          string                  `yaml:"slug" json:"slug" validate:"required"`
	Manufacturer  string                  `yaml:"manufacturer" json:"manufacturer" validate:"required"`
	UHeight       int                     `yaml:"u_height,omitempty" json:"u_height,omitempty"`
	IsFullDepth   bool                    `yaml:"is_full_depth,omitempty" json:"is_full_depth,omitempty"`
	SubdeviceRole string                  `yaml:"subdevice_role,omitempty" json:"subdevice_role,omitempty"`
	Tags          []string                `yaml:"tags,omitempty" json:"tags,omitempty"`
	Interfaces    []InterfaceTemplate     `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	FrontPorts    []PortTemplate          `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
	RearPorts     []PortTemplate          `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
	ModuleBays    []ModuleBayTemplate     `yaml:"module_bays,omitempty" json:"module_bays,omitempty"`
	DeviceBays    []DeviceBayTemplate     `yaml:"device_bays,omitempty" json:"device_bays,omitempty"`
}
