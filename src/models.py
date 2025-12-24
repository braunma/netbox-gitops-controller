from typing import Optional, Literal, List, Union
from pydantic import BaseModel, Field


# =========================================================
# FOUNDATION MODELS (Sites, Racks, Tags, etc.)
# =========================================================

class TagModel(BaseModel):
    name: str
    slug: str
    color: str = "9e9e9e"
    description: str = ""

class RoleModel(BaseModel):
    name: str
    slug: str
    color: str = "9e9e9e"
    vm_role: bool = False

class SiteModel(BaseModel):
    name: str
    slug: str
    status: str = "active"
    time_zone: str = "UTC"
    tags: List[str] = []

class RackModel(BaseModel):
    name: str
    slug: str
    site_slug: str
    u_height: int = 42
    status: str = "active"
    tags: List[str] = []


# =========================================================
# NETWORK MODELS (VLANs, VRFs, Prefixes)
# =========================================================

class VRFModel(BaseModel):
    name: str
    rd: Optional[str] = None
    description: str = ""
    enforce_unique: bool = True
    tags: List[str] = []
    
    @property
    def slug(self):
        return self.name.lower().replace(" ", "-")

class VlanGroupModel(BaseModel):
    name: str
    slug: str
    site_slug: Optional[str] = None
    description: str = ""
    min_vid: int = 1
    max_vid: int = 4094
    tags: List[str] = []

class VlanModel(BaseModel):
    vid: int
    name: str
    site_slug: str
    status: str = "active"
    group_slug: Optional[str] = None
    description: str = ""
    tags: List[str] = []

class PrefixModel(BaseModel):
    prefix: str
    description: str = ""
    site_slug: Optional[str] = None
    vlan_name: Optional[str] = None
    vrf_name: Optional[str] = None
    is_pool: bool = False
    status: Literal["container", "active", "reserved", "deprecated"] = "active"
    tags: List[str] = []


# =========================================================
# DEVICE TYPE TEMPLATES (Baupläne)
# =========================================================

class InterfaceTemplateModel(BaseModel):
    """Interface Template for Device Types."""
    name: str
    type: str
    mgmt_only: bool = False
    lag_name: Optional[str] = None

class PortTemplateModel(BaseModel):
    """Port Template for patch panels (Front/Rear)."""
    name: str
    type: str = "8p8c"
    rear_port: Optional[str] = None

class ModuleBayTemplateModel(BaseModel):
    """Module Bay Template (e.g., for GPUs)."""
    name: str
    label: Optional[str] = None
    description: Optional[str] = None
    position: Optional[str] = None

class DeviceBayTemplateConfig(BaseModel):
    """Device Bay Template (e.g., for Chassis Nodes)."""
    name: str
    label: Optional[str] = None
    description: Optional[str] = None

class ModuleTypeModel(BaseModel):
    """Blueprint for a module (e.g., NVIDIA H200)."""
    model: str           # Display name
    slug: str            # Unique ID
    manufacturer: str    # Manufacturer slug
    description: Optional[str] = None
    tags: List[str] = []

class DeviceTypeModel(BaseModel):
    """Device Type Definition (Blueprint for devices)."""
    model: str
    slug: str
    manufacturer: str
    u_height: int = 1
    is_full_depth: bool = True
    subdevice_role: Optional[Literal["parent", "child"]] = None
    
    tags: List[str] = []
    interfaces: List[InterfaceTemplateModel] = []
    front_ports: List[PortTemplateModel] = []
    rear_ports: List[PortTemplateModel] = []
    module_bays: List[ModuleBayTemplateModel] = []
    device_bays: List[DeviceBayTemplateConfig] = []


# =========================================================
# DEVICE INSTANCE MODELS (Controller - Konkrete Geräte)
# =========================================================

class LinkConfig(BaseModel):
    """Cable connection definition."""
    peer_device: str
    peer_port: str
    cable_type: Optional[str] = "cat6a"
    color: Optional[str] = None
    length: Optional[float] = None
    length_unit: Optional[str] = 'm'

class IPConfig(BaseModel):
    """IP-Adresse Konfiguration."""
    address: str  # CIDR (z.B. 10.0.0.1/24)
    dns_name: Optional[str] = None
    description: Optional[str] = None
    status: Literal["active", "reserved", "dhcp", "deprecated"] = "active"
    tags: List[Union[str, int]] = Field(default_factory=list)
    vrf: Optional[str] = None
    address_role: Optional[str] = None

class InterfaceConfig(BaseModel):
    """Interface Configuration (for concrete devices)."""
    name: str
    type: str = "1000base-t"
    enabled: bool = True
    label: Optional[str] = None
    description: Optional[str] = None
    mtu: Optional[int] = None
    
    # Layer 1: Verkabelung
    link: Optional[LinkConfig] = None
    
    # Layer 2: Switching
    mode: Optional[Literal["access", "tagged", "tagged-all"]] = None
    untagged_vlan: Optional[str] = None
    tagged_vlans: List[str] = Field(default_factory=list)
    
    # Layer 3: IP
    ip: Optional[IPConfig] = None
    address_role: Literal["primary", "secondary"] = "secondary"
    
    # LAG
    members: List[str] = Field(default_factory=list)
    tags: List[Union[str, int]] = Field(default_factory=list)

class RearPortConfig(BaseModel):
    """Rear Port Konfiguration (Backbone)."""
    name: str
    type: str = 'lc'
    label: Optional[str] = None
    positions: int = 1
    description: Optional[str] = None
    tags: List[Union[str, int]] = Field(default_factory=list)
    link: Optional[LinkConfig] = None

class FrontPortConfig(BaseModel):
    """Front Port Konfiguration (Patch)."""
    name: str
    type: str
    label: Optional[str] = None
    description: Optional[str] = None
    tags: List[Union[str, int]] = Field(default_factory=list)

    # Mapping to rear port
    rear_port: str
    rear_port_position: int = 1

    # Optional: Direct patching
    link: Optional[LinkConfig] = None

class ModuleConfig(BaseModel):
    """Module configuration (e.g., installed GPU)."""
    name: str                   # Must match the bay name
    module_type_slug: str       # Reference to module type blueprint
    status: str = "active"
    serial: Optional[str] = None
    asset_tag: Optional[str] = None
    description: Optional[str] = None
    tags: List[Union[str, int]] = Field(default_factory=list)

class DeviceConfig(BaseModel):
    """Device Konfiguration (konkretes Gerät)."""
    name: str
    site_slug: str
    device_type_slug: str
    role_slug: str
    
    @property
    def slug(self):
        return self.name.lower().replace(" ", "-")
    
    # RACK LOCATION (normale Geräte)
    rack_slug: Optional[str] = None
    position: Optional[int] = None
    face: Literal["front", "rear"] = "front"
    
    # CHASSIS LOCATION (Blade/Node Geräte)
    parent_device: Optional[str] = None
    device_bay: Optional[str] = None
    
    # METADATA
    status: str = "active"
    serial: Optional[str] = None
    asset_tag: Optional[str] = None
    tags: List[Union[str, int]] = Field(default_factory=list)
    
    # COMPONENTS
    modules: List[ModuleConfig] = Field(default_factory=list)
    interfaces: List[InterfaceConfig] = Field(default_factory=list)
    front_ports: List[FrontPortConfig] = Field(default_factory=list)
    rear_ports: List[RearPortConfig] = Field(default_factory=list)