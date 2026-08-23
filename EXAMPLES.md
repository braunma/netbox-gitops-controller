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
| Hardware devices  | 8     | 2 servers, switch, GPU server, chassis + 2 blades, patch panel |
| Virtual machines  | 2     | `web-01` (clustered), `edge-01` (site-only)                     |
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
│   ├── device_type_library/                   # community library format
│   │   └── Dell/poweredge-r650.yaml           # one type per file, hyphenated keys
│   └── virtualization/
│       ├── cluster_types/, cluster_groups/, clusters/
└── inventory/                   # Concrete instances
    ├── hardware/
    │   ├── active/              # servers.yaml, switches.yaml, gpu-servers.yaml, chassis.yaml
    │   └── passive/             # patchpanels.yaml
    └── virtual/                 # per-env folders, one YAML per VM
        ├── prod/                # web-01.yaml      (clustered VM)
        ├── stage/               # (empty here)
        └── playground/          # edge-01.yaml     (site-only VM)
```

## Key concepts demonstrated

### Network segmentation
VRFs (`Management`, `Production`) provide routing isolation; VLANs live in
per-site VLAN groups and are referenced from prefixes and interfaces by name,
resolved against the object's site.

### Devices and interfaces
A device references its site, role, device type and rack by slug. Inventory
files support a grouped form where a `defaults` block is merged into every
device (device values win, tags are combined), so shared fields are written
once (see `inventory/hardware/active/servers.yaml`):

```yaml
defaults:
  device_type_slug: "example-server-r100"
  role_slug: "server"
  site_slug: "berlin-dc"
  rack_slug: "rack-a01"
  status: "active"
  tags: ["gitops", "production"]

devices:
  - name: "example-server-01"
    position: 10
    interfaces:
      - name: "eth1"
        description: "Data interface"
```

Interface `type` and `enabled` are not repeated in the inventory: the ports
come from the device type's interface templates, and interfaces are enabled
unless explicitly set to `enabled: false`. `type` is only needed for
interfaces that don't exist in the template. The classic form — a plain YAML
list of devices — remains fully supported (see `switches.yaml` and
`chassis.yaml`).

An interface's `ip:` accepts either a plain CIDR string (`ip: "10.0.0.1/24"`)
or the full mapping form with `dns_name`, `vrf`, `status` etc.

### Checking a data directory without NetBox

`yamlcheck` validates any directory holding `definitions/` and `inventory/` —
syntax, the typed models, and then the cross-object checks that no single
object can see on its own: a name or IP used twice, two devices in one rack
unit, a port claimed by two cables, a slug that matches nothing declared, an
interface the device type does not have.

```bash
go run ./cmd/yamlcheck example        # this example tree
go run ./cmd/yamlcheck               # definitions/, inventory/ and example/
go run ./cmd/yamlcheck --strict      # fail on warnings as well as errors
```

The full list of checks and their severities is in the README section
*Checking the YAML before it reaches NetBox*.

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

VMs are organised one file per VM under a per-environment folder
(`inventory/virtual/{prod,stage,playground}/`); the controller scans
`inventory/virtual/` recursively, so every environment is documented in NetBox.

```yaml
# inventory/virtual/prod/web-01.yaml
- name: "web-01"
  vmid: 101                        # Proxmox VMID, stored in NetBox as a custom field
  vm_template_id: 800              # template VMID this VM was cloned from
  node: "pve-01"                   # hypervisor node it runs on
  cluster: "berlin-prod-cluster"   # clustered VM; site inherited from cluster
  role_slug: "vm"                  # role must have vm_role: true in NetBox
  platform: "ubuntu-22-04"
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

The `vmid`, `vm_template_id` and `node` fields describe where the VM lives on
its hypervisor. NetBox has no native slot for the first two, so they are stored
as custom fields; `node` is documentation only. `vmid`, `vcpus`, `memory`,
`disk` and `status` are the keys a Proxmox collector run may rewrite from
observed facts — see [`docs/INGEST.md`](docs/INGEST.md).

### Auto-wiring
The controller wires cables automatically from a `link:` block on any
interface, front port or rear port — it creates the cable bidirectionally
between the two endpoints, so each cable is declared on **one end only**
(convention: the server/endpoint side owns the link, see `servers.yaml`):

```yaml
interfaces:
  - name: "eth1"
    link:
      peer_device: "example-switch-01"
      peer_port: "GigabitEthernet1/0/1"
      cable_type: "cat6"           # optional
```

### GitOps tag
Every object the controller creates is tagged `gitops`. Only tagged objects are
eligible for deletion by `--prune`; manually created objects are protected. See
the README for the full pruning semantics.

## Customizing

Copy the example files into a `definitions/` and `inventory/` directory at the
repository root (both are gitignored) and edit them for your environment. The
application uses those by default; the test suite keeps using `example/`.
