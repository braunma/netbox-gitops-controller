# Example Inventory & Definitions Guide

The `example/` directory ships a small, self-contained dataset that the test
suite runs against and that doubles as a reference for the YAML format. Run the
controller against it with `--data-dir example` (the binary also falls back to
`example/` automatically when no `definitions/` directory exists in the working
directory):

```bash
./netbox-gitops --dry-run --data-dir example
```

## What the example models

Two sites — **Berlin DC** (`berlin-dc`) and **Test Lab** (`test-lab`) — with a
few objects of each supported type:

| Category          | Count | Items                                                             |
|-------------------|-------|-------------------------------------------------------------------|
| Sites             | 2     | Berlin DC, Test Lab                                               |
| Device roles      | 5     | Server, Switch, Storage, Patch Panel, Virtual Machine            |
| Platforms         | 2     | Ubuntu 22.04 LTS, Debian 12                                      |
| Tenant groups     | 1     | Internal                                                         |
| Tenants           | 1     | Platform Engineering                                            |
| Custom fields     | 2     | `vmid`, `vm_template_id` (consumed by virtual machines)         |
| VRFs              | 2     | Management, Production                                          |
| VLAN groups       | 2     | Berlin DC VLANs, Test Lab VLANs                                 |
| VLANs             | 3     | Management, Production Network, Test Network                   |
| Prefixes          | 3     | 10.0.0.0/8, 10.0.100.0/24, 10.0.200.0/24                       |
| Racks             | 3     | Rack A-01, Rack A-02, Test Rack                                |
| Module types      | 2     | 10G SFP+ module, GPU A100                                       |
| Device types      | 6     | server, switch, GPU server, blade chassis, blade, patch panel  |
| Hardware devices  | 7     | server, switch, GPU server, chassis + 2 blades, patch panel    |
| Virtual machines  | 2     | `web-01` (provisioned in Proxmox), `edge-01` (doc-only)        |
| Virtualization    | —     | cluster type Proxmox VE, group Production, cluster `berlin-prod-cluster` |

## File layout

```text
example/
├── definitions/                 # Blueprints and global objects
│   ├── sites/sites.yaml
│   ├── racks/racks.yaml
│   ├── roles/roles.yaml
│   ├── platforms/platforms.yaml
│   ├── tenant_groups/, tenants/
│   ├── custom_fields/custom_fields.yaml      # the `vmid` field
│   ├── extras/tags.yaml
│   ├── vrfs/, vlan_groups/, vlans/, prefixes/
│   ├── module_types/module_types.yaml
│   ├── device_types/                          # example-*.yaml (one per type)
│   │   ├── example-server.yaml
│   │   ├── example-switch.yaml
│   │   ├── example-gpu-server.yaml
│   │   ├── example-chassis.yaml               # device bays
│   │   ├── example-blade.yaml                 # child device (u_height 0)
│   │   └── example-patch-panel.yaml           # ports defined per instance
│   └── virtualization/
│       ├── cluster_types/, cluster_groups/, clusters/
└── inventory/                   # Concrete instances
    ├── hardware/
    │   ├── active/              # servers.yaml, switches.yaml, gpu-servers.yaml, chassis.yaml
    │   └── passive/             # patchpanels.yaml
    └── virtual/                 # vms.yaml
```

## Key concepts demonstrated

### Network segmentation
VRFs (`Management`, `Production`) provide routing isolation; VLANs live in
per-site VLAN groups and are referenced from prefixes and interfaces by name,
resolved against the object's site.

### Devices and interfaces
A device references its site, role, device type and rack by slug, and lists its
interfaces inline (see `inventory/hardware/active/servers.yaml`):

```yaml
- name: "example-server-01"
  device_type_slug: "example-server-r100"
  role_slug: "server"
  site_slug: "berlin-dc"
  rack_slug: "rack-a01"
  position: 10
  status: "active"
  interfaces:
    - name: "eth0"
      type: "1000base-t"
    - name: "eth1"
      type: "10gbase-x-sfpp"
```

### Self-healing device bays and blades
`example-chassis.yaml` defines device-bay templates; the controller auto-creates
the bays on each chassis instance, and `chassis.yaml` installs child blades into
them via `parent_device` + `device_bay`. Child device types use `u_height: 0`
and `subdevice_role: "child"`. See [`example/ADVANCED_FEATURES.md`](example/ADVANCED_FEATURES.md).

### Patch panels (front/rear ports)
Passive devices model structured cabling with `front_ports` mapped to
`rear_ports` (see `inventory/hardware/passive/patchpanels.yaml`):

```yaml
front_ports:
  - name: "Port 1"
    type: "8p8c"
    rear_port: "Rear Port 1"
    rear_port_position: 1
rear_ports:
  - name: "Rear Port 1"
    type: "8p8c"
    positions: 1
```

### Virtualization (clusters & VMs)
A **cluster** ties a cluster type (hypervisor) and optional cluster group / site
/ tenant together; **virtual machines** live on a cluster (inheriting its site)
or directly on a site. VM interfaces reuse the device interface semantics
(VLANs, IPs, primary IP) but are not cabled. Platforms and tenants are managed
in the foundation phase and referenced here by slug.

```yaml
# inventory/virtual/vms.yaml
- name: "web-01"
  provision: true                  # also create this VM in Proxmox (optional)
  vmid: 101                        # Proxmox VMID, also stored in NetBox
  vm_template_id: 800              # template VMID to clone (provisioning only)
  node: "pve-01"                   # target Proxmox node (provisioning only)
  cluster: "berlin-prod-cluster"   # clustered VM; site inherited from cluster
  role_slug: "vm"                  # role must have vm_role: true in NetBox
  platform: "ubuntu-22-04"         # NetBox documentation (not used to clone)
  vcpus: 4
  memory: 8192                     # MB
  interfaces:
    - name: "eth0"
      mode: "access"
      untagged_vlan: "Management"  # resolved at the cluster's site
      ip:
        address: "10.0.100.21/24"
      address_role: "primary"      # promotes to the VM's primary_ip4
```

The `provision`/`vmid`/`vm_template_id`/`node` fields are only needed if you also
provision the VMs in Proxmox from the same YAML — NetBox itself ignores them
(except the `vmid`/`vm_template_id` it stores as custom fields). Without
`provision: true` a VM is documentation-only. See
[`terraform/README.md`](terraform/README.md) for that optional pipeline.

### Auto-wiring (feature reference)
The bundled example data does not connect cables, but the controller can wire
them automatically from a `link:` block on any interface, front port or rear
port — it creates the cable bidirectionally between the two endpoints:

```yaml
interfaces:
  - name: "eth0"
    link:
      peer_device: "example-switch-01"
      peer_port: "GigabitEthernet1/0/1"
      cable_type: "cat6a"          # optional
```

### GitOps tag
Every object the controller creates is tagged `gitops`. Only tagged objects are
eligible for deletion by `--prune`; manually created objects are protected. See
the README for the full pruning semantics.

## Customizing

Copy the example files into a `definitions/` and `inventory/` directory at the
repository root (both are gitignored) and edit them for your environment. The
application uses those by default; the test suite keeps using `example/`.
