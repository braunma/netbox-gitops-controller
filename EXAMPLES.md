# Example Inventory & Definitions Guide

This repository includes comprehensive example files demonstrating the NetBox GitOps Controller's capabilities. These examples showcase a realistic multi-site infrastructure with various device types, network configurations, and cabling scenarios.

## 📋 Overview

The examples model a fictional infrastructure across three sites:
- **Berlin DC** - Primary production data center
- **Frankfurt DC** - Secondary production site
- **Munich Lab** - Development and testing facility

## 🗂️ Example File Structure

```
definitions/                    # Global blueprints and definitions
├── sites/
│   └── sites.yaml             # 4 example sites (Berlin, Frankfurt, Munich, Hamburg)
├── racks/
│   └── racks.yaml             # 6 racks across different sites
├── roles/
│   └── roles.yaml             # 8 device roles (Server, Switch, Storage, etc.)
├── extras/
│   └── tags.yaml              # 7 custom tags for organization
├── vlan_groups/
│   └── vlan_groups.yaml       # 3 VLAN groups (per-site organization)
├── vlans/
│   └── vlans.yaml             # 8 VLANs across sites
├── vrfs/
│   └── vrfs.yaml              # 4 VRFs (Production, Management, Storage, Lab)
├── prefixes/
│   └── prefixes.yaml          # 12 IP prefixes with container hierarchies
├── module_types/
│   └── gpu-modules.yaml       # 6 module types (GPUs, NICs)
└── device_types/
    ├── switches/
    │   └── S5248F-ON.yaml     # Dell 48-port 25G + 6x100G switch
    ├── servers/
    │   ├── poweredge-r740.yaml    # 2U server with multiple NICs
    │   ├── poweredge-r7615.yaml   # AMD-based server
    │   └── poweredge-xe9680.yaml  # AI/ML optimized server
    ├── storage/
    │   └── isilon-a300.yaml   # Storage array
    └── patchpanel-*.yaml      # Fiber patch panels (12/48 port)

inventory/                      # Concrete hardware instances
├── hardware/
│   ├── active/
│   │   ├── servers.yaml       # 6 server instances
│   │   └── switches.yaml      # 3 switch instances
│   └── passive/
│       └── patchpanels.yaml   # 4 patch panel instances
```

## 🎯 Key Concepts Demonstrated

### 1. Multi-Site Infrastructure

**Sites** represent physical locations:
```yaml
- name: "Berlin DC"
  slug: "berlin-dc"
  status: "active"
  time_zone: "Europe/Berlin"
  tags: ["gitops", "production"]
```

### 2. Network Segmentation

**VRFs** provide routing isolation:
- **Production VRF** (`65000:100`) - Production workloads
- **Management VRF** (`65000:10`) - Out-of-band management
- **Storage VRF** (`65000:30`) - Storage traffic
- **Lab VRF** (`65000:999`) - Testing environment

**VLANs** provide Layer 2 segmentation:
- VLAN 10 - Management (iDRAC, IPMI)
- VLAN 20 - Server Production
- VLAN 30 - Storage (iSCSI/NFS)
- VLAN 40 - AI/ML Compute

### 3. IP Address Management

**Prefixes** define IP subnets with proper hierarchy:

```yaml
# Container prefix (organizational)
- prefix: "10.10.0.0/16"
  status: "container"
  site_slug: "berlin-dc"

# Usable subnet within container
- prefix: "10.10.10.0/24"
  status: "active"
  is_pool: true              # Available for IP allocation
  vlan_name: "Management"
  vrf_name: "Management"
```

### 4. Device Cabling (Auto-Wiring)

The controller automatically creates cables based on `link` definitions:

```yaml
interfaces:
  - name: "eth0"
    ip:
      address: "10.10.20.101/24"
    link:
      peer_device: "berlin-leaf-01"
      peer_port: "Eth1/1"
      cable_type: "dac-active"  # Direct Attach Copper
```

This single definition creates:
- IP address on the interface
- Cable object in NetBox
- Bidirectional link between devices

### 5. Advanced Server Configuration

**AI/ML Server Example** (`berlin-srv-ai-01`):
- Multiple network interfaces (mgmt + high-speed compute)
- 100G interfaces for AI workloads
- MTU 9000 for jumbo frames
- Dedicated AI-Compute VLAN
- Ready for GPU module installation

**Dual-homed Server** (production web servers):
- Primary interface (eth0) with production IP
- iDRAC out-of-band management
- Multiple VRFs for traffic separation

### 6. Switch Port Configuration

**Access Port** (single VLAN):
```yaml
- name: "Eth1/1"
  mode: "access"
  untagged_vlan: "Server-Production"
```

**Trunk Port** (multiple VLANs):
```yaml
- name: "Eth1/49"
  mode: "tagged"
  tagged_vlans: ["Management", "Server-Production", "Storage"]
  mtu: 9000
```

### 7. Patch Panel Infrastructure

Patch panels use **front_ports** and **rear_ports** to model fiber infrastructure:

```yaml
front_ports:
  - name: "1"
    type: "lc"
    rear_port: "1"              # Maps to rear port
    label: "To Rack A02 Port 1"

rear_ports:
  - name: "1"
    type: "lc"
    label: "Backbone to MDF"    # Main Distribution Frame
```

### 8. Tagging Strategy

