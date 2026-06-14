package constants

// Managed tag constants
const (
	ManagedTagSlug        = "gitops"
	ManagedTagName        = "GitOps Managed"
	ManagedTagColor       = "4caf50"
	ManagedTagDescription = "Managed by GitOps Controller"
)

// Default values
const (
	DefaultCableType   = "cat6a"
	DefaultCableStatus = "connected"
	DefaultLengthUnit  = "m"
)

// Termination types
const (
	TerminationInterface = "dcim.interface"
	TerminationFrontPort = "dcim.frontport"
	TerminationRearPort  = "dcim.rearport"
)

// Assigned-object types for IP addresses
const (
	AssignedObjectInterface   = "dcim.interface"
	AssignedObjectVMInterface = "virtualization.vminterface"
)

// Endpoints
const (
	EndpointInterfaces = "interfaces"
	EndpointFrontPorts = "front_ports"
	EndpointRearPorts  = "rear_ports"
	EndpointModules    = "modules"
	EndpointCables     = "cables"
)

// Wait durations (milliseconds)
const (
	WaitAfterCableDelete = 100
	WaitAfterModuleDelete = 200
)

// Template endpoints (don't support tags)
var TemplateEndpoints = []string{
	"interface_templates",
	"front_port_templates",
	"rear_port_templates",
	"device_bay_templates",
	"module_bay_templates",
}

// UntaggableEndpoints are NetBox endpoints whose objects are not taggable, so
// the managed-tag must not be injected into their payloads (NetBox rejects an
// unknown `tags` field). These objects are therefore also not prunable by tag
// and are intentionally excluded from pruneTargets.
var UntaggableEndpoints = map[string]bool{
	"custom-fields": true,
}

// Field transforms for API calls
var FieldTransforms = map[string]string{
	"device_type_id": "device_type",
	"module_type_id": "module_type",
}

// Cache resource types
var CacheResourceTypes = []string{
	"sites",
	"roles",
	"device_types",
	"module_types",
	"racks",
	"vlans",
	"vrfs",
	"tags",
	"manufacturers",
	"platforms",
	"tenant_groups",
	"tenants",
	"cluster_types",
	"cluster_groups",
	"clusters",
}

// Cable color map
var CableColorMap = map[string]string{
	"cat6":   "f44336",
	"cat6a":  "ffeb3b",
	"cat7":   "ff9800",
	"dac":    "000000",
	"fiber":  "00bcd4",
	"om3":    "00bcd4",
	"om4":    "2196f3",
	"os2":    "9c27b0",
}
