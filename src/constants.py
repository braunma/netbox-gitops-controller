"""
Central configuration and constants for NetBox GitOps Controller.
This module contains all magic strings, default values, and configuration
that should be easily accessible and modifiable.
"""

from typing import Final

# ============================================================================
# TAG CONFIGURATION
# ============================================================================
# GitOps managed tag configuration
# Single Source of Truth: NetBoxClient._ensure_tag() creates/verifies this tag
# All syncers receive the tag ID (no duplicate creation logic)
MANAGED_TAG_SLUG: Final[str] = "gitops"
MANAGED_TAG_NAME: Final[str] = "GitOps Managed"
MANAGED_TAG_COLOR: Final[str] = "00bcd4"  # Cyan
MANAGED_TAG_DESCRIPTION: Final[str] = "Automatically managed by NetBox GitOps Controller"

# ============================================================================
# DEVICE ROLES
# ============================================================================
ROLE_PATCH_PANEL: Final[str] = "patch-panel"

# ============================================================================
# NETBOX TERMINATION TYPES
# ============================================================================
TERMINATION_INTERFACE: Final[str] = "dcim.interface"
TERMINATION_FRONT_PORT: Final[str] = "dcim.frontport"
TERMINATION_REAR_PORT: Final[str] = "dcim.rearport"

# ============================================================================
# NETBOX API ENDPOINTS
# ============================================================================
ENDPOINT_INTERFACES: Final[str] = "interfaces"
ENDPOINT_FRONT_PORTS: Final[str] = "front_ports"
ENDPOINT_REAR_PORTS: Final[str] = "rear_ports"
ENDPOINT_CABLES: Final[str] = "cables"
ENDPOINT_DEVICES: Final[str] = "devices"
ENDPOINT_MODULES: Final[str] = "modules"
ENDPOINT_MODULE_BAYS: Final[str] = "module_bays"
ENDPOINT_DEVICE_BAYS: Final[str] = "device_bays"
ENDPOINT_DEVICE_BAY_TEMPLATES: Final[str] = "device_bay_templates"

# Template endpoints (do not support tags)
TEMPLATE_ENDPOINTS: Final[frozenset] = frozenset([
    "interface_templates",
    "front_port_templates",
    "rear_port_templates",
    "power_port_templates",
    "module_bay_templates",
    "device_bay_templates",
])

# ============================================================================
# CABLE CONFIGURATION
# ============================================================================
DEFAULT_CABLE_TYPE: Final[str] = "cat6a"
DEFAULT_CABLE_STATUS: Final[str] = "connected"
DEFAULT_LENGTH_UNIT: Final[str] = "m"

# Cable color mapping (name -> hex)
CABLE_COLOR_MAP: Final[dict] = {
    'purple': '800080',
    'blue': '0000ff',
    'yellow': 'ffff00',
    'red': 'ff0000',
    'white': 'ffffff',
    'black': '000000',
    'gray': '808080',
    'grey': '808080',
    'orange': 'ffa500',
    'green': '008000',
}

# ============================================================================
# TIMING CONFIGURATION
# ============================================================================
# Wait times for API operations (in seconds)
WAIT_AFTER_DELETE: Final[float] = 1.0
WAIT_AFTER_MODULE_DELETE: Final[float] = 0.1
WAIT_AFTER_CABLE_DELETE: Final[float] = 1.0

# Retry configuration
MAX_RETRIES: Final[int] = 3
RETRY_BACKOFF_BASE: Final[float] = 2.0  # Exponential backoff base

# ============================================================================
# CACHE CONFIGURATION
# ============================================================================
CACHE_RESOURCE_TYPES: Final[frozenset] = frozenset([
    'sites',
    'roles',
    'device_types',
    'racks',
    'vlans',
    'vrfs',
    'tags',
    'module_types',
    'manufacturers',
])

# ============================================================================
# DEFAULT VALUES
# ============================================================================
DEFAULT_RACK_HEIGHT: Final[int] = 42
DEFAULT_TIMEZONE: Final[str] = "UTC"
DEFAULT_STATUS: Final[str] = "active"
DEFAULT_DEVICE_FACE: Final[str] = "front"

# ============================================================================
# FIELD NAME TRANSFORMATIONS
# ============================================================================
# NetBox API expects different field names for create vs filter operations
FIELD_TRANSFORMS: Final[dict] = {
    'device_type_id': 'device_type',
}

# ============================================================================
# LOGGING CONFIGURATION
# ============================================================================
LOG_PREFIX_CABLE: Final[str] = "[CABLE]"
LOG_PREFIX_MODULE: Final[str] = "[MODULE]"
LOG_PREFIX_BAYS: Final[str] = "[BAYS]"
LOG_PREFIX_DRY_RUN: Final[str] = "[DRY]"

# ============================================================================
# VALIDATION
# ============================================================================
MIN_VLAN_ID: Final[int] = 1
MAX_VLAN_ID: Final[int] = 4094
