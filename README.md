# NetBox GitOps Controller

This Go tool enables **declarative management** (Infrastructure as Code) for a NetBox instance. It synchronizes definitions from YAML files idempotently against the NetBox API.

## 🚀 Key Features

  * **Single Source of Truth:** The YAML files in this repository represent the desired state of the network inventory.
  * **Idempotency:** The script calculates differences and only applies necessary changes. Repeated executions result in "No-Ops" (no API calls) if the state is already correct.
  * **Safety (Shared Management):**
      * Objects created by this tool are automatically stamped with a **`gitops`** tag.
      * **Opt-in Pruning (`--prune`):** Objects removed from YAML are deleted only when you pass `--prune`, and only if they carry the `gitops` tag — manually created objects are never touched. Combine with `--dry-run` to preview. See the Pruning section below.
  * **Auto-Wiring:** Physical cabling and LAG (Link Aggregation) members are automatically configured based on the YAML definition.
  * **Renames, not duplicates (`rename_from`):** Correcting a typo in a name, slug or other identifying field renames the existing object instead of creating a second one and orphaning the first. Supported on every managed object type. See the Renaming section below.
  * **Coverage:** Manages DCIM (sites, racks, device types, module types, devices, installed modules, cabling), IPAM (VRFs, VLAN groups, VLANs, prefixes), platforms/tenants, custom fields, and **virtualization** (cluster types/groups, clusters, virtual machines and VM interfaces with VLAN/IP assignment).
  * **Device & module type libraries:** Reads the community `devicetype-library` layout (`device-types/` and `module-types/`) alongside the native format, so vendor definitions can be vendored in as-is instead of retyped. Local definitions win over library ones of the same identity. See the Device type library section below.
  * **Proxmox provisioning (optional):** The *same* VM YAML can also provision the VMs in Proxmox via Terraform. VMs live in per-environment folders (`inventory/virtual/{prod,stage,playground}/`, one file per VM); `cmd/tfgen` renders each env to its own Terraform vars, and the `terraform/` module (`bpg/proxmox`) builds them into a separate state per environment — a second, independent consumer of one source of truth. Set `provision: true` on a VM to build it; otherwise it is documented in NetBox only. See [`docs/PLAN_YAML_VM_PIPELINE.md`](docs/PLAN_YAML_VM_PIPELINE.md) and [`terraform/README.md`](terraform/README.md).
  * **Type Safety:** All input data is validated against typed Go models before interacting with the API to prevent bad requests.


-----

## 📂 Project Structure

```text
.
├── definitions/          # Your Private Definitions (gitignored)
│   ├── sites/           # Data Center Locations
│   ├── device_types/    # Hardware Models (incl. Interface Templates)
│   ├── roles/           # Device Roles (e.g., Server, Leaf Switch)
│   ├── vlans/           # VLAN Definitions
│   ├── prefixes/        # IP Subnets / Prefixes
│   ├── platforms/       # OS / firmware families
│   ├── tenant_groups/   # Tenant Groups (tenancy)
│   ├── tenants/         # Tenants (tenancy)
│   ├── custom_fields/   # Custom field definitions (e.g. vmid)
│   ├── virtualization/  # cluster_types/, cluster_groups/, clusters/
│   └── ...              # Other NetBox object types
├── inventory/           # Your Private Inventory (gitignored)
│   ├── hardware/
│   │   ├── active/      # Active Servers & Switches
│   │   └── passive/     # Patch Panels, PDUs
│   └── virtual/         # Virtual Machines
├── example/             # Public Example Data for Tests
│   ├── definitions/     # Example definitions (for learning/testing)
│   └── inventory/       # Example inventory (for learning/testing)
├── terraform/           # Optional Proxmox provisioning (bpg/proxmox)
├── pkg/                 # Go Implementation (Core Logic)
│   ├── client/          # NetBox API Client
│   ├── lint/            # Cross-object checks (references, collisions)
│   ├── loader/          # YAML Data Loader
│   ├── models/          # Data Models
│   ├── reconciler/      # Synchronization Logic
│   ├── tfgen/           # VM YAML → Terraform vars (Proxmox)
│   └── utils/           # Utilities
└── cmd/                 # Command-Line Interfaces
    ├── netbox-gitops/   # Main Entry Point (NetBox sync)
    ├── tfgen/           # Generate Terraform vars from VM YAML
    └── yamlcheck/       # YAML syntax, model validation, cross-object lint
```

