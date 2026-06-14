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
	WaitAfterCableDelete  = 100
	WaitAfterModuleDelete = 200
)

// UntaggableEndpoints are NetBox endpoints whose objects are not taggable, so
// the managed tag must not be injected into their payloads. These fall into two
// groups:
//
//   - Objects NetBox rejects an unknown `tags` field on (e.g. custom-fields).
//   - Component templates and the tag objects themselves, which silently accept
//     but never store/return a `tags` field. Injecting one there makes every
//     reconcile see a phantom diff — the fetched object never reports the tag
//     back, so `tags` looks changed and the object is re-PATCHed on every run
//     (the change is invisible in the diff output because `tags` is hidden).
//
// Endpoint keys use the hyphenated form passed to Apply (e.g. "rear-port-templates").
// These objects are also not prunable by tag and are intentionally excluded
// from pruneTargets.
var UntaggableEndpoints = map[string]bool{
	"custom-fields":        true,
	"tags":                 true,
	"interface-templates":  true,
	"front-port-templates": true,
	"rear-port-templates":  true,
	"device-bay-templates": true,
	"module-bay-templates": true,
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
	"cat6":  "f44336",
	"cat6a": "ffeb3b",
	"cat7":  "ff9800",
	"dac":   "000000",
	"fiber": "00bcd4",
	"om3":   "00bcd4",
	"om4":   "2196f3",
	"os2":   "9c27b0",
}