All GitOps-managed objects include the `gitops` tag:
- Enables safe cleanup (only tagged objects can be deleted)
- Protects manually created NetBox objects
- Additional tags for categorization (production, lab, ai-ml)

## 🚀 Using the Examples

### Option 1: Use Examples As-Is (Testing)

1. Set up NetBox connection:
```bash
cat > .env << EOF
NETBOX_URL=https://netbox.example.com
NETBOX_TOKEN=your_api_token_here
EOF
```

2. Run dry-run to preview:
```bash
python src/main.py --dry-run
```

3. Apply to NetBox:
```bash
python src/main.py
```

### Option 2: Customize for Your Environment

1. **Update Sites**: Edit `definitions/sites/sites.yaml` with your locations
2. **Update Networks**: Modify VLANs and IP prefixes in `definitions/vlans/` and `definitions/prefixes/`
3. **Update Device Types**: Add or modify templates in `definitions/device_types/`
4. **Create Your Inventory**: Edit files in `inventory/hardware/` to match your equipment

### Option 3: Start Fresh

Remove example inventory but keep definitions as templates:
```bash
# Remove example devices
rm inventory/hardware/active/*.yaml
rm inventory/hardware/passive/*.yaml

# Keep example definitions as reference
# Modify them to match your infrastructure
```

## 📚 Example Scenarios

### Scenario 1: Simple Server Addition

Add a new web server to Berlin DC:

```yaml
# In inventory/hardware/active/servers.yaml
- name: "berlin-srv-web-03"
  site_slug: "berlin-dc"
  device_type_slug: "poweredge-r740"
  role_slug: "server"
  rack_slug: "berlin-rack-a01"
  position: 14
  status: "active"

  interfaces:
    - name: "idrac"
      ip:
        address: "10.10.10.103/24"
        vrf: "Management"

    - name: "eth0"
      ip:
        address: "10.10.20.103/24"
        vrf: "Production"
      address_role: "primary"
      link:
        peer_device: "berlin-leaf-01"
        peer_port: "Eth1/5"
```

### Scenario 2: Adding a New VLAN

```yaml
# In definitions/vlans/vlans.yaml
- vid: 50
  name: "DMZ"
  site_slug: "berlin-dc"
  status: "active"
  description: "Demilitarized zone"
  tags: ["gitops"]

# In definitions/prefixes/prefixes.yaml
- prefix: "10.10.50.0/24"
  description: "Berlin DC - DMZ network"
  site_slug: "berlin-dc"
  vlan_name: "DMZ"
  vrf_name: "Production"
  status: "active"
  is_pool: true
  tags: ["gitops"]
```

### Scenario 3: Cross-Rack Fiber Connection

```yaml
# Patch panel in Rack A01 (front port connects to room)
- name: "berlin-pp-01"
  front_ports:
    - name: "1"
      rear_port: "1"
      link:  # This cable goes to adjacent rack
        peer_device: "berlin-pp-02"
        peer_port: "1"
        cable_type: "smf"  # Single-mode fiber
```

## 🔍 Validation & Troubleshooting

### Check for Errors

All data is validated by Pydantic models before API calls:

```bash
python src/main.py --dry-run 2>&1 | grep -i error
```

### Common Issues

**"Device type not found"**
- Ensure the `device_type_slug` exists in `definitions/device_types/`
- Device types must be synced before devices

**"VLAN not found"**
- Verify the VLAN name matches exactly (case-sensitive)
- Check the VLAN is defined for the correct site

**"IP address already assigned"**
- Each IP can only be assigned once
- Use different IPs or remove duplicates

**"Cable already exists"**
- A port can only have one cable
- Check for duplicate `link` definitions

## 🎓 Learning Path

1. **Start with the README.md** - Understand core concepts
2. **Review definitions/** - Learn about blueprints (device types, VLANs, etc.)
3. **Study inventory/switches.yaml** - See port configurations and trunking
4. **Study inventory/servers.yaml** - See IP assignment and cabling
5. **Study patchpanels.yaml** - Understand structured cabling
6. **Run --dry-run** - See what would happen without making changes
7. **Experiment** - Modify examples and observe behavior

## 📖 Additional Resources

- [NetBox Documentation](https://docs.netbox.dev/)
- [Pydantic Validation](https://docs.pydantic.dev/)
- Device Type Library: [devicetype-library](https://github.com/netbox-community/devicetype-library)

## ⚠️ Important Notes

### Production Use

Before using in production:
1. **Backup NetBox** - Always backup before running automation
2. **Test in staging** - Create a NetBox test instance first
3. **Review changes** - Always run `--dry-run` first
4. **Version control** - Commit changes to git before applying
5. **Incremental rollout** - Start with a small subset of devices

### GitOps Tag Safety

Objects with the `gitops` tag can be:
- Updated by the controller
- Deleted if removed from YAML files

Objects **without** the `gitops` tag are:
- Ignored by the controller
- Protected from deletion
- Safe to manage manually in NetBox

This allows **hybrid management** - some objects via GitOps, others manually.

## 🔄 Migration to Go

These YAML files are designed to be forward-compatible with the planned Go refactor:
- Pure declarative YAML (no Python-specific features)
- Standard Pydantic/JSON Schema validation patterns
- RESTful API interactions (easily portable)

The folder structure and file formats will remain the same during the Go migration.