### 🔒 Private Data vs. Public Examples

**Important**: This repository separates your private data from public examples:

- **`definitions/` and `inventory/`**: Your actual private data (excluded from git via `.gitignore`)
- **`example/`**: Public example data used for tests and documentation (committed to git)

When you run the application, it uses your private `definitions/` and `inventory/` directories.
When you run tests, they use the `example/` directory.

-----

## 📝 Workflow: How to Add New Hardware

### Step 1: Define a Device Type (if new)

File: `definitions/device_types/servers.yaml` (any `*.yaml` under the
`definitions/device_types/` directory is loaded)

Here we define the "blueprint" including all physical ports. NetBox copies these ports *once* when a device is instantiated.

```yaml
- model: "Dell PowerEdge R640"
  slug: "dell-r640"
  manufacturer: "Dell"
  u_height: 1
  is_full_depth: true
  interfaces:
    - name: "idrac"
      type: "1000base-t"
      mgmt_only: true
    - name: "eth0"
      type: "25gbase-x-sfp28"
  # Optional: console_ports, console_server_ports, power_ports, power_outlets,
  # front_ports/rear_ports for patch panels, module_bays, device_bays
```

#### Reusing the community device type library

You do not have to hand-write a device type that already exists. The NetBox
community publishes thousands of them in the
[devicetype-library](https://github.com/netbox-community/devicetype-library),
and that format is consumed **unchanged** — one device type per file, laid out
as `<library>/<Manufacturer>/<model>.yaml`, with hyphenated component keys
(`console-ports`, `power-ports`, …).

Point the controller at a library checkout:

```bash
# Explicit path (a git submodule, a clone, or a directory of vendored files)
./netbox-gitops --devicetype-library ../devicetype-library/device-types

# Or via the environment
export DEVICETYPE_LIBRARY=../devicetype-library/device-types
```

With neither set, `definitions/device_type_library/` inside the data directory
is used if it exists — so vendoring just the models you need is as simple as
copying the files in:

```text
definitions/
├── device_types/              # Native format: a YAML list per file
│   └── servers.yaml
└── device_type_library/       # Community format: one device type per file
    └── Dell/
        └── poweredge-r650.yaml
```

Library entries are merged with your native definitions. If both define the
same slug, **your local definition wins** and the override is logged, so a
vendored library can be customised without editing the checkout. Fields the
library carries but this project does not manage (inventory items) are
reported rather than dropped silently.

See `example/definitions/device_type_library/` for a working example.

#### Module types from the same library

The library ships a `module-types/` tree alongside `device-types/`, covering
the hardware that goes *into* module bays — line cards, NIC mezzanines,
transceivers, power supplies. It is consumed the same way:

```bash
./netbox-gitops --moduletype-library ../devicetype-library/module-types
export MODULETYPE_LIBRARY=../devicetype-library/module-types
```

With neither set, `definitions/module_type_library/` inside the data directory
is used if it exists. A module type carries the same component templates as a
device type (interfaces, console and power ports, front/rear ports, module
bays — everything except device bays, which NetBox allows only on a device
type).

Component names in this library commonly contain the literal `{module}`
placeholder:

```yaml
interfaces:
  - name: '{module}-1GbE-0'
    label: '1'
    type: 1000base-t
```

NetBox substitutes it with the module bay position when the module is
installed, so the same module type in two bays produces `1-1GbE-0` and
`2-1GbE-0` rather than colliding. The placeholder is passed through verbatim.

> **Note:** NetBox requires a module type's manufacturer and model to be
> unique together, and module types have no slug. Two library files claiming
> the same pair cannot both be applied; the loader reports every such conflict
> at once so you can park the unwanted file with `--ignore-file`. The published
> library currently contains one (two Panduit part numbers sharing a model
> name).

#### Referring to a module type

A device installs a module by naming its bay and the module type:

```yaml
modules:
  - name: OCP-1                       # the module bay on the device
    module_type_slug: dell-ocp-25g
```

The slug is a local reference key, not something NetBox stores:

  * A module type defined **natively** uses the `slug` you gave it.
  * A module type read from a **library** has no slug in the file, so the key
    is derived from its model — `Dell OCP 25GbE Mezz` becomes
    `dell-ocp-25gbe-mezz`.

Different vendors may legitimately ship the same model name. When that happens
the bare name is ambiguous and is **not** accepted; use the
manufacturer-qualified form, which always works:

```yaml
    module_type_slug: dell/sfp-10g-sr
```

A run that detects the ambiguity warns and lists the qualified keys to use, so
an ambiguous reference fails with "module type not found" rather than quietly
installing another vendor's hardware.

### Step 2: Create Device Instances (Server/Switch)

File: `inventory/hardware/active/servers.yaml`

Inventory files support a grouped form: fields under `defaults` are merged
into every device in the file (device values win, `tags` are combined).
Since the ports already come from the device type template, an interface is
only listed here when it carries device-specific data — IPs, VLANs, cabling —
without repeating `type` or `enabled`.

```yaml
defaults:
  site_slug: "berlin"
  role_slug: "server"
  device_type_slug: "dell-r640"
  rack_slug: "rack-a01"
  status: "active"
  tags: ["gitops", "production"]

devices:
  - name: "srv-web-01"
    position: 10
    interfaces:
      - name: "idrac"
        ip: "10.0.10.50/24"   # string shorthand for `ip: { address: ... }`

      - name: "eth0"
        ip:
          address: "10.0.20.50/24"
          vrf: "Production"
        address_role: "primary"  # Sets the Primary IP on the Device object
        link:
          peer_device: "sw-leaf-01"
          peer_port: "Eth1/1"
          cable_type: "cat6a"    # Optional

  - name: "srv-web-02"
    position: 12
    interfaces:
      - name: "idrac"
        ip: "10.0.10.51/24"
```

The classic form — a plain YAML list of devices — is still fully supported;
`defaults` is just a shortcut, and every field can still be set per device.

### Step 3: Configure Switch Ports & VLANs

File: `inventory/hardware/active/switches.yaml`

```yaml
- name: "sw-leaf-01"
  # ... (Header params like site, role...) ...
  interfaces:
    - name: "Eth1/1"
      mode: "access"
      untagged_vlan: "Server-Vlan"
      # OR for Trunks:
      # mode: "tagged"
      # tagged_vlans: ["Vlan10", "Vlan20"]
```

### Conventions that keep the YAML small

  * **Ports live in the device type, not the inventory.** Define every
    physical port once as an interface template (Step 1). In the inventory,
    only list an interface when it needs device-specific settings, and never
    repeat its `type` — `type` on a concrete device is only needed for
    interfaces that do *not* exist in the template (e.g. virtual or LAG
    interfaces).
  * **Interfaces are enabled by default.** `enabled: true` is implied; only
    write `enabled: false` to shut a port.
  * **Declare each cable on one end only.** Pick a convention (e.g. the
    server/endpoint side owns the `link:`) — the reconciler wires both ends
    from a single declaration. Declaring the same cable from both sides works
    but means every re-patch is a two-file edit.
  * **Group shared fields in `defaults`.** One file per rack, role or
    hardware batch with a `defaults` block keeps each device down to its name,
    position and IPs.
  * **Start from the parked template.** `inventory/hardware/active/_new-server.yaml`
    holds a filled-in skeleton. The leading underscore parks it, so it is never
    applied (see *Parking a File* below) — copy a block out of it rather than
    editing it.

### Adding a server, end to end

With those conventions in place, a new machine in an existing rack is its
identity and its addresses, and nothing else:

```yaml
# inventory/hardware/active/servers.yaml  (defaults supply site, role,
# device type, rack, face, status and tags)
  - name: "berlin-srv-web-03"
    device_type_slug: "poweredge-r740"
    rack_slug: "berlin-rack-a01"
    position: 14
    serial: "WEB03-SN-KLMNO"
    tags: ["production"]

    interfaces:
      - name: "idrac"
        ip: "10.10.10.103/24"

      - name: "eth0"
        ip:
          address: "10.10.20.103/24"
          vrf: "Production"
        address_role: "primary"
        link:
          peer_device: "berlin-leaf-01"
          peer_port: "Eth1/4"
          cable_type: "dac-active"
```

Then check it before it reaches NetBox:

```bash
go run ./cmd/yamlcheck definitions inventory
```

Nothing else has to change — the switch side is wired from the server's
`link:`, and the ports come from the device type. The switch is edited only
when the new port needs a VLAN it does not already have.

### Checking the YAML before it reaches NetBox

`yamlcheck` reads the data directory and reports what is wrong with it without
opening a connection: YAML syntax, then the typed model validation (required
fields, cross-field constraints, NetBox choice values), then cross-object
checks that no single object can see on its own.

```bash
go run ./cmd/yamlcheck                       # definitions/, inventory/ and example/
go run ./cmd/yamlcheck path/to/data-dir      # a directory holding definitions/ and inventory/
go run ./cmd/yamlcheck --strict              # fail on warnings too
```

**Errors** — wrong regardless of what NetBox contains, so they fail the run:

| Check | What it catches |
|---|---|
| `duplicate-device`, `duplicate-vm`, `duplicate-interface` | the same name declared twice, where the second declaration silently overwrites the first |
| `duplicate-ip` | one address on two interfaces in the same VRF (the global table counts as one) |
| `duplicate-primary-ip` | two interfaces claiming the primary IP for one address family |
| `rack-collision` | two devices occupying the same rack unit, computed from each device type's `u_height` |
| `position-without-face` | a rack position with no `face`, which NetBox rejects outright (`Must specify rack face when defining rack position`) |
| `cable-conflict` | a port claimed by two different cables |
| `unknown-peer-port` | a cable to a port its peer's device type does not have |
| `untyped-interface` | an interface that is neither an interface template on the device type nor carries a `type`, so NetBox has nothing to create it from |
| `unknown-lag-member` | a LAG whose `members` name an interface the device does not have |
| `unknown-module-bay`, `unknown-device-bay`, `missing-device-bay` | a module or child device installed into a bay the parent's device type does not have |
| `unknown-site`, `unknown-role`, `unknown-rack`, `unknown-device-type`, `unknown-vlan`, `unknown-vrf`, `unknown-module-type`, `unknown-cluster`, `unknown-platform`, `unknown-tenant`, `unknown-parent-device` | a reference that resolves to nothing declared here (VLANs are matched within the device's site, as NetBox resolves them) |
| `invalid-ip`, `network-address`, `broadcast-address` | an address that is not a host address (a /31 point-to-point link is not flagged) |

**Warnings** — legitimate in some repositories, so they only fail under
`--strict`: `redundant-interface-type` and `redundant-enabled` (a value the
device type template or the default already supplies),
`interface-type-override`, `interface-not-in-template`,
`cable-declared-twice` (the same cable from both ends),
`undeclared-peer-device`, `ip-outside-prefix`, `unracked-device`,
`module-type-without-slug`.

Two things worth knowing about the reference checks:

  * A kind of object the repository declares **none** of is not checked at all.
    A repository that manages devices but not sites is a legitimate partial
    adoption, not one full of broken site references.
  * If you do declare everything but reference an object created outside this
    repository, `--allow-undeclared-refs` reports those as warnings instead.

-----

## ⚠️ Important Concepts & Troubleshooting

### Renaming an Object (Fixing a Typo)

Every object is matched against NetBox by an **identifying field**. That is what
makes the sync idempotent — and it is also why editing that field is not an
ordinary change. The old value stops matching anything, so the object is created
a *second* time and the original is left behind, still holding every reference
that pointed at it. Nothing errors; you just quietly end up with two.

Declare where the object came from and it is renamed in place instead:

```yaml
- name: Rack 01          # was "Rakc 01"
  slug: rack-01
  site_slug: frankfurt
  rename_from: Rakc 01   # ← the previous identity
```

Once the sync has run, NetBox holds the new value, the declaration becomes a
no-op, and you can delete the line. Leaving it in place is harmless.

**Which field is the identity?** It differs per object type, because NetBox
does. `rename_from` always holds the previous value of *that* field:

| Object | Identified by | `rename_from` holds |
|---|---|---|
| Site, device role, platform, tenant, tenant group, tag, VLAN group, cluster type, cluster group, device type | `slug` | the old slug |
| Rack, device, interface, VM, VM interface, cluster, VRF, custom field | `name` | the old name |
| Module type | `model` | the old model |
| VLAN | `vid` | the old VID, quoted (`"201"`) |
| Prefix | `prefix` | the old CIDR |

Two consequences worth knowing:

  * For a slug-identified object, changing only the **name** needs no
    declaration — the slug still matches, so it is a plain update. The same goes
    for a VLAN's name, since a VLAN is identified by its VID.
  * A rename changes *what an object is called*, not where it lives. The scope
    (site, device, cluster) is carried over unchanged; moving an object between
    scopes is a different operation and is not what this does.

**Safety.** `rename_from` describes an object's past, which is weaker evidence
than a plain identity match — a typo in it can name a real object that has
nothing to do with your declaration. So:

  * Only objects carrying the `gitops` tag can be renamed. To bring an existing
    unmanaged object under management, declare it under the name it *already*
    has; the next sync adopts and tags it, and it can be renamed after that.
  * If `rename_from` matches more than one object, the sync fails rather than
    guessing.
  * If objects exist under **both** the old and the new identity, nothing is
    renamed and you get a warning — which one survives is your call, not the
    tool's.
  * A `rename_from` that matches nothing is not an error; that is simply what it
    looks like once it has been applied.

`--dry-run` reports a rename as the update it is, without writing.

### Parking a File (Ignored Files)

Not everything in the repository should be applied. Inventory owned by another
system, a draft you are not ready to sync, a reference copy — all of these can
stay in place and be skipped:

```bash
# Default: filenames starting with an underscore are skipped
inventory/hardware/active/_imported-from-cmdb.yaml   # not applied

# Override the patterns (globs, matched against the filename)
./netbox-gitops --ignore-file '_*.yaml' --ignore-file 'imported-*.yaml'
export IGNORED_FILES='_*.yaml,imported-*.yaml'

# Apply everything, including parked files
./netbox-gitops --include-ignored-files
```

Every skipped file is logged, so a parked file never goes unnoticed. Patterns
match the **filename only**, not the path — a file inside a directory whose
name matches a pattern is still loaded. An invalid glob fails at startup rather
than silently matching nothing.

> **⚠️ Pruning:** A parked file is invisible to the controller, so `--prune`
> cannot tell its objects apart from orphans. Two cases, and only you can tell
> them apart:
>
> - Objects **another system owns** were never created here, carry no `gitops`
>   tag, and are safe — pruning never touches untagged objects.
> - Objects **this controller previously created** from a file you have now
>   parked still carry the tag and **will be deleted** as orphans.
>
> A run that both skips files and uses `--prune` warns and lists the skipped
> files before deleting anything. Preview with `--dry-run --prune` first.

### Front Ports Across NetBox Releases

NetBox 4.6 replaced a front port's singular `rear_port` / `rear_port_position`
fields with a `rear_ports` list of `{position, rear_port, rear_port_position}`
mappings — and **accepts the old fields silently rather than rejecting them**,
so sending the wrong shape leaves the port created but unwired.

The controller picks the shape from the release reported by `/api/status/`, so
patch panels wire correctly on both older releases and 4.6+. Nothing in your
YAML changes; `rear_port` and `rear_port_position` stay as they are.

### NetBox Version Compatibility

On startup the controller reads `/api/status/`, logs the detected release, and
refuses to run against NetBox older than **3.6** — the release that renamed the
device `device_role` field to `role`. Without this check an unsupported server
produces a scatter of `400 Bad Request` errors on individual fields rather than
one clear message.

A server whose version cannot be determined (no status endpoint, a proxy that
rewrites it) is reported as a warning and the run continues: the check exists
to explain failures, not to become a new way to fail.

### Phase Order (Dependency Model)

NetBox rejects an object whose references do not exist yet — a device needs its
site, role and device type; a prefix needs its VRF. The controller therefore
reconciles in a fixed order derived from the object types themselves, **not**
from file or directory names. You never have to number or sort your YAML: the
layout under `definitions/` and `inventory/` is free-form, and the same order
runs every time.

| # | Phase | Objects, in reconcile order |
|---|-------|-----------------------------|
| 1 | `foundation` | tags → roles → custom fields → platforms → tenant groups → tenants → sites → racks |
| 2 | `network` | VRFs → VLAN groups → VLANs → prefixes |
| 2 | `device-types` | manufacturers → device types (incl. interface/port/bay templates) → module types |
| 3 | `devices` | per device: device → interfaces → IP addresses → primary IP → modules → device bays → front/rear ports; **then all cables** |
| 4 | `virtualization` | cluster types → cluster groups → clusters → VMs → VM interfaces → VM IPs |

Two orderings inside a phase are worth knowing about, because both solve
problems that a "number your files" convention leaves to the user:

  * **Cables run last, in a second pass.** A cable needs the ports on *both*
    ends to exist, so every `link:` found while reconciling devices is queued
    and applied only after all devices and their ports are in place. Peer order
    in the YAML is irrelevant.
  * **Parents are reconciled before their children.** A device with
    `parent_device` is installed into a bay on that parent, so the parent must
    exist first. The device list is topologically sorted before reconciliation,
    at any nesting depth — a blade may be declared above its chassis, or in an
    alphabetically earlier file, and the run still succeeds. A `parent_device`
    that is not declared in the run is assumed to exist in NetBox already; a
    cycle (`a` parented to `b` parented to `a`) is reported as an error before
    any change is applied.

The phase order is asserted by `TestValidPhasesCreationOrder`, and its mirror
image — the reverse order used for `--prune` — by
`TestPruneTargetsReverseDependencyOrder`.

### The `gitops` Tag

  * The script automatically tags every object it creates with `GitOps Managed` (slug: `gitops`).
  * **Default behavior:** If you remove a device from the YAML file, it is **not** deleted from NetBox — the tool only creates and updates objects unless you opt into pruning.
  * **Pruning (`--prune`):** Run a sync with `--prune` to delete orphans — objects that still carry the `gitops` tag but are no longer declared in YAML. Only managed objects are ever deleted; manually created objects (without the tag) are protected. Combine with `--dry-run` to preview the deletions before applying them. Pruning is scoped to the phases that run (`--only`) and cannot be combined with `--site`/`--device`/`--vm`. See `docs/MISSING_FEATURES.md` for details and current limitations.

### Common Errors

**Error: "400 Bad Request: {'type': ['This field may not be blank.']}"**

  * **Cause:** A device interface or template is missing the `type` definition in the YAML.
  * **Solution:** Ensure every interface in your `definitions/device_types/` files has a valid type (e.g., `1000base-t`, `virtual`, `lag`).

**Cables are "flapping" (Deleting... Creating... on every run)**

  * **Cause:** You likely assigned two different devices to the same peer port.
  * **Solution:** Check your `link:` definitions. A port can only support one cable connection.

**Changes to Device Type (e.g., adding a port) do not appear on existing servers**

  * **Cause:** This is standard NetBox behavior. Modifying the "Blueprint" (Device Type) does not automatically update already created "Instances" (Devices).
  * **Solution:** Either recreate the device (Delete + Sync) or manually update the components in NetBox using the "Sync components" button on the Device Type page. Global attributes like `u_height` update immediately.

  

## 🛠 Local Development

### 1\. Clone Repository

```bash
git clone <repo-url>
cd netbox-gitops
```

### 2\. Build the Binary

Requires Go 1.24 or later. Dependencies are managed via Go modules and downloaded automatically.

```bash
go build -o netbox-gitops ./cmd/netbox-gitops/
```

### 3\. Environment Configuration

Create a `.env` file in the root directory:

```ini
NETBOX_URL=https://netbox.example.com
NETBOX_TOKEN=your_api_token_here
# Optional: Disable SSL verification (Dev environments only)
# IGNORE_SSL_ERRORS=True
```

## ▶️ Usage

### 1\. Dry-Run (Simulation)

Shows exactly what changes *would* be applied without actually touching NetBox. **Always run this first\!**

```bash
./netbox-gitops --dry-run
```

Every run ends with a terraform-style plan summary:

```text
Plan: 3 to create, 1 to update, 0 to delete, 41 unchanged
```

### 2\. Apply Changes

Executes the synchronization against the NetBox API.

```bash
./netbox-gitops
```

### 3\. Drift Detection (CI-friendly)

With `--detailed-exitcode` the process exits with code `2` when changes are
pending (exit `0` means NetBox is in sync with the YAML). A scheduled CI job
becomes a drift monitor with zero extra infrastructure:

```bash
./netbox-gitops --dry-run --detailed-exitcode
```

### 4\. Machine-Readable Plan Output

`--output json` prints the planned (or applied) changes as JSON on stdout;
all logs move to stderr so stdout stays clean for parsing. Useful for posting
a `terraform plan`-style comment on a merge request:

```bash
./netbox-gitops --dry-run --output json 2>/dev/null > plan.json
```

```json
{
  "dry_run": true,
  "summary": { "create": 3, "update": 1, "delete": 0, "unchanged": 41 },
  "changes": [
    {
      "action": "create",
      "app": "dcim",
      "endpoint": "sites",
      "object": "slug=berlin-dc",
      "fields": { "name": "Berlin DC", "slug": "berlin-dc" }
    }
  ]
}
```

### 5\. Selective Sync

Restrict a run to specific phases or devices — handy for debugging and
targeted hotfixes:

```bash
# Only reconcile foundation objects (tags, roles, sites, racks)
./netbox-gitops --only foundation

# Only network (VRFs, VLAN groups, VLANs, prefixes) and device types
./netbox-gitops --only network,device-types

# Only devices of one site
./netbox-gitops --only devices --site berlin-dc

# A single device
./netbox-gitops --device srv-web-01

# Only virtual machines, and just one of them
./netbox-gitops --only virtualization --vm web-01
```

Valid `--only` values: `foundation`, `network`, `device-types`, `devices`,
`virtualization`. `--site` filters the device and virtualization phases by site
slug; `--device` filters a single device; `--vm` filters a single virtual
machine by name. Selected phases always run in the fixed dependency order — see
[Phase Order](#phase-order-dependency-model) — regardless of the order you list
them in.

> **Note:** Skipped phases are not validated — if you sync `--only devices`,
> the referenced sites, roles and device types must already exist in NetBox.
> Likewise `--only virtualization` assumes its sites, roles, platforms, tenants
> and VLANs already exist.
>
> **Note:** `--site` matches a VM only if the VM declares that `site_slug`
> directly; a clustered VM (whose site comes from its cluster) is not matched by
> `--site`. Use `--vm` to target such a VM by name.

### 6\. Pruning Orphans

By default the controller only creates and updates objects. With `--prune` it
also deletes orphans: objects that still carry the `gitops` managed tag but are
no longer declared in YAML. Manually created objects (those without the tag)
are never touched.

```bash
# Preview what pruning would delete (no changes applied)
./netbox-gitops --dry-run --prune

# Apply, deleting orphaned managed objects
./netbox-gitops --prune

# Prune only the network phase's orphans (VRFs, VLAN groups, VLANs, prefixes)
./netbox-gitops --only network --prune
```

Pruning runs after all selected phases and deletes endpoints in reverse
dependency order (device/VM children first, then sites/platforms/tenants/tags
last) to respect NetBox foreign-key constraints. It is scoped to the phases that
run via `--only`, and cannot be combined with `--site`/`--device`/`--vm` (a
filtered run would delete the out-of-scope objects the filter excluded). Device children
(interfaces, IP addresses, front/rear ports, modules) are pruned too; cables
are not (they are untagged and NetBox removes them when their port or device is
deleted). See `docs/MISSING_FEATURES.md` for details.

## 📚 Example Files

The `example/` directory ships a small, self-contained dataset used by the test
suite and as a format reference. It covers two sites (Berlin DC, Test Lab) and a
few objects of each supported type:

- 2 sites, 5 device roles, 2 platforms, 1 tenant (+ group)
- 2 VRFs, 2 VLAN groups, 3 VLANs, 3 prefixes, 3 racks
- 6 device types (native format) + 1 in the community library format,
  2 module types, 2 VM custom fields (`vmid`, `vm_template_id`)
- 8 hardware devices — including a blade chassis with two child blades, a GPU
  server, and a patch panel (front/rear ports)
- 2 virtual machines (one provisioned in Proxmox, one NetBox documentation-only)

See **[EXAMPLES.md](./EXAMPLES.md)** for the full breakdown, file layout, and the
key concepts each file demonstrates.

### Quick Test

To see the examples in action:

```bash
# Preview what would be created (uses the example data)
./netbox-gitops --dry-run --data-dir example

# Apply to your NetBox instance (requires .env configuration)
./netbox-gitops --data-dir example
```

**Note**: The examples create a complete test infrastructure suitable for learning and development. For production use, customize the files to match your actual environment.

-----

## 📄 License

Licensed under the [Apache License, Version 2.0](./LICENSE).

Every source file carries an `SPDX-License-Identifier: Apache-2.0` line, so
licence scanners in a CI pipeline can identify the project without parsing the
full text.
